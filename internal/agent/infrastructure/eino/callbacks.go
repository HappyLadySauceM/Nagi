// Langfuse callback wiring: Eino spans export through the process-wide
// TracerProvider instead of a second pipeline.
// Langfuse 回调装配：Eino span 通过进程级 TracerProvider 导出，而不是另建管线。
package eino

import (
	"context"
	"fmt"
	"time"

	langfusecb "github.com/cloudwego/eino-ext/callbacks/langfuse/v2"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"

	"github.com/HappyLadySauceM/QQBot/pkg/version"
)

// LangfuseConfig configures the callback. BaseURL is the Langfuse host; the
// handler appends the OTLP path itself.
// LangfuseConfig 配置回调。BaseURL 是 Langfuse 主机地址；
// handler 自行拼接 OTLP 路径。
type LangfuseConfig struct {
	BaseURL     string
	PublicKey   string
	SecretKey   string
	Environment string
	Timeout     time.Duration
	// RedactContent replaces every exported string value with a marker; used
	// when raw content export is disabled so spans keep their shape and metrics.
	// RedactContent 把每个导出的字符串值替换为标记；正文导出关闭时使用，
	// 使 span 仍保留结构与指标。
	RedactContent bool
}

// redactedValue is the placeholder written instead of redacted content.
// redactedValue 是替代被隐藏正文的占位值。
const redactedValue = `{"redacted":true}`

// NewLangfuseHandler builds the callback on top of the shared provider. It
// returns nil when telemetry is unavailable so the reply path keeps working.
// NewLangfuseHandler 在共享 Provider 上构建回调。遥测不可用时返回 nil，
// 回复链路照常工作。
func NewLangfuseHandler(ctx context.Context, provider *sdktrace.TracerProvider, cfg LangfuseConfig) (*langfusecb.CallbackHandler, error) {
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
		ServiceName: "qqbot",
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
	if cfg.RedactContent {
		callbackCfg.MaskFunc = func(string) string { return redactedValue }
	}
	handler, err := langfusecb.NewHandler(ctx, callbackCfg)
	if err != nil {
		return nil, fmt.Errorf("agent: build langfuse callback: %w", err)
	}
	return handler, nil
}
