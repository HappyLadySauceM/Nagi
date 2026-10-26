package infrastructure

import (
	"context"
	"testing"

	echoapp "github.com/HappyLadySauceM/Nagi/internal/echo/application"
	obcontracts "github.com/HappyLadySauceM/Nagi/internal/onebot/contracts"
	"github.com/HappyLadySauceM/Nagi/internal/onebot/domain"
)

// noopSender discards all outgoing messages.
// noopSender 丢弃所有发出的消息。
type noopSender struct{}

func (noopSender) SendPrivateMessage(ctx context.Context, to domain.UserID, segments ...domain.Segment) error {
	return nil
}

func (noopSender) SendGroupMessage(ctx context.Context, to domain.GroupID, segments ...domain.Segment) error {
	return nil
}

func (noopSender) DeleteMessage(ctx context.Context, id domain.MessageID) error { return nil }

func (s noopSender) Sender(botID int64) (obcontracts.Sender, bool) { return s, true }

func TestHandlerMatchesOnlyPingAndPrefixedMessages(t *testing.T) {
	// 验证 Match 仅命中 ping 与前缀指令。
	svc, err := echoapp.NewService(noopSender{}, "/echo")
	if err != nil {
		t.Fatalf("new service: %v", err)
	}
	h := NewHandler(svc, "/echo")
	cases := []struct {
		text string
		want bool
	}{
		{text: "ping", want: true},
		{text: "  ping  ", want: true},
		{text: "/echo hi", want: true},
		{text: "hello", want: false},
		{text: "/echox", want: false},
	}
	for _, c := range cases {
		event := domain.Event{Post: domain.PostMessage, RawMessage: c.text}
		if got := h.Match(event); got != c.want {
			t.Fatalf("match(%q) = %v, want %v", c.text, got, c.want)
		}
	}
	if h.Match(domain.Event{Post: domain.PostNotice, RawMessage: "ping"}) {
		t.Fatal("notice event should not match")
	}
}

func TestHandlerHandlesPingWithoutError(t *testing.T) {
	// 验证 ping 事件进入 ping 用例且不出错。
	svc, _ := echoapp.NewService(noopSender{}, "/echo")
	h := NewHandler(svc, "/echo")
	if err := h.Handle(context.Background(), 100, domain.Event{Post: domain.PostMessage, RawMessage: "ping"}); err != nil {
		t.Fatalf("handle ping: %v", err)
	}
	if h.Name() != "echo" {
		t.Fatalf("name = %q", h.Name())
	}
}
