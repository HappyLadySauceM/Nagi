package application

import (
	"context"
	"encoding/json"

	"github.com/HappyLadySauceM/QQBot/internal/onebot/domain"
)

// Transport abstracts the wire to a OneBot implementation.
// Transport 抽象与 OneBot 实现之间的传输端口，由 infrastructure 提供正向 WS 实现。
type Transport interface {
	// Send writes one action frame carrying the given echo id.
	// Send 写出一帧携带指定 echo 的动作请求。
	Send(ctx context.Context, action domain.Action, echo string) error

	// Connected reports whether a live connection exists.
	// Connected 报告当前是否存在可用连接。
	Connected() bool

	// Close tears down the transport.
	// Close 关闭传输。
	Close() error
}

// ResponseSink receives action responses matched by echo id.
// ResponseSink 接收按 echo 匹配的动作响应帧。
type ResponseSink interface {
	HandleResponse(echo string, payload json.RawMessage)
}

// EventSink receives parsed protocol events.
// EventSink 接收已解析的事件帧。
type EventSink interface {
	HandleEvent(event domain.Event)
}
