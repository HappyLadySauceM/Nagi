// Package application implements the OneBot client use cases.
// Package application 实现 OneBot 客户端用例：带 echo 匹配的动作调用与事件流出。
package application

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/HappyLadySauceM/Nagi/internal/onebot/contracts"
	"github.com/HappyLadySauceM/Nagi/internal/onebot/domain"
)

// eventBuffer bounds the in-memory event stream.
// eventBuffer 是事件流出通道的缓冲上限。
const eventBuffer = 256

// callResult carries one awaited action response.
// callResult 承载一次等待中的动作响应。
type callResult struct {
	payload json.RawMessage
	err     error
}

// ClientService turns raw transport frames into typed use cases.
// ClientService 将传输层的原始帧转换为类型化用例，并维护 echo 匹配。
type ClientService struct {
	transport   Transport
	callTimeout time.Duration
	logger      *slog.Logger

	seq     atomic.Uint64
	closed  atomic.Bool
	selfID  atomic.Int64
	selfSet atomic.Bool

	// identityMu serialises first-write identity binding so one connection can
	// never be silently rebound to another account.
	// identityMu 串行化首次身份绑定，避免同一连接被静默改写为其他账号。
	identityMu sync.Mutex

	// Heartbeat visibility derived from meta_event frames.
	// 由 meta_event 帧推导的心跳可见性。
	lastHeartbeatAt   atomic.Int64 // unix nanos; 0 = unseen
	heartbeatInterval atomic.Int64 // milliseconds

	eventsMu     sync.RWMutex
	eventsClosed bool

	pendingMu sync.Mutex
	pending   map[string]chan callResult

	events chan domain.Event
}

// NewClientService binds the service to a transport.
// NewClientService 将客户端服务绑定到传输端口。
func NewClientService(transport Transport, callTimeout time.Duration, logger *slog.Logger) *ClientService {
	if logger == nil {
		logger = slog.Default()
	}
	return &ClientService{
		transport:   transport,
		callTimeout: callTimeout,
		logger:      logger,
		pending:     make(map[string]chan callResult),
		events:      make(chan domain.Event, eventBuffer),
	}
}

// Call sends one action and waits for the echo-matched response.
// Call 发送动作并等待 echo 匹配的响应，同时受 call-timeout 与 ctx 双重约束。
func (s *ClientService) Call(ctx context.Context, action domain.Action) (json.RawMessage, error) {
	if s.closed.Load() {
		return nil, ErrClosed
	}
	echo := fmt.Sprintf("nagi-%d", s.seq.Add(1))
	ch := make(chan callResult, 1)
	s.pendingMu.Lock()
	s.pending[echo] = ch
	s.pendingMu.Unlock()
	defer func() {
		s.pendingMu.Lock()
		delete(s.pending, echo)
		s.pendingMu.Unlock()
	}()
	if err := s.transport.Send(ctx, action, echo); err != nil {
		return nil, fmt.Errorf("onebot: send action %s: %w", action.Name, err)
	}
	var timeoutC <-chan time.Time
	if s.callTimeout > 0 {
		timer := time.NewTimer(s.callTimeout)
		defer timer.Stop()
		timeoutC = timer.C
	}
	select {
	case res := <-ch:
		if res.err != nil {
			return nil, res.err
		}
		return res.payload, nil
	case <-timeoutC:
		return nil, fmt.Errorf("onebot: wait action %s response: %w", action.Name, ErrTimeout)
	case <-ctx.Done():
		return nil, fmt.Errorf("onebot: action %s canceled: %w", action.Name, ctx.Err())
	}
}

// do executes an action and discards the response payload.
// do 执行动作并丢弃响应载荷。
func (s *ClientService) do(ctx context.Context, action domain.Action) error {
	_, err := s.Call(ctx, action)
	return err
}

// SendPrivateMessage implements contracts.Sender.
// SendPrivateMessage 实现 contracts.Sender 的私聊发送。
func (s *ClientService) SendPrivateMessage(ctx context.Context, to domain.UserID, segments ...domain.Segment) error {
	action, err := domain.NewSendPrivateMessage(to, segments)
	if err != nil {
		return err
	}
	return s.do(ctx, action)
}

// SendGroupMessage implements contracts.Sender.
// SendGroupMessage 实现 contracts.Sender 的群聊发送。
func (s *ClientService) SendGroupMessage(ctx context.Context, to domain.GroupID, segments ...domain.Segment) error {
	action, err := domain.NewSendGroupMessage(to, segments)
	if err != nil {
		return err
	}
	return s.do(ctx, action)
}

// DeleteMessage implements contracts.Sender.
// DeleteMessage 实现 contracts.Sender 的消息撤回。
func (s *ClientService) DeleteMessage(ctx context.Context, id domain.MessageID) error {
	action, err := domain.NewDeleteMessage(id)
	if err != nil {
		return err
	}
	return s.do(ctx, action)
}

// HandleResponse implements ResponseSink.
// HandleResponse 实现 ResponseSink：解析结果信封并分发给等待中的调用。
func (s *ClientService) HandleResponse(echo string, payload json.RawMessage) {
	var env struct {
		Status  string          `json:"status"`
		Retcode int64           `json:"retcode"`
		Wording string          `json:"wording"`
		Data    json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(payload, &env); err != nil {
		s.logger.Warn("onebot: malformed action response", "echo", echo, "error", err)
		return
	}
	s.pendingMu.Lock()
	ch, ok := s.pending[echo]
	s.pendingMu.Unlock()
	if !ok {
		s.logger.Debug("onebot: drop response without waiter", "echo", echo)
		return
	}
	if env.Retcode != 0 && env.Retcode != 1 {
		ch <- callResult{err: &APIError{Status: env.Status, Retcode: env.Retcode, Wording: env.Wording}}
		return
	}
	ch <- callResult{payload: env.Data}
}

// HandleEvent implements EventSink.
// HandleEvent 实现 EventSink：记录自身账号与最近心跳，并发布事件。
func (s *ClientService) HandleEvent(event domain.Event) {
	if event.SelfID != 0 {
		if err := s.BindIdentity(event.SelfID); err != nil {
			// A connection that changes identity mid-flight would mix two accounts'
			// data, so drop the event instead of overwriting the binding.
			// 连接中途改变身份会导致两个账号数据混用，因此丢弃事件而不是覆盖绑定。
			s.logger.Error("onebot: drop event from unexpected account",
				"event_self_id", int64(event.SelfID), "error", err)
			return
		}
	}
	if event.IsHeartbeat() {
		s.lastHeartbeatAt.Store(time.Now().UnixNano())
		if interval := event.HeartbeatIntervalMillis(); interval > 0 {
			s.heartbeatInterval.Store(interval)
		}
	}
	s.eventsMu.RLock()
	defer s.eventsMu.RUnlock()
	if s.eventsClosed {
		return
	}
	select {
	case s.events <- event:
	default:
		s.logger.Warn("onebot: event channel full, event dropped", "post_type", string(event.Post))
	}
}

// Events implements contracts.EventSource.
// Events 实现 contracts.EventSource 的事件读取。
func (s *ClientService) Events() <-chan domain.Event { return s.events }

// Connected implements contracts.EventSource.
// Connected 实现 contracts.EventSource 的连接状态查询。
func (s *ClientService) Connected() bool {
	return s.transport != nil && s.transport.Connected()
}

// BindIdentity records the account identity observed by get_login_info or the
// first event. The first observation wins; a different id returns an error so
// callers can reject the connection rather than mix two accounts.
// BindIdentity 记录 get_login_info 或首个事件观测到的账号身份。首次观测生效；
// 后续不一致返回错误，调用方应拒绝该连接而不是混用两个账号。
func (s *ClientService) BindIdentity(botID domain.BotID) error {
	if botID <= 0 {
		return fmt.Errorf("onebot: bot id must be positive")
	}
	s.identityMu.Lock()
	defer s.identityMu.Unlock()
	if s.selfSet.Load() {
		if current := s.selfID.Load(); current != int64(botID) {
			return fmt.Errorf("onebot: connection bound to bot %d, refusing bot %d", current, int64(botID))
		}
		return nil
	}
	s.selfID.Store(int64(botID))
	s.selfSet.Store(true)
	return nil
}

// SelfID returns the bot account observed from recent events.
// SelfID 返回从事件中观测到的机器人账号。
func (s *ClientService) SelfID() (domain.BotID, bool) {
	if !s.selfSet.Load() {
		return 0, false
	}
	return domain.BotID(s.selfID.Load()), true
}

// Status implements contracts.StatusReader.
// Status 实现 contracts.StatusReader。
func (s *ClientService) Status() contracts.BotStatus {
	id, known := s.SelfID()
	out := contracts.BotStatus{Connected: s.Connected(), SelfID: id, SelfIDKnown: known}
	if nano := s.lastHeartbeatAt.Load(); nano != 0 {
		out.HeartbeatSeen = true
		out.LastHeartbeatAt = time.Unix(0, nano)
		out.HeartbeatIntervalMillis = s.heartbeatInterval.Load()
	}
	return out
}

// Close stops accepting calls and tears down the transport and event stream.
// Close 停止接收调用，并关闭事件流与传输。
func (s *ClientService) Close() error {
	s.closed.Store(true)
	s.eventsMu.Lock()
	if !s.eventsClosed {
		s.eventsClosed = true
		close(s.events)
	}
	s.eventsMu.Unlock()
	if s.transport == nil {
		return nil
	}
	return s.transport.Close()
}
