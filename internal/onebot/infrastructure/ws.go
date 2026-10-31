// Package infrastructure implements the forward WebSocket transport port.
// Package infrastructure 实现正向 WebSocket 传输端口：拨号、token 鉴权、断线指数退避重连与读写分离。
package infrastructure

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math/rand"
	"net"
	"net/http"
	"net/url"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"

	obapp "github.com/HappyLadySauceM/Nagi/internal/onebot/application"
	obdomain "github.com/HappyLadySauceM/Nagi/internal/onebot/domain"
)

const (
	// defaultHandshakeTimeout bounds a single dial attempt.
	// defaultHandshakeTimeout 限定单次拨号时限。
	defaultHandshakeTimeout = 10 * time.Second
	// defaultWriteTimeout bounds a single frame write.
	// defaultWriteTimeout 限定单帧写出时限。
	defaultWriteTimeout = 10 * time.Second
	// readLimit caps one inbound frame at 8MiB.
	// readLimit 将入站帧上限设为 8MiB。
	readLimit = 8 << 20
)

// Options configures the forward WebSocket transport.
// Options 配置正向 WebSocket 传输。
type Options struct {
	URL              string
	AccessToken      string
	HandshakeTimeout time.Duration
	ReconnectInitial time.Duration
	ReconnectMax     time.Duration
	WriteTimeout     time.Duration
	// HeartbeatTimeout closes the connection when no inbound frame arrives
	// within the window; <= 0 disables the watchdog.
	// HeartbeatTimeout 在窗口内没有入站帧时断开连接；<= 0 关闭看门狗。
	HeartbeatTimeout time.Duration
}

// Transport implements application.Transport over gorilla/websocket.
// Transport 基于 gorilla/websocket 实现 application.Transport 端口。
type Transport struct {
	opts      Options
	responses obapp.ResponseSink
	events    obapp.EventSink
	logger    *slog.Logger

	mu        sync.Mutex
	conn      *websocket.Conn
	connected atomic.Bool
	closed    atomic.Bool
}

// NewTransport creates a disconnected transport; call Attach once before Run.
// NewTransport 创建未连接的传输；须在 Run 之前调用一次 Attach。
func NewTransport(opts Options, logger *slog.Logger) *Transport {
	if logger == nil {
		logger = slog.Default()
	}
	if opts.HandshakeTimeout <= 0 {
		opts.HandshakeTimeout = defaultHandshakeTimeout
	}
	if opts.ReconnectInitial <= 0 {
		opts.ReconnectInitial = time.Second
	}
	if opts.ReconnectMax <= 0 {
		opts.ReconnectMax = 30 * time.Second
	}
	if opts.WriteTimeout <= 0 {
		opts.WriteTimeout = defaultWriteTimeout
	}
	return &Transport{opts: opts, logger: logger}
}

// Attach binds the frame receivers before Run starts any goroutine.
// Attach 在 Run 启动任何 goroutine 之前绑定帧接收者。
func (t *Transport) Attach(responses obapp.ResponseSink, events obapp.EventSink) {
	t.responses = responses
	t.events = events
}

// Run keeps the connection loop alive until ctx is canceled or Close is called.
// Run 维持连接循环：拨号、读泵、断线指数退避重连，直到 ctx 取消或调用 Close。
func (t *Transport) Run(ctx context.Context) {
	backoff := t.opts.ReconnectInitial
	for {
		if ctx.Err() != nil || t.closed.Load() {
			t.disconnect()
			return
		}
		conn, resp, err := t.dial(ctx)
		if err != nil {
			if resp != nil {
				_ = resp.Body.Close()
			}
			delay := jitter(backoff)
			t.logger.Warn("onebot: connect failed, retrying", "error", err, "retry_in", delay.String())
			if !sleep(ctx, delay) {
				t.disconnect()
				return
			}
			backoff = min(backoff*2, t.opts.ReconnectMax)
			continue
		}
		backoff = t.opts.ReconnectInitial
		t.setConn(conn)
		t.logger.Info("onebot: connected", "endpoint", endpointOf(t.opts.URL))
		err = t.readPump(ctx, conn)
		t.disconnect()
		if ctx.Err() != nil || t.closed.Load() {
			return
		}
		delay := jitter(backoff)
		t.logger.Warn("onebot: connection lost", "error", err, "retry_in", delay.String())
		if !sleep(ctx, delay) {
			return
		}
		backoff = min(backoff*2, t.opts.ReconnectMax)
	}
}

// Send implements application.Transport.
// Send 实现 application.Transport：在写超时与互斥保护下写出一帧。
func (t *Transport) Send(ctx context.Context, action obdomain.Action, echo string) error {
	frame, err := encodeFrame(action, echo)
	if err != nil {
		return err
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.conn == nil {
		return obapp.ErrNotConnected
	}
	deadline := time.Now().Add(t.opts.WriteTimeout)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	_ = t.conn.SetWriteDeadline(deadline)
	return t.conn.WriteJSON(frame)
}

// Connected implements application.Transport.
// Connected 实现 application.Transport。
func (t *Transport) Connected() bool { return t.connected.Load() }

// Close marks the transport closed and drops the current connection.
// Close 标记传输关闭并断开当前连接；Run 将随后退出。
func (t *Transport) Close() error {
	t.closed.Store(true)
	t.mu.Lock()
	conn := t.conn
	t.conn = nil
	t.mu.Unlock()
	t.connected.Store(false)
	if conn == nil {
		return nil
	}
	return conn.Close()
}

func (t *Transport) dial(ctx context.Context) (*websocket.Conn, *http.Response, error) {
	dialer := &websocket.Dialer{
		HandshakeTimeout: t.opts.HandshakeTimeout,
		Proxy:            http.ProxyFromEnvironment,
	}
	header := http.Header{}
	if t.opts.AccessToken != "" {
		header.Set("Authorization", "Bearer "+t.opts.AccessToken)
	}
	return dialer.DialContext(ctx, t.opts.URL, header)
}

func (t *Transport) readPump(ctx context.Context, conn *websocket.Conn) error {
	conn.SetReadLimit(readLimit)
	applyReadDeadline := func() {
		if t.opts.HeartbeatTimeout > 0 {
			_ = conn.SetReadDeadline(time.Now().Add(t.opts.HeartbeatTimeout))
		}
	}
	applyReadDeadline()
	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		_, data, err := conn.ReadMessage()
		if err != nil {
			if isTimeoutError(err) && t.opts.HeartbeatTimeout > 0 {
				return fmt.Errorf("onebot: heartbeat timeout after %s: %w", t.opts.HeartbeatTimeout, err)
			}
			return err
		}
		applyReadDeadline()
		t.routeFrame(data)
	}
}

// isTimeoutError reports whether err is a network deadline timeout.
// isTimeoutError 判断 err 是否为网络期限超时。
func isTimeoutError(err error) bool {
	var netErr net.Error
	return errors.As(err, &netErr) && netErr.Timeout()
}

// routeFrame demultiplexes frames: echo-bearing responses and protocol events.
// routeFrame 解复用帧：携带 echo 的动作响应与协议事件。
func (t *Transport) routeFrame(data []byte) {
	var probe struct {
		Echo     string `json:"echo"`
		PostType string `json:"post_type"`
	}
	if err := json.Unmarshal(data, &probe); err != nil {
		t.logger.Debug("onebot: ignore unparsable frame", "error", err)
		return
	}
	switch {
	case probe.Echo != "":
		t.responses.HandleResponse(probe.Echo, json.RawMessage(data))
	case probe.PostType != "":
		event, err := mapEvent(data)
		if err != nil {
			t.logger.Warn("onebot: event parse failed", "error", err)
			return
		}
		t.events.HandleEvent(event)
	default:
		t.logger.Debug("onebot: ignore unknown frame")
	}
}

func (t *Transport) setConn(conn *websocket.Conn) {
	t.mu.Lock()
	t.conn = conn
	t.mu.Unlock()
	t.connected.Store(conn != nil)
}

func (t *Transport) disconnect() {
	t.mu.Lock()
	conn := t.conn
	t.conn = nil
	t.mu.Unlock()
	t.connected.Store(false)
	if conn != nil {
		_ = conn.Close()
	}
}

// encodeFrame converts a domain action into its wire representation.
// encodeFrame 将领域动作转换为线上表示，不修改原始 Params。
func encodeFrame(action obdomain.Action, echo string) (map[string]any, error) {
	if action.Name == "" {
		return nil, fmt.Errorf("onebot: action name required")
	}
	params := make(map[string]any, len(action.Params)+1)
	for k, v := range action.Params {
		if segments, ok := v.([]obdomain.Segment); ok {
			params[k] = encodeSegments(segments)
			continue
		}
		params[k] = v
	}
	frame := map[string]any{"action": string(action.Name), "params": params}
	if echo != "" {
		frame["echo"] = echo
	}
	return frame, nil
}

// encodeSegments converts segments to wire shape and restores embedded node
// content arrays from their JSON text form.
// encodeSegments 将消息段转换为线上形状，并把内嵌节点 content 由 JSON 文本还原为真实数组。
func encodeSegments(segments []obdomain.Segment) []map[string]any {
	return obdomain.WireSegments(segments)
}

type wireEvent struct {
	Time        int64           `json:"time"`
	SelfID      int64           `json:"self_id"`
	PostType    string          `json:"post_type"`
	MessageType string          `json:"message_type"`
	SubType     string          `json:"sub_type"`
	MessageID   int64           `json:"message_id"`
	UserID      int64           `json:"user_id"`
	GroupID     int64           `json:"group_id"`
	RawMessage  string          `json:"raw_message"`
	Message     json.RawMessage `json:"message"`
	Sender      *wireSender     `json:"sender"`
}

type wireSender struct {
	UserID   int64  `json:"user_id"`
	Nickname string `json:"nickname"`
	Card     string `json:"card"`
}

// mapEvent maps the wire frame onto the pure domain event.
// mapEvent 将线上帧映射为纯领域事件；message 兼容段数组与字符串两种形式。
func mapEvent(data []byte) (obdomain.Event, error) {
	var row wireEvent
	if err := json.Unmarshal(data, &row); err != nil {
		return obdomain.Event{}, err
	}
	event := obdomain.Event{
		Time:       row.Time,
		SelfID:     obdomain.BotID(row.SelfID),
		Post:       obdomain.PostType(row.PostType),
		Type:       obdomain.MessageType(row.MessageType),
		SubType:    row.SubType,
		MessageID:  obdomain.MessageID(row.MessageID),
		UserID:     obdomain.UserID(row.UserID),
		GroupID:    obdomain.GroupID(row.GroupID),
		RawMessage: row.RawMessage,
	}
	if row.Sender != nil {
		event.Sender = obdomain.Sender{
			ID:       obdomain.UserID(row.Sender.UserID),
			Nickname: row.Sender.Nickname,
			Card:     row.Sender.Card,
		}
	}
	if len(row.Message) > 0 {
		if segments, err := obdomain.SegmentsFromWire(row.Message); err == nil {
			event.Segments = segments
		} else {
			var text string
			if err := json.Unmarshal(row.Message, &text); err == nil {
				if event.RawMessage == "" {
					event.RawMessage = text
				}
				event.Segments = obdomain.ParseCQ(text)
			}
		}
	}
	extras := map[string]any{}
	if err := json.Unmarshal(data, &extras); err == nil {
		for _, key := range []string{
			"time", "self_id", "post_type", "message_type", "sub_type",
			"message_id", "user_id", "group_id", "raw_message", "message", "sender",
		} {
			delete(extras, key)
		}
		if len(extras) > 0 {
			event.Extra = extras
		}
	}
	return event, nil
}

func sleep(ctx context.Context, d time.Duration) bool {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

// jitter returns base/2 .. base with randomization to avoid thundering herds.
// jitter 在 base/2 到 base 间随机取值，避免重连风暴。
func jitter(base time.Duration) time.Duration {
	if base <= 0 {
		return time.Second
	}
	half := base / 2
	if half <= 0 {
		return base
	}
	return half + time.Duration(rand.Int63n(int64(half)+1))
}

// endpointOf logs only the host, never the token or query.
// endpointOf 只输出主机名，绝不输出 token 或查询串。
func endpointOf(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return "(invalid)"
	}
	return u.Host
}
