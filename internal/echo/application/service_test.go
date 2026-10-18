package application

import (
	"context"
	"strings"
	"testing"

	obcontracts "github.com/HappyLadySauceM/QQBot/internal/onebot/contracts"
	"github.com/HappyLadySauceM/QQBot/internal/onebot/domain"
)

// fakeSender records outgoing messages per chat type.
// fakeSender 按会话类型记录发出的消息。
type fakeSender struct {
	private []fakeMessage
	group   []fakeMessage
}

type fakeMessage struct {
	to       int64
	segments []domain.Segment
}

func (f *fakeSender) SendPrivateMessage(ctx context.Context, to domain.UserID, segments ...domain.Segment) error {
	f.private = append(f.private, fakeMessage{to: int64(to), segments: segments})
	return nil
}

func (f *fakeSender) SendGroupMessage(ctx context.Context, to domain.GroupID, segments ...domain.Segment) error {
	f.group = append(f.group, fakeMessage{to: int64(to), segments: segments})
	return nil
}

func (f *fakeSender) DeleteMessage(ctx context.Context, id domain.MessageID) error { return nil }

var _ obcontracts.Sender = (*fakeSender)(nil)

func TestHandlePingRepliesPongToGroup(t *testing.T) {
	// 验证群内 ping 回复 pong。
	sender := &fakeSender{}
	svc, err := NewService(sender, "/echo")
	if err != nil {
		t.Fatalf("new service: %v", err)
	}
	event := domain.Event{Post: domain.PostMessage, Type: domain.TypeGroup, GroupID: 456, RawMessage: "ping"}
	if err := svc.HandlePing(context.Background(), event); err != nil {
		t.Fatalf("handle: %v", err)
	}
	if len(sender.group) != 1 || sender.group[0].to != 456 || domain.PlainText(sender.group[0].segments) != "pong" {
		t.Fatalf("group sends = %+v", sender.group)
	}
}

func TestHandlePingIgnoresNonPing(t *testing.T) {
	// 验证非 ping 消息不回复。
	sender := &fakeSender{}
	svc, _ := NewService(sender, "/echo")
	event := domain.Event{Post: domain.PostMessage, Type: domain.TypePrivate, UserID: 111, RawMessage: "hello"}
	if err := svc.HandlePing(context.Background(), event); err != nil {
		t.Fatalf("handle: %v", err)
	}
	if len(sender.private) != 0 || len(sender.group) != 0 {
		t.Fatal("unexpected reply")
	}
}

func TestHandleEchoCommandRepeatsText(t *testing.T) {
	// 验证前缀指令复读剩余文本。
	sender := &fakeSender{}
	svc, _ := NewService(sender, "/echo")
	event := domain.Event{Post: domain.PostMessage, Type: domain.TypePrivate, UserID: 111, RawMessage: "/echo hello world"}
	if err := svc.HandleEchoCommand(context.Background(), event); err != nil {
		t.Fatalf("handle: %v", err)
	}
	if len(sender.private) != 1 || domain.PlainText(sender.private[0].segments) != "hello world" {
		t.Fatalf("private sends = %+v", sender.private)
	}
}

func TestHandleEchoCommandWithoutTextReturnsUsage(t *testing.T) {
	// 验证空前缀指令回复用法提示。
	sender := &fakeSender{}
	svc, _ := NewService(sender, "/echo")
	event := domain.Event{Post: domain.PostMessage, Type: domain.TypeGroup, GroupID: 456, RawMessage: "/echo"}
	if err := svc.HandleEchoCommand(context.Background(), event); err != nil {
		t.Fatalf("handle: %v", err)
	}
	if len(sender.group) != 1 || !strings.Contains(domain.PlainText(sender.group[0].segments), "usage") {
		t.Fatalf("group sends = %+v", sender.group)
	}
}

func TestNewServiceRejectsInvalidInput(t *testing.T) {
	// 验证空发送端口与前缀被拒绝。
	if _, err := NewService(nil, "/echo"); err == nil {
		t.Fatal("expected error for nil sender")
	}
	if _, err := NewService(&fakeSender{}, "  "); err == nil {
		t.Fatal("expected error for empty prefix")
	}
}
