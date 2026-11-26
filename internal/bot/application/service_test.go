package application

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"sync/atomic"
	"testing"
	"time"

	obcontracts "github.com/HappyLadySauceM/Nagi/internal/onebot/contracts"
	"github.com/HappyLadySauceM/Nagi/internal/onebot/domain"
	scopedomain "github.com/HappyLadySauceM/Nagi/internal/scope/domain"
)

// fakeSource is a controllable event source.
// fakeSource 是可控的事件源。
type fakeSource struct {
	events chan obcontracts.EventFrame
}

func newFakeSource() *fakeSource {
	return &fakeSource{events: make(chan obcontracts.EventFrame, 8)}
}

func (f *fakeSource) Events() <-chan obcontracts.EventFrame { return f.events }

func (f *fakeSource) Connected() bool { return true }

// recordingHandler records handled events and can be told to panic.
// recordingHandler 记录被处理的事件，并可注入 panic。
type recordingHandler struct {
	name    string
	match   func(domain.Event) bool
	handled *atomic.Int32
	botID   *atomic.Int64
	panicOn bool
}

func (h *recordingHandler) Name() string { return h.name }

func (h *recordingHandler) Match(event domain.Event) bool { return h.match(event) }

func (h *recordingHandler) Handle(ctx context.Context, botID domain.BotID, event domain.Event) error {
	h.handled.Add(1)
	if h.botID != nil {
		h.botID.Store(int64(botID))
	}
	if h.panicOn {
		panic("boom")
	}
	return nil
}

func TestDispatcherRoutesMatchingHandlersAndRecoversPanics(t *testing.T) {
	// 验证分发按 Match 路由、panic 被恢复且循环继续。
	src := newFakeSource()
	d := NewDispatcher(src, nil, nil, nil)
	var handled, panicked, unmatched atomic.Int32
	var observed atomic.Int64
	if err := d.Register(&recordingHandler{
		name:    "h1",
		match:   func(domain.Event) bool { return true },
		handled: &handled,
		botID:   &observed,
	}); err != nil {
		t.Fatalf("register: %v", err)
	}
	if err := d.Register(&recordingHandler{
		name:    "panic",
		match:   func(domain.Event) bool { return true },
		handled: &panicked,
		panicOn: true,
	}); err != nil {
		t.Fatalf("register: %v", err)
	}
	if err := d.Register(&recordingHandler{
		name:    "h2",
		match:   func(domain.Event) bool { return false },
		handled: &unmatched,
	}); err != nil {
		t.Fatalf("register: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		_ = d.Run(ctx)
		close(done)
	}()

	src.events <- obcontracts.EventFrame{Route: domain.Event{Post: domain.PostNotice, SelfID: 10001}}
	src.events <- obcontracts.EventFrame{Route: domain.Event{Post: domain.PostNotice, SelfID: 10001}}
	deadline := time.Now().Add(2 * time.Second)
	for handled.Load() < 2 && time.Now().Before(deadline) {
		time.Sleep(2 * time.Millisecond)
	}
	cancel()
	<-done

	if handled.Load() != 2 {
		t.Fatalf("handled = %d", handled.Load())
	}
	if got := observed.Load(); got != 10001 {
		t.Fatalf("handler bot id = %d, want 10001", got)
	}
	if panicked.Load() != 2 {
		t.Fatalf("panicking handler executions = %d", panicked.Load())
	}
	if unmatched.Load() != 0 {
		t.Fatalf("non-matching handler executed %d times", unmatched.Load())
	}
	if d.HandledCount() != 2 {
		t.Fatalf("handled count = %d", d.HandledCount())
	}
	if names := d.Names(); len(names) != 3 {
		t.Fatalf("names = %v", names)
	}
}

func TestDispatcherRejectsDuplicateNames(t *testing.T) {
	// 验证重名处理器被拒绝。
	src := newFakeSource()
	d := NewDispatcher(src, nil, nil, nil)
	var counter atomic.Int32
	if err := d.Register(&recordingHandler{
		name:    "dup",
		match:   func(domain.Event) bool { return false },
		handled: &counter,
	}); err != nil {
		t.Fatalf("register: %v", err)
	}
	err := d.Register(&recordingHandler{
		name:    "dup",
		match:   func(domain.Event) bool { return false },
		handled: &counter,
	})
	if err == nil {
		t.Fatal("expected duplicate name error")
	}
}

func TestDispatcherReturnsWhenChannelCloses(t *testing.T) {
	// 验证事件通道关闭后 Run 返回。
	src := newFakeSource()
	d := NewDispatcher(src, nil, nil, nil)
	done := make(chan error, 1)
	go func() { done <- d.Run(context.Background()) }()
	close(src.events)
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("run: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("run did not return after channel close")
	}
}

type scopeCheck func(context.Context, int64, scopedomain.ScopeType, int64) (scopedomain.Policy, error)

func (f scopeCheck) Resolve(ctx context.Context, bot int64, kind scopedomain.ScopeType, target int64) (scopedomain.Policy, error) {
	return f(ctx, bot, kind, target)
}

func TestDispatcherChecksScopeBeforeDecodingOrMatching(t *testing.T) {
	var logs bytes.Buffer
	var checks, decoded, matched int
	var handled atomic.Int32
	allowed := false
	authorizer := scopeCheck(func(_ context.Context, bot int64, kind scopedomain.ScopeType, target int64) (scopedomain.Policy, error) {
		checks++
		if kind != scopedomain.ScopeGroup || target != 21 {
			t.Fatalf("scope %d/%s/%d", bot, kind, target)
		}
		if bot == 12 {
			return scopedomain.Policy{Enabled: true}, errors.New("sensitive fixture lookup detail")
		}
		return scopedomain.Policy{Enabled: allowed}, nil
	})
	d := NewDispatcher(nil, nil, authorizer, slog.New(slog.NewJSONHandler(&logs, nil)))
	if err := d.Register(&recordingHandler{name: "probe", handled: &handled, match: func(e domain.Event) bool { matched++; return e.RawMessage == "synthetic" }}); err != nil {
		t.Fatal(err)
	}
	group := domain.Event{Post: domain.PostMessage, Type: domain.TypeGroup, SelfID: 11, GroupID: 21, UserID: 31}
	frame := obcontracts.EventFrame{Route: group, Decode: func() (domain.Event, error) { decoded++; e := group; e.RawMessage = "synthetic"; return e, nil }}
	NewDispatcher(nil, nil, nil, nil).dispatch(context.Background(), frame)
	d.dispatch(context.Background(), frame)
	allowed = true
	d.dispatch(context.Background(), frame)
	other := frame
	other.Route.SelfID = 12
	d.dispatch(context.Background(), other)
	allowed = false
	d.dispatch(context.Background(), frame)
	if checks != 4 || decoded != 1 || matched != 1 || handled.Load() != 1 {
		t.Fatalf("checks=%d decoded=%d matched=%d handled=%d", checks, decoded, matched, handled.Load())
	}
	if got := logs.String(); !bytes.Contains([]byte(got), []byte("ingress scope lookup failed")) || bytes.Contains([]byte(got), []byte("sensitive fixture")) || bytes.Contains([]byte(got), []byte("bot_id")) {
		t.Fatalf("unexpected fault log %s", got)
	}
}
