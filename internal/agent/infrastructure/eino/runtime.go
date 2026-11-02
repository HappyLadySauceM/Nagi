// Eino ADK runtime: one ChatModelAgent per run, driven by the ReplyExecutor.
// Eino ADK 运行时：每次回复构建一个 ChatModelAgent，由 ReplyExecutor 驱动。
package eino

import (
	"context"
	"errors"
	"fmt"
	mediacontracts "github.com/HappyLadySauceM/Nagi/internal/media/contracts"
	"github.com/HappyLadySauceM/Nagi/pkg/modelhttp"
	"io"
	"net/http"
	"sync"
	"time"

	"github.com/cloudwego/eino-ext/components/model/openai"
	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/callbacks"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/components/tool"
	einoschema "github.com/cloudwego/eino/schema"

	"github.com/HappyLadySauceM/Nagi/internal/agent/contracts"
	"github.com/HappyLadySauceM/Nagi/internal/observability"
)

// defaultMaxSteps bounds the ReAct loop when the caller does not specify one.
// defaultMaxSteps 在调用方未指定时限制 ReAct 循环轮数。
const defaultMaxSteps = 6

// agentName labels this agent in Eino spans and logs.
// agentName 在 Eino span 与日志中标识该 Agent。
const agentName = "nagi_replier"

// Config configures the runtime. BaseURL points at any OpenAI-compatible
// endpoint; Timeout bounds one HTTP call and MaxOutputTokens one reply.
// Config 配置运行时。BaseURL 指向任意 OpenAI 兼容端点；
// Timeout 限制单次 HTTP 调用，MaxOutputTokens 限制单条回复。
type Config struct {
	HTTPHeaders           map[string]string
	ProviderDefaultOutput bool
	Thinking              bool
	ReasoningEffort       string
	ContextWindow         int
	BaseURL               string
	APIKey                string
	Model                 string
	MaxOutputTokens       int
	// Timeout bounds one whole run; HTTPTimeout bounds a single model call.
	// Timeout 限制整次运行；HTTPTimeout 限制单次模型调用。
	Timeout     time.Duration
	HTTPTimeout time.Duration
	// MaxSteps is the default ReAct bound used when a request does not set one.
	// MaxSteps 是请求未指定时使用的默认 ReAct 轮数上限。
	MaxSteps int
	// Tools resolves the immutable tool snapshot of one run; nil disables tools.
	// Tools 解析单次运行的不可变工具快照；nil 表示不启用工具。
	Tools func(ctx context.Context, req contracts.Request) []tool.BaseTool
	// Handlers are optional observability callbacks; the Langfuse handler is
	// registered per run so it never becomes a second global export pipeline.
	// Handlers 是可选的观测回调；Langfuse handler 按 Run 注册，
	// 避免形成第二条全局导出管线。
	Handlers          []callbacks.Handler
	SensitiveHandlers []callbacks.Handler
}

// maxCachedModels bounds the per-token-budget model cache. A request may set
// its own output budget, and building a model is cheap but not free.
// maxCachedModels 限制按输出预算缓存的模型数量。请求可以指定自己的输出预算，
// 构建模型代价不高但也不为零。
const maxCachedModels = 4

// Runtime implements contracts.Runtime on top of Eino ADK.
// Runtime 基于 Eino ADK 实现 contracts.Runtime。
type Runtime struct {
	// model is the default chat model built from Config.MaxOutputTokens.
	// model 是按 Config.MaxOutputTokens 构建的默认聊天模型。
	model model.BaseModel[*einoschema.Message]
	// builder creates a model for another output budget; nil when a test
	// injected the model directly, in which case the default model is reused.
	// builder 按其他输出预算创建模型；测试直接注入模型时为 nil，
	// 此时始终复用默认模型。
	builder func(ctx context.Context, maxTokens int) (model.BaseModel[*einoschema.Message], error)
	cfg     Config

	mu     sync.RWMutex
	models map[int]model.BaseModel[*einoschema.Message]
}

// newRuntimeWithModel wires a runtime around an existing model; it exists so
// tests can exercise the event loop without an HTTP endpoint.
// newRuntimeWithModel 用已有模型装配运行时；用于测试在不访问 HTTP 端点的情况下
// 验证事件循环。
func newRuntimeWithModel(cfg Config, chatModel model.BaseModel[*einoschema.Message]) *Runtime {
	return &Runtime{model: chatModel, cfg: cfg}
}

// NewRuntime validates the configuration and builds the chat model once; the
// model is stateless per request and safe to reuse.
// NewRuntime 校验配置并只构建一次聊天模型；模型按请求无状态，可安全复用。
func NewRuntime(ctx context.Context, cfg Config) (*Runtime, error) {
	if cfg.BaseURL == "" || cfg.Model == "" {
		return nil, fmt.Errorf("agent: eino base-url and model are required")
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = 30 * time.Second
	}
	if cfg.HTTPTimeout <= 0 {
		cfg.HTTPTimeout = 25 * time.Second
	}
	if cfg.MaxSteps <= 0 {
		cfg.MaxSteps = defaultMaxSteps
	}
	if cfg.MaxOutputTokens <= 0 && !cfg.ProviderDefaultOutput {
		cfg.MaxOutputTokens = 400
	}
	builder := func(ctx context.Context, maxTokens int) (model.BaseModel[*einoschema.Message], error) {
		effort := cfg.ReasoningEffort
		if !cfg.Thinking {
			effort = "none"
		} else if effort == "" {
			effort = "medium"
		}
		tokenLimit := &maxTokens
		if cfg.ProviderDefaultOutput && maxTokens <= 0 {
			tokenLimit = nil
		}
		return openai.NewChatModel(ctx, &openai.ChatModelConfig{
			ReasoningEffort: openai.ReasoningEffortLevel(effort),
			HTTPClient:      modelhttp.Client(&http.Client{Timeout: cfg.HTTPTimeout}, cfg.HTTPHeaders),
			BaseURL:         cfg.BaseURL,
			APIKey:          cfg.APIKey,
			Model:           cfg.Model,
			Timeout:         cfg.HTTPTimeout,
			MaxTokens:       tokenLimit,
		})
	}
	chatModel, err := builder(ctx, cfg.MaxOutputTokens)
	if err != nil {
		return nil, fmt.Errorf("agent: build eino chat model: %w", err)
	}
	return &Runtime{
		model:   chatModel,
		builder: builder,
		cfg:     cfg,
		models:  make(map[int]model.BaseModel[*einoschema.Message]),
	}, nil
}

// modelFor resolves the chat model of one run. A zero or default budget reuses
// the shared model; other budgets are built once and cached.
// modelFor 解析单次运行的聊天模型。零值或默认预算复用共享模型；
// 其他预算只构建一次并缓存。
func (r *Runtime) modelFor(ctx context.Context, maxTokens int) (model.BaseModel[*einoschema.Message], error) {
	if maxTokens <= 0 || maxTokens == r.cfg.MaxOutputTokens || r.builder == nil {
		return r.model, nil
	}
	r.mu.RLock()
	cached, ok := r.models[maxTokens]
	r.mu.RUnlock()
	if ok {
		return cached, nil
	}
	built, err := r.builder(ctx, maxTokens)
	if err != nil {
		return nil, fmt.Errorf("agent: build eino chat model for %d tokens: %w", maxTokens, err)
	}
	r.mu.Lock()
	if len(r.models) < maxCachedModels {
		r.models[maxTokens] = built
	}
	r.mu.Unlock()
	return built, nil
}

// Run executes one reply. The event iterator is always drained to its terminal
// state: stopping early would leave Eino's model/tool spans incomplete.
// Run 执行一次回复。事件迭代器必须消费到终态：提前停止会让 Eino 的
// 模型/工具 span 不完整。
func (r *Runtime) Run(ctx context.Context, req contracts.Request) (contracts.Result, error) {
	if len(req.Messages) == 0 {
		return contracts.Result{}, fmt.Errorf("agent: eino run requires messages")
	}
	timeout := req.Timeout
	if timeout <= 0 {
		timeout = r.cfg.Timeout
	}
	ctx = observability.WithContentState(ctx)
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	steps := req.MaxSteps
	if steps <= 0 {
		steps = r.cfg.MaxSteps
	}
	hasImages := false
	for i := range req.Messages {
		parts := append([]mediacontracts.Part(nil), req.Messages[i].Parts...)
		for j := range parts {
			if parts[j].Base64 != "" {
				hasImages = true
				if !req.ModelImages {
					parts[j].Base64 = ""
					parts[j].Text = "[当前模型不支持图片输入]"
				}
			}
		}
		req.Messages[i].Parts = parts
	}
	toolsConfig := adk.ToolsConfig{}
	toolsConfig.ExecuteSequentially = true
	exposed := 0
	credentialTools := false
	if r.cfg.Tools != nil && (req.ModelTools || !req.CapabilitiesKnown) {
		if snapshot := r.cfg.Tools(runCtx, req); len(snapshot) > 0 {
			toolsConfig.ToolsNodeConfig.Tools = snapshot
			exposed = len(snapshot)
			for _, t := range snapshot {
				if v, ok := t.(interface{ Sensitive() bool }); ok && v.Sensitive() {
					credentialTools = true
				}
			}
		}
	}
	chatModel, err := r.modelFor(runCtx, req.MaxOutputTokens)
	if err != nil {
		return contracts.Result{}, err
	}
	agent, err := adk.NewChatModelAgent(runCtx, &adk.ChatModelAgentConfig{
		Name:          agentName,
		Model:         chatModel,
		ToolsConfig:   toolsConfig,
		MaxIterations: steps,
		Middlewares: []adk.AgentMiddleware{{BeforeChatModel: func(ctx context.Context, state *adk.ChatModelAgentState) error {
			if err := normalizeToolImages(ctx, state); err != nil {
				return err
			}
			if r.cfg.ContextWindow > 0 {
				used := 0
				for _, m := range state.Messages {
					used += len(m.Content)
					for _, part := range m.UserInputMultiContent {
						used += len(part.Text)
						if part.Image != nil {
							used += 4096
						}
					}
				}
				if used > r.cfg.ContextWindow {
					return fmt.Errorf("模型上下文预算不足，请增加最大上下文或减少历史内容")
				}
			}
			return nil
		}}},
	})
	if err != nil {
		return contracts.Result{}, fmt.Errorf("agent: build eino agent: %w", err)
	}
	runner := adk.NewRunner(runCtx, adk.RunnerConfig{Agent: agent})

	opts := make([]adk.AgentRunOption, 0, 1)
	// Sensitive runs keep metrics while their model and tool contents stay redacted.
	// 敏感运行继续记录指标，但隐藏模型及工具内容。
	handlers := r.cfg.Handlers
	if credentialTools || hasImages {
		handlers = make([]callbacks.Handler, len(r.cfg.SensitiveHandlers))
		for i, h := range r.cfg.SensitiveHandlers {
			handlers[i] = privateErrorHandler{h}
		}
	}
	if len(handlers) > 0 {
		opts = append(opts, adk.WithCallbacks(handlers...))
	}
	iterator := runner.Run(runCtx, toSchemaMessages(req.Messages), opts...)

	result := contracts.Result{Model: r.cfg.Model, ToolsExposed: exposed}
	answered := false
	for {
		event, ok := iterator.Next()
		if !ok {
			break
		}
		if event.Err != nil {
			return contracts.Result{}, privateRuntimeError(event.Err)
		}
		if event.Output == nil || event.Output.MessageOutput == nil {
			continue
		}
		message, outErr := materialize(event.Output.MessageOutput)
		if outErr != nil {
			return contracts.Result{}, privateRuntimeError(outErr)
		}
		if message == nil || message.Role != einoschema.Assistant {
			continue
		}
		answered = true
		// Tool-calling turns carry no text; keep the last real answer instead of
		// blanking it.
		// 工具调用轮次没有正文；保留最后一段真实回复而不是清空。
		if message.Content != "" {
			result.Text = message.Content
		}
		if usage := message.ResponseMeta; usage != nil && usage.Usage != nil {
			result.InputUsage += int64(usage.Usage.PromptTokens)
			result.OutputUsage += int64(usage.Usage.CompletionTokens)
		}
	}
	if !answered {
		return contracts.Result{}, fmt.Errorf("agent: eino run produced no assistant message")
	}
	return result, nil
}

// materialize returns the complete message for one event, draining the stream
// when streaming is enabled.
// materialize 返回事件对应的完整消息；开启流式时把分片读完。
func materialize(variant *adk.TypedMessageVariant[*einoschema.Message]) (*einoschema.Message, error) {
	if variant.IsStreaming {
		if variant.MessageStream == nil {
			return nil, nil
		}
		return drainStream(variant.MessageStream)
	}
	return variant.Message, nil
}

// drainStream concatenates one message stream into a single message.
// drainStream 把一个消息流拼成单条消息。
func drainStream(stream *einoschema.StreamReader[*einoschema.Message]) (*einoschema.Message, error) {
	defer stream.Close()
	var merged *einoschema.Message
	for {
		chunk, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("agent: read eino stream: %w", err)
		}
		if chunk == nil {
			continue
		}
		if merged == nil {
			copied := *chunk
			merged = &copied
			continue
		}
		merged.Content += chunk.Content
		merged.ToolCalls = append(merged.ToolCalls, chunk.ToolCalls...)
		if chunk.ResponseMeta != nil {
			merged.ResponseMeta = chunk.ResponseMeta
		}
	}
	return merged, nil
}

// toSchemaMessages converts business messages into Eino schema messages.
// toSchemaMessages 把业务消息转换为 Eino 的 schema 消息。
func toSchemaMessages(messages []contracts.Message) []*einoschema.Message {
	out := make([]*einoschema.Message, 0, len(messages))
	for _, message := range messages {
		role := einoschema.RoleType(message.Role)
		switch role {
		case einoschema.System, einoschema.User, einoschema.Assistant, einoschema.Tool:
		default:
			role = einoschema.User
		}
		converted := &einoschema.Message{Role: role, Content: message.Content}
		if len(message.Parts) > 0 {
			converted.Content = ""
			for _, p := range message.Parts {
				if p.Base64 != "" {
					data := p.Base64
					converted.UserInputMultiContent = append(converted.UserInputMultiContent, einoschema.MessageInputPart{Type: einoschema.ChatMessagePartTypeImageURL, Image: &einoschema.MessageInputImage{MessagePartCommon: einoschema.MessagePartCommon{MIMEType: p.MIME, Base64Data: &data}}})
				} else {
					converted.UserInputMultiContent = append(converted.UserInputMultiContent, einoschema.MessageInputPart{Type: einoschema.ChatMessagePartTypeText, Text: p.Text})
				}
			}
		}
		out = append(out, converted)
	}
	return out
}

// normalizeToolImages keeps tool receipts textual and appends images after the complete tool batch.
// normalizeToolImages 保持工具回执为文本，并在完整工具批次之后追加图片。
func normalizeToolImages(ctx context.Context, state *adk.ChatModelAgentState) error {
	var out []*einoschema.Message
	var images []einoschema.MessageInputPart
	flush := func() {
		if len(images) > 0 {
			out = append(out, &einoschema.Message{Role: einoschema.User, UserInputMultiContent: images})
			images = nil
		}
	}
	for _, m := range state.Messages {
		if m.Role != einoschema.Tool {
			flush()
			out = append(out, m)
			continue
		}
		if len(m.UserInputMultiContent) == 0 {
			out = append(out, m)
			continue
		}
		copy := *m
		copy.UserInputMultiContent = nil
		for _, p := range m.UserInputMultiContent {
			if p.Type == einoschema.ChatMessagePartTypeImageURL {
				images = append(images, p)
			} else if p.Type == einoschema.ChatMessagePartTypeText {
				copy.Content += p.Text + "\n"
			}
		}
		out = append(out, &copy)
	}
	flush()
	state.Messages = out
	return ctx.Err()
}

// privateRuntimeError keeps upstream response bodies out of application spans and logs.
// privateRuntimeError 防止上游响应正文进入应用追踪和日志。
func privateRuntimeError(err error) error {
	if errors.Is(err, context.Canceled) {
		return context.Canceled
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return context.DeadlineExceeded
	}
	return errors.New("agent: model execution failed")
}
