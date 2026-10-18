// Package application dispatches OneBot events to registered handlers.
// Package application 将 OneBot 事件按注册表分发到处理器，并对单个处理器提供 panic 隔离。
package application

import (
	"context"
	"fmt"
	"log/slog"
	"runtime/debug"
	"sync"
	"sync/atomic"

	botcontracts "github.com/HappyLadySauceM/QQBot/internal/bot/contracts"
	botdomain "github.com/HappyLadySauceM/QQBot/internal/bot/domain"
	"github.com/HappyLadySauceM/QQBot/internal/onebot/domain"
)

// Dispatcher consumes events and routes them to handlers.
// Dispatcher 消费事件流并将其路由到已注册的处理器。
type Dispatcher struct {
	src    EventSource
	logger *slog.Logger

	mu       sync.RWMutex
	handlers []botcontracts.Handler

	handled atomic.Uint64
}

// NewDispatcher binds the dispatcher to an event source.
// NewDispatcher 将分发器绑定到事件源。
func NewDispatcher(src EventSource, logger *slog.Logger) *Dispatcher {
	if logger == nil {
		logger = slog.Default()
	}
	return &Dispatcher{src: src, logger: logger}
}

// Register adds a handler; nil handlers and duplicate names are rejected.
// Register 注册处理器；空处理器与重名会被拒绝。
func (d *Dispatcher) Register(h botcontracts.Handler) error {
	if h == nil || h.Name() == "" {
		return fmt.Errorf("bot: handler required")
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	for _, existing := range d.handlers {
		if existing.Name() == h.Name() {
			return fmt.Errorf("bot: handler %q already registered", h.Name())
		}
	}
	d.handlers = append(d.handlers, h)
	return nil
}

// Names snapshots registered handler names.
// Names 返回已注册处理器名的快照。
func (d *Dispatcher) Names() []string {
	d.mu.RLock()
	defer d.mu.RUnlock()
	names := make([]string, 0, len(d.handlers))
	for _, h := range d.handlers {
		names = append(names, h.Name())
	}
	return names
}

// HandledCount reports how many handler executions succeeded.
// HandledCount 报告成功执行的处理器次数。
func (d *Dispatcher) HandledCount() uint64 { return d.handled.Load() }

// Run consumes the event stream until it closes or ctx is canceled.
// Run 消费事件流，直到通道关闭或 ctx 取消。
func (d *Dispatcher) Run(ctx context.Context) error {
	events := d.src.Events()
	for {
		select {
		case <-ctx.Done():
			return nil
		case event, ok := <-events:
			if !ok {
				d.logger.Info("bot: event channel closed, dispatcher exiting")
				return nil
			}
			d.dispatch(ctx, event)
		}
	}
}

// dispatch matches handlers via the pure policy and invokes each in isolation.
// dispatch 经纯策略匹配处理器，并逐个隔离调用。
func (d *Dispatcher) dispatch(ctx context.Context, event domain.Event) {
	d.mu.RLock()
	handlers := make([]botcontracts.Handler, len(d.handlers))
	copy(handlers, d.handlers)
	d.mu.RUnlock()

	descriptors := make([]botdomain.Descriptor, len(handlers))
	for i, h := range handlers {
		descriptors[i] = botdomain.Descriptor{Name: h.Name(), Match: h.Match(event)}
	}
	if len(botdomain.Select(descriptors)) == 0 {
		d.logger.Debug("bot: no handler for event",
			"post_type", string(event.Post),
			"message_type", string(event.Type))
		return
	}
	for i, h := range handlers {
		if !descriptors[i].Match {
			continue
		}
		d.handleOne(ctx, h, event)
	}
}

// handleOne runs one handler with panic recovery; failures never kill the loop.
// handleOne 以 panic 恢复运行单个处理器；失败不会终止分发循环。
func (d *Dispatcher) handleOne(ctx context.Context, h botcontracts.Handler, event domain.Event) {
	defer func() {
		if rec := recover(); rec != nil {
			d.logger.Error("bot: handler panic recovered",
				"handler", h.Name(),
				"panic", fmt.Sprint(rec),
				"stack", string(debug.Stack()))
		}
	}()
	if err := h.Handle(ctx, event); err != nil {
		d.logger.Error("bot: handler failed",
			"handler", h.Name(),
			"post_type", string(event.Post),
			"message_id", int64(event.MessageID),
			"error", err)
		return
	}
	d.handled.Add(1)
}
