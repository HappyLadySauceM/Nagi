// Package contracts publishes the stable interfaces of the onebot context.
// Package contracts 发布 onebot 上下文对外协作的稳定接口。
// onebot/domain 是 OneBot v11 协议的“已发布语言”，业务上下文经本包引用协议值类型属于允许的例外。
package contracts

import (
	"context"

	"github.com/HappyLadySauceM/QQBot/internal/onebot/domain"
)

// Sender is the stable action-sending interface for business contexts.
// Sender 是业务上下文发送 OneBot 动作的稳定接口。
type Sender interface {
	SendPrivateMessage(ctx context.Context, to domain.UserID, segments ...domain.Segment) error
	SendGroupMessage(ctx context.Context, to domain.GroupID, segments ...domain.Segment) error
	DeleteMessage(ctx context.Context, id domain.MessageID) error
}

// EventSource is the stable event-reading interface for business contexts.
// EventSource 是业务上下文读取 OneBot 事件的稳定接口。
type EventSource interface {
	Events() <-chan domain.Event
	Connected() bool
}

// BotStatus is a read-only snapshot of the connection state.
// BotStatus 是连接状态的只读快照。
type BotStatus struct {
	Connected   bool
	SelfID      domain.BotID
	SelfIDKnown bool
}

// StatusReader queries the current connection snapshot.
// StatusReader 查询当前连接状态快照。
type StatusReader interface {
	Status() BotStatus
}
