// Tests for membership notice routing.
// 成员变更通知路由的测试。
package infrastructure

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	obdomain "github.com/HappyLadySauceM/Nagi/internal/onebot/domain"
)

type recordedChange struct {
	kind    string
	botID   int64
	groupID int64
	userID  int64
}

type fakeApplier struct {
	calls chan recordedChange
}

func newFakeApplier() *fakeApplier {
	return &fakeApplier{calls: make(chan recordedChange, 4)}
}

func (f *fakeApplier) ApplyMemberJoin(_ context.Context, botID, groupID, userID int64) error {
	f.calls <- recordedChange{kind: "join", botID: botID, groupID: groupID, userID: userID}
	return nil
}

func (f *fakeApplier) ApplyMemberLeave(_ context.Context, botID, groupID, userID int64) error {
	f.calls <- recordedChange{kind: "leave", botID: botID, groupID: groupID, userID: userID}
	return nil
}

func (f *fakeApplier) ApplyBotJoined(_ context.Context, botID, groupID int64) error {
	f.calls <- recordedChange{kind: "bot_joined", botID: botID, groupID: groupID}
	return nil
}

func (f *fakeApplier) ApplyBotLeft(_ context.Context, botID, groupID int64) error {
	f.calls <- recordedChange{kind: "bot_left", botID: botID, groupID: groupID}
	return nil
}

func discardLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func noticeEvent(notice obdomain.NoticeType, subType string, groupID, userID int64) obdomain.Event {
	return obdomain.Event{
		Post:    obdomain.PostNotice,
		SubType: subType,
		GroupID: obdomain.GroupID(groupID),
		UserID:  obdomain.UserID(userID),
		Extra:   map[string]any{"notice_type": string(notice)},
	}
}

func TestMembershipMatchOnlyAcceptsGroupMembershipNotices(t *testing.T) {
	handler, err := NewMembershipHandler(newFakeApplier(), discardLogger())
	if err != nil {
		t.Fatalf("new handler: %v", err)
	}

	cases := []struct {
		name  string
		event obdomain.Event
		want  bool
	}{
		{"increase", noticeEvent(obdomain.NoticeGroupIncrease, "approve", 7, 42), true},
		{"decrease", noticeEvent(obdomain.NoticeGroupDecrease, "leave", 7, 42), true},
		{"ban", noticeEvent(obdomain.NoticeGroupBan, "", 7, 42), false},
		{"friend add", noticeEvent(obdomain.NoticeFriendAdd, "", 0, 42), false},
		{"no group", noticeEvent(obdomain.NoticeGroupIncrease, "approve", 0, 42), false},
		{"message", obdomain.Event{Post: obdomain.PostMessage, GroupID: 7}, false},
	}
	for _, testCase := range cases {
		if got := handler.Match(testCase.event); got != testCase.want {
			t.Fatalf("%s: match = %v, want %v", testCase.name, got, testCase.want)
		}
	}
}

func TestMembershipHandleRoutesEachVariation(t *testing.T) {
	cases := []struct {
		name  string
		event obdomain.Event
		kind  string
	}{
		{"member join", noticeEvent(obdomain.NoticeGroupIncrease, "approve", 7, 42), "join"},
		{"bot join", noticeEvent(obdomain.NoticeGroupIncrease, "invite", 7, 10000), "bot_joined"},
		{"member leave", noticeEvent(obdomain.NoticeGroupDecrease, "leave", 7, 42), "leave"},
		{"member kick", noticeEvent(obdomain.NoticeGroupDecrease, "kick", 7, 42), "leave"},
		{"bot kicked", noticeEvent(obdomain.NoticeGroupDecrease, "kick_me", 7, 10000), "bot_left"},
	}
	for _, testCase := range cases {
		applier := newFakeApplier()
		handler, err := NewMembershipHandler(applier, discardLogger())
		if err != nil {
			t.Fatalf("new handler: %v", err)
		}
		// The dispatcher's account is authoritative; the frame carries no self_id.
		// 账号以分发器为准；帧内不携带 self_id。
		if err := handler.Handle(context.Background(), obdomain.BotID(10000), testCase.event); err != nil {
			t.Fatalf("%s: handle: %v", testCase.name, err)
		}
		select {
		case call := <-applier.calls:
			if call.kind != testCase.kind || call.botID != 10000 || call.groupID != 7 {
				t.Fatalf("%s: call = %+v, want kind=%s", testCase.name, call, testCase.kind)
			}
		case <-time.After(time.Second):
			t.Fatalf("%s: no membership call recorded", testCase.name)
		}
	}
}

func TestMembershipHandleIgnoresUnknownAccount(t *testing.T) {
	applier := newFakeApplier()
	handler, err := NewMembershipHandler(applier, discardLogger())
	if err != nil {
		t.Fatalf("new handler: %v", err)
	}
	if err := handler.Handle(context.Background(), 0, noticeEvent(obdomain.NoticeGroupIncrease, "approve", 7, 42)); err != nil {
		t.Fatalf("handle: %v", err)
	}
	select {
	case call := <-applier.calls:
		t.Fatalf("unexpected call for unknown account: %+v", call)
	case <-time.After(50 * time.Millisecond):
	}
}
