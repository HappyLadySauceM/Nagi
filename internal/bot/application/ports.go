package application

import (
	"github.com/HappyLadySauceM/Nagi/internal/onebot/domain"
)

// EventSource provides the OneBot event stream for dispatch.
// EventSource 为分发器提供 OneBot 事件流。
type EventSource interface {
	Events() <-chan domain.Event
	Connected() bool
}

// SelfIDReader reports the account bound to one connection. It is the fallback
// when an event carries no self_id (for example lifecycle frames).
// SelfIDReader 报告一条连接绑定的账号；当事件没有 self_id（如生命周期帧）时作为兜底。
type SelfIDReader interface {
	SelfID() (domain.BotID, bool)
}
