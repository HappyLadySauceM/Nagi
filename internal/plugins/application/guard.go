// Guard enforces server-side authorization, timeouts and size limits around
// every tool invocation; the model never decides permissions.
// Guard 在每次工具调用外层强制服务端授权、超时与大小限制；
// 权限从不由模型决定。
package application

import (
	"context"
	"errors"
	"fmt"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	"github.com/HappyLadySauceM/QQBot/internal/plugins/contracts"
)

// Tool execution statuses recorded on spans and returned to the model.
// 工具执行状态，写入 span 并回给模型。
const (
	StatusOK       = "ok"
	StatusDenied   = "denied"
	StatusTimeout  = "timeout"
	StatusTooLarge = "too_large"
	StatusError    = "error"
)

// Scope is the per-message security context of one agent run; it is injected
// by the reply pipeline and never enters the prompt.
// Scope 是单次 Agent 运行的消息级安全上下文；由回复流水线注入，
// 绝不进入提示词。
type Scope struct {
	BotID          int64
	ConversationID string
	Kind           string
	TargetID       int64
	SenderID       int64
	// Permissions is the granted permission set of this run.
	// Permissions 是本次运行已授予的权限集合。
	Permissions    map[string]bool
	Timeout        time.Duration
	MaxResultBytes int
}

type scopeKey struct{}

// WithScope attaches the security context to the run context.
// WithScope 把安全上下文附加到运行 ctx。
func WithScope(ctx context.Context, scope Scope) context.Context {
	return context.WithValue(ctx, scopeKey{}, scope)
}

// ScopeFrom reads the security context; a missing scope denies every tool.
// ScopeFrom 读取安全上下文；缺失时拒绝所有工具。
func ScopeFrom(ctx context.Context) (Scope, bool) {
	scope, ok := ctx.Value(scopeKey{}).(Scope)
	return scope, ok
}

// Guard wraps one tool invocation with authorization and resource limits.
// Failures are returned as text so the model can explain them; they never
// become a larger permission or a hard agent crash.
// Guard 为一次工具调用包上授权与资源限制。失败以文本返回，
// 模型可以解释原因，既不会扩大权限，也不会让 Agent 直接崩溃。
func Guard(scope Scope, descriptor contracts.ToolDescriptor, invoke contracts.InvokeFunc) contracts.InvokeFunc {
	return func(ctx context.Context, argumentsInJSON string) (string, error) {
		ctx, span := tracer().Start(ctx, "qqbot.tool")
		defer span.End()
		span.SetAttributes(attribute.String("qqbot.tool_id", descriptor.ID))

		if !descriptor.ReadOnly {
			return finish(span, StatusDenied, 0, "error: tool is not available for this conversation")
		}
		if descriptor.Permission != "" && !scope.Permissions[descriptor.Permission] {
			return finish(span, StatusDenied, 0, "error: permission denied for "+descriptor.ID)
		}
		timeout := scope.Timeout
		if timeout <= 0 {
			timeout = 5 * time.Second
		}
		runCtx, cancel := context.WithTimeout(ctx, timeout)
		defer cancel()
		// Tools read the same scope to bind bot/target; it stays out of prompts.
		// 工具读取同一份作用域来绑定账号与目标；它不会进入提示词。
		runCtx = WithScope(runCtx, scope)

		started := time.Now()
		result, err := invoke(runCtx, argumentsInJSON)
		span.SetAttributes(attribute.Int64("qqbot.tool_duration_ms", time.Since(started).Milliseconds()))
		if err != nil {
			status := StatusError
			if errors.Is(err, context.DeadlineExceeded) {
				status = StatusTimeout
			}
			span.RecordError(err)
			return finish(span, status, 0, fmt.Sprintf("error: tool %s failed", descriptor.ID))
		}
		if scope.MaxResultBytes > 0 && len(result) > scope.MaxResultBytes {
			return finish(span, StatusTooLarge, len(result), "error: tool result too large")
		}
		return finish(span, StatusOK, len(result), result)
	}
}

// tracer returns the shared no-op-safe tracer of this package.
// tracer 返回本包共享的、无 SDK 时为 no-op 的 tracer。
func tracer() trace.Tracer {
	return otel.Tracer("github.com/HappyLadySauceM/QQBot/internal/plugins")
}

// finish records the terminal status and returns the text shown to the model.
// finish 记录终态并返回给模型看的文本。
func finish(span trace.Span, status string, bytes int, text string) (string, error) {
	span.SetAttributes(
		attribute.String("qqbot.tool_status", status),
		attribute.Int("qqbot.tool_bytes", bytes),
	)
	return text, nil
}
