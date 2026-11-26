// Langfuse callback wiring: Eino spans export through the process-wide
// TracerProvider instead of a second pipeline.
// Langfuse 回调装配：Eino span 通过进程级 TracerProvider 导出，而不是另建管线。
package eino

import (
	"context"
	"errors"
	"fmt"
	"time"

	langfusecb "github.com/cloudwego/eino-ext/callbacks/langfuse/v2"
	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/callbacks"
	"github.com/cloudwego/eino/components"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
	"go.opentelemetry.io/otel/attribute"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"

	"github.com/HappyLadySauceM/Nagi/internal/observability"
	"github.com/HappyLadySauceM/Nagi/pkg/version"
)

// LangfuseConfig configures the callback. BaseURL is the Langfuse host; the
// handler appends the OTLP path itself.
// LangfuseConfig 配置回调。BaseURL 是 Langfuse 主机地址；
// handler 自行拼接 OTLP 路径。
type LangfuseConfig struct {
	BaseURL       string
	PublicKey     string
	SecretKey     string
	Environment   string
	Timeout       time.Duration
	RedactContent bool
}

// redactedValue is the placeholder written instead of redacted content.
// redactedValue 是替代被隐藏正文的占位值。
const redactedValue = `{"redacted":true}`

// NewLangfuseHandler builds the callback on top of the shared provider. It
// returns nil when telemetry is unavailable so the reply path keeps working.
// NewLangfuseHandler 在共享 Provider 上构建回调。遥测不可用时返回 nil，
// 回复链路照常工作。
func NewLangfuseHandler(ctx context.Context, provider *sdktrace.TracerProvider, cfg LangfuseConfig) (*LangfuseHandler, error) {
	if provider == nil {
		return nil, nil
	}
	if cfg.BaseURL == "" || cfg.PublicKey == "" || cfg.SecretKey == "" {
		return nil, nil
	}
	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	callbackCfg := &langfusecb.Config{
		Host:        cfg.BaseURL,
		PublicKey:   cfg.PublicKey,
		SecretKey:   cfg.SecretKey,
		ServiceName: "nagi",
		Environment: cfg.Environment,
		Version:     version.Version,
		Timeout:     timeout,
		// Eino's own ReAct/Init/Lambda wrappers add no information for an
		// operator; collapsing them keeps agent -> model/tool readable.
		// Eino 自身的 ReAct/Init/Lambda 包装对运维没有信息量；
		// 收敛后整棵树保持 agent -> 模型/工具 可读。
		CollapseAgentInternalSpans: true,
		// The callback must never create its own exporter: two pipelines would
		// duplicate every model span.
		// 回调绝不自建导出器：两条管线会让每个模型 span 重复上报。
		TracerProvider: provider,
	}
	callbackCfg.MaskFunc = func(value string) string {
		if cfg.RedactContent {
			return redactedValue
		}
		return observability.RedactString(value)
	}
	handler, err := langfusecb.NewHandler(ctx, callbackCfg)
	if err != nil {
		return nil, fmt.Errorf("agent: build langfuse callback: %w", err)
	}
	return &LangfuseHandler{CallbackHandler: handler, redactContent: cfg.RedactContent}, nil
}

// privateCallbackContext preserves cancellation while hiding its potentially private cause.
// privateCallbackContext 保留取消行为，但隐藏可能含有私密数据的取消原因。
type privateCallbackContext struct{ context.Context }

func (c privateCallbackContext) Value(key any) any {
	return context.WithoutCancel(c.Context).Value(key)
}

type LangfuseHandler struct {
	*langfusecb.CallbackHandler
	redactContent bool
}

func (h *LangfuseHandler) OnStart(ctx context.Context, info *callbacks.RunInfo, input callbacks.CallbackInput) context.Context {
	ctx = h.CallbackHandler.OnStart(ctx, info, input)
	if h.redactContent && isChatGeneration(info) {
		if in := model.ConvCallbackInput(input); in != nil && in.Config != nil {
			setGenerationModel(trace.SpanFromContext(ctx), in.Config.Model)
		}
	}
	return ctx
}

func (h *LangfuseHandler) OnEnd(ctx context.Context, info *callbacks.RunInfo, output callbacks.CallbackOutput) context.Context {
	if isChatGeneration(info) {
		output = h.generationOutput(trace.SpanFromContext(ctx), output)
	}
	return h.CallbackHandler.OnEnd(ctx, info, output)
}

func (h *LangfuseHandler) OnEndWithStreamOutput(ctx context.Context, info *callbacks.RunInfo, output *schema.StreamReader[callbacks.CallbackOutput]) context.Context {
	if isChatGeneration(info) && output != nil {
		span := trace.SpanFromContext(ctx)
		output = schema.StreamReaderWithConvert(output, func(chunk callbacks.CallbackOutput) (callbacks.CallbackOutput, error) {
			return h.generationOutput(span, chunk), nil
		})
	}
	return h.CallbackHandler.OnEndWithStreamOutput(ctx, info, output)
}

func isChatGeneration(info *callbacks.RunInfo) bool {
	return info != nil && info.Component == components.ComponentOfChatModel
}

func setGenerationModel(span trace.Span, name string) {
	if name != "" {
		span.SetAttributes(attribute.String("langfuse.observation.model.name", observability.RedactString(name)))
	}
}

func (h *LangfuseHandler) generationOutput(span trace.Span, output callbacks.CallbackOutput) callbacks.CallbackOutput {
	out := model.ConvCallbackOutput(output)
	if out == nil {
		return output
	}
	var total int
	if out.TokenUsage != nil {
		total = out.TokenUsage.TotalTokens
	} else if out.Message != nil && out.Message.ResponseMeta != nil && out.Message.ResponseMeta.Usage != nil {
		total = out.Message.ResponseMeta.Usage.TotalTokens
	}
	if total > 0 {
		span.SetAttributes(attribute.Int("gen_ai.usage.total_tokens", total))
	}
	if !h.redactContent || out.Config == nil || out.Config.Model == "" {
		return output
	}
	config := *out.Config
	setGenerationModel(span, config.Model)
	config.Model = ""
	withRedactedContent := *out
	withRedactedContent.Config = &config
	return &withRedactedContent
}

type privateErrorHandler struct{ callbacks.Handler }

func (h privateErrorHandler) OnStart(ctx context.Context, info *callbacks.RunInfo, input callbacks.CallbackInput) context.Context {
	return h.Handler.OnStart(privateCallbackContext{ctx}, info, input)
}
func (h privateErrorHandler) OnError(ctx context.Context, info *callbacks.RunInfo, err error) context.Context {
	safe := errors.New("sensitive operation failed")
	if errors.Is(err, context.DeadlineExceeded) {
		safe = context.DeadlineExceeded
	} else if errors.Is(err, context.Canceled) {
		safe = context.Canceled
	}
	return h.Handler.OnError(privateCallbackContext{ctx}, info, safe)
}

func (h privateErrorHandler) Needed(ctx context.Context, info *callbacks.RunInfo, timing callbacks.CallbackTiming) bool {
	// Agent iterators can carry errors outside OnError; the application already records agent spans.
	// Agent 迭代器可在 OnError 之外携带错误；应用自身已有 Agent 追踪。
	if info != nil && (info.Component == adk.ComponentOfAgent || info.Component == adk.ComponentOfAgenticAgent) {
		return false
	}

	if checker, ok := h.Handler.(callbacks.TimingChecker); ok {
		return checker.Needed(ctx, info, timing)
	}
	return true
}
