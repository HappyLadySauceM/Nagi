package application

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/HappyLadySauceM/QQBot/internal/onebot/domain"
)

// fakeSource is a controllable event source.
// fakeSource 是可控的事件源。
type fakeSource struct {
	events chan domain.Event
}

func newFakeSource() *fakeSource {
	return &fakeSource{events: make(chan domain.Event, 8)}
}

func (f *fakeSource) Events() <-chan domain.Event { return f.events }

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
	d := NewDispatcher(src, nil, nil)
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

	src.events <- domain.Event{Post: domain.PostMessage, SelfID: 10001}
	src.events <- domain.Event{Post: domain.PostMessage, SelfID: 10001}
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
	d := NewDispatcher(src, nil, nil)
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
	d := NewDispatcher(src, nil, nil)
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
