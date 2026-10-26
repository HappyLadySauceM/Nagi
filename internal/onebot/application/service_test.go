package application

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/HappyLadySauceM/Nagi/internal/onebot/domain"
)

// fakeTransport records sends, optionally failing them or auto-responding.
// fakeTransport 记录发送的动作帧，可注入发送失败或自动回响应。
type fakeTransport struct {
	mu        sync.Mutex
	sent      []sentFrame
	events    chan domain.Event
	connected bool
	closed    bool
	onSend    func(echo string)
}

type sentFrame struct {
	action domain.Action
	echo   string
}

func newFakeTransport() *fakeTransport {
	return &fakeTransport{events: make(chan domain.Event, 1)}
}

func (f *fakeTransport) Send(ctx context.Context, action domain.Action, echo string) error {
	f.mu.Lock()
	f.sent = append(f.sent, sentFrame{action: action, echo: echo})
	f.mu.Unlock()
	if f.onSend != nil {
		f.onSend(echo)
	}
	return nil
}

// sentFrames snapshots recorded frames safely.
// sentFrames 返回已记录动作帧的安全快照。
func (f *fakeTransport) sentFrames() []sentFrame {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]sentFrame(nil), f.sent...)
}

func (f *fakeTransport) Events() <-chan domain.Event { return f.events }

func (f *fakeTransport) Connected() bool { return f.connected }

func (f *fakeTransport) Close() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.closed = true
	return nil
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for !cond() && time.Now().Before(deadline) {
		time.Sleep(2 * time.Millisecond)
	}
}

func TestCallMatchesResponseByEcho(t *testing.T) {
	// 验证动作响应按 echo 精确匹配并解出 data 载荷。
	tr := newFakeTransport()
	svc := NewClientService(tr, 2*time.Second, nil)
	action, err := domain.NewSendPrivateMessage(10001, []domain.Segment{domain.Text("hi")})
	if err != nil {
		t.Fatalf("new action: %v", err)
	}
	type callOut struct {
		payload json.RawMessage
		err     error
	}
	out := make(chan callOut, 1)
	go func() {
		payload, err := svc.Call(context.Background(), action)
		out <- callOut{payload: payload, err: err}
	}()
	waitFor(t, func() bool { return len(tr.sentFrames()) == 1 })
	frames := tr.sentFrames()
	if len(frames) != 1 {
		t.Fatal("action was not sent")
	}
	echo := frames[0].echo
	raw, _ := json.Marshal(map[string]any{
		"status": "ok", "retcode": 0, "echo": echo,
		"data": map[string]any{"message_id": 42},
	})
	svc.HandleResponse(echo, raw)
	result := <-out
	if result.err != nil {
		t.Fatalf("call: %v", result.err)
	}
	var data struct {
		MessageID int64 `json:"message_id"`
	}
	if err := json.Unmarshal(result.payload, &data); err != nil {
		t.Fatalf("unmarshal data: %v", err)
	}
	if data.MessageID != 42 {
		t.Fatalf("message_id = %d", data.MessageID)
	}
}

func TestCallTimesOutWithoutResponse(t *testing.T) {
	// 验证超时返回 ErrTimeout 且等待者被清理。
	tr := newFakeTransport()
	svc := NewClientService(tr, 20*time.Millisecond, nil)
	action, _ := domain.NewSendGroupMessage(123, []domain.Segment{domain.Text("hi")})
	if _, err := svc.Call(context.Background(), action); !errors.Is(err, ErrTimeout) {
		t.Fatalf("err = %v", err)
	}
	svc.pendingMu.Lock()
	pending := len(svc.pending)
	svc.pendingMu.Unlock()
	if pending != 0 {
		t.Fatalf("pending entries leaked: %d", pending)
	}
}

func TestCallSurfacesAPIError(t *testing.T) {
	// 验证失败 retcode 转换为 APIError。
	tr := newFakeTransport()
	svc := NewClientService(tr, time.Second, nil)
	action, _ := domain.NewSendPrivateMessage(10001, []domain.Segment{domain.Text("hi")})
	out := make(chan error, 1)
	go func() {
		_, err := svc.Call(context.Background(), action)
		out <- err
	}()
	waitFor(t, func() bool { return len(tr.sentFrames()) == 1 })
	raw, _ := json.Marshal(map[string]any{"status": "failed", "retcode": 1200, "wording": "no permission"})
	svc.HandleResponse(tr.sentFrames()[0].echo, raw)
	err := <-out
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.Retcode != 1200 {
		t.Fatalf("err = %v", err)
	}
}

func TestSendGroupMessageBuildsTypedAction(t *testing.T) {
	// 验证群聊发送构造正确的动作与参数并等待成功响应。
	tr := newFakeTransport()
	svc := NewClientService(tr, time.Second, nil)
	tr.onSend = func(echo string) {
		raw, _ := json.Marshal(map[string]any{"status": "ok", "retcode": 0, "echo": echo})
		svc.HandleResponse(echo, raw)
	}
	if err := svc.SendGroupMessage(context.Background(), 123, domain.Text("hi")); err != nil {
		t.Fatalf("send: %v", err)
	}
	frames := tr.sentFrames()
	if len(frames) != 1 || frames[0].action.Name != domain.ActionSendGroup {
		t.Fatalf("frames = %+v", frames)
	}
	if frames[0].action.Params["group_id"] != int64(123) {
		t.Fatalf("group_id = %v", frames[0].action.Params["group_id"])
	}
}

func TestHandleEventPublishesAndRecordsSelfID(t *testing.T) {
	// 验证事件进入通道且 self_id 被记录。
	tr := newFakeTransport()
	svc := NewClientService(tr, 0, nil)
	svc.HandleEvent(domain.Event{SelfID: 10000, Post: domain.PostMeta})
	select {
	case event := <-svc.Events():
		if event.SelfID != 10000 {
			t.Fatalf("self id = %d", event.SelfID)
		}
	default:
		t.Fatal("event not published")
	}
	if id, ok := svc.SelfID(); !ok || id != 10000 {
		t.Fatalf("self id = %d known = %v", id, ok)
	}
}

func TestCloseRejectsFurtherCalls(t *testing.T) {
	// 验证关闭后调用返回 ErrClosed 且传输被关闭。
	tr := newFakeTransport()
	svc := NewClientService(tr, 0, nil)
	if err := svc.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	_, err := svc.Call(context.Background(), domain.Action{Name: domain.ActionGetLoginInfo})
	if !errors.Is(err, ErrClosed) {
		t.Fatalf("err = %v", err)
	}
	if !tr.closed {
		t.Fatal("transport not closed")
	}
}
