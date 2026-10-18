// Package contracts publishes the handler contract for business contexts.
// Package contracts 发布 bot 上下文对业务上下文的稳定处理器契约。
package contracts

import (
	"context"

	"github.com/HappyLadySauceM/QQBot/internal/onebot/domain"
)

// Handler is implemented by business contexts to receive OneBot events.
// Handler 由业务上下文实现，用于接收 OneBot 事件。
type Handler interface {
	// Name identifies the handler in logs and status output.
	// Name 在日志与状态输出中标识处理器。
	Name() string

	// Match reports whether this handler wants the event.
	// Match 报告该处理器是否处理此事件。
	Match(event domain.Event) bool

	// Handle processes the event; it must not block for long.
	// Handle 处理事件；不应长时间阻塞。
	Handle(ctx context.Context, event domain.Event) error
}
