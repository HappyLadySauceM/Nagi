package domain

import "testing"

func TestNewSendPrivateMessageValidatesInput(t *testing.T) {
	// 验证非法用户 ID 与空消息段都会失败。
	if _, err := NewSendPrivateMessage(0, []Segment{Text("hi")}); err == nil {
		t.Fatal("expected error for invalid user id")
	}
	if _, err := NewSendPrivateMessage(10001, nil); err == nil {
		t.Fatal("expected error for empty segments")
	}
	if _, err := NewSendGroupMessage(0, []Segment{Text("hi")}); err == nil {
		t.Fatal("expected error for invalid group id")
	}
	if _, err := NewDeleteMessage(0); err == nil {
		t.Fatal("expected error for invalid message id")
	}
}

func TestNewSendPrivateMessageClonesSegments(t *testing.T) {
	// 验证构造后修改原始消息段不影响动作参数。
	segments := []Segment{Text("hello")}
	action, err := NewSendPrivateMessage(10001, segments)
	if err != nil {
		t.Fatalf("new action: %v", err)
	}
	segments[0].Data["text"] = "mutated"
	params, ok := action.Params["message"].([]Segment)
	if !ok {
		t.Fatal("message param missing")
	}
	if PlainText(params) != "hello" {
		t.Fatalf("params were mutated: %q", PlainText(params))
	}
}

func TestEventTextPrefersSegmentsThenRawMessage(t *testing.T) {
	// 验证 Text 优先取消息段纯文本，回退 raw_message。
	event := Event{
		RawMessage: "raw",
		Segments:   []Segment{Text("seg"), {Type: SegImage, Data: map[string]string{"file": "a.png"}}},
	}
	if event.Text() != "seg" {
		t.Fatalf("text = %q", event.Text())
	}
	event.Segments = nil
	if event.Text() != "raw" {
		t.Fatalf("fallback text = %q", event.Text())
	}
}

func TestEventGroupPredicates(t *testing.T) {
	// 验证消息事件与群聊场景的判断。
	group := Event{Post: PostMessage, Type: TypeGroup}
	if !group.IsMessage() || !group.InGroup() {
		t.Fatal("group message event not recognized")
	}
	private := Event{Post: PostMessage, Type: TypePrivate}
	if private.InGroup() {
		t.Fatal("private message misclassified as group")
	}
	notice := Event{Post: PostNotice}
	if notice.IsMessage() {
		t.Fatal("notice misclassified as message")
	}
}

func TestAtAndReplyFormatIDs(t *testing.T) {
	// 验证命名类型 ID 被格式化为字符串数据。
	if got := At(UserID(10001)).Data["qq"]; got != "10001" {
		t.Fatalf("at qq = %q", got)
	}
	if got := Reply(MessageID(42)).Data["id"]; got != "42" {
		t.Fatalf("reply id = %q", got)
	}
}
