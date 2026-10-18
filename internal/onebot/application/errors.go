package application

import (
	"errors"
	"fmt"
)

// Sentinels for callers using errors.Is.
// 供调用方通过 errors.Is 判断的哨兵错误。
var (
	// ErrNotConnected means the transport has no live connection.
	// ErrNotConnected 表示当前没有可用连接。
	ErrNotConnected = errors.New("onebot: connection not ready")

	// ErrTimeout means no echo-matched response arrived before the deadline.
	// ErrTimeout 表示在期限内未收到 echo 匹配的响应。
	ErrTimeout = errors.New("onebot: action call timed out")

	// ErrClosed means the client service has been closed.
	// ErrClosed 表示客户端服务已关闭。
	ErrClosed = errors.New("onebot: client service closed")
)

// APIError represents a OneBot action response with a failing retcode.
// APIError 表示 retcode 失败的 OneBot 动作响应。
type APIError struct {
	Status  string
	Retcode int64
	Wording string
}

// Error implements the error interface; it never exposes payload details.
// Error 实现错误接口；只描述结果状态，不泄露载荷内容。
func (e *APIError) Error() string {
	return fmt.Sprintf("onebot: action failed retcode=%d status=%s wording=%q", e.Retcode, e.Status, e.Wording)
}
