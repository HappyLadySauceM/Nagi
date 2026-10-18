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

	"github.com/HappyLadySauceM/QQBot/internal/onebot/contracts"
	"github.com/HappyLadySauceM/QQBot/internal/onebot/domain"
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
	echo := fmt.Sprintf("qqbot-%d", s.seq.Add(1))
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
// HandleEvent 实现 EventSink：记录自身账号并发布事件。
func (s *ClientService) HandleEvent(event domain.Event) {
	if event.SelfID != 0 {
		s.selfID.Store(int64(event.SelfID))
		s.selfSet.Store(true)
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
	return contracts.BotStatus{Connected: s.Connected(), SelfID: id, SelfIDKnown: known}
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
