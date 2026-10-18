package application

import (
	"github.com/HappyLadySauceM/QQBot/internal/onebot/domain"
)

// EventSource provides the OneBot event stream for dispatch.
// EventSource 为分发器提供 OneBot 事件流。
type EventSource interface {
	Events() <-chan domain.Event
	Connected() bool
}
