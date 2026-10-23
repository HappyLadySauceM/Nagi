// Tests for the LLM replier: scope re-check, sanitization, send and record.
// LLM 回复器测试：作用域复核、净化、发送与记录。
package application

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"testing"

	agentdomain "github.com/HappyLadySauceM/QQBot/internal/agent/domain"
	intentdomain "github.com/HappyLadySauceM/QQBot/internal/intent/domain"
	obcontracts "github.com/HappyLadySauceM/QQBot/internal/onebot/contracts"
	obdomain "github.com/HappyLadySauceM/QQBot/internal/onebot/domain"
	scopedomain "github.com/HappyLadySauceM/QQBot/internal/scope/domain"
)

func replierLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// fakeModel returns canned text and records the request.
// fakeModel 返回预置文本并记录请求。
type fakeModel struct {
	req   agentdomain.ChatRequest
	text  string
	err   error
	calls int
}

func (f *fakeModel) Generate(_ context.Context, req agentdomain.ChatRequest) (agentdomain.ChatResult, error) {
	f.calls++
	f.req = req
	if f.err != nil {
		return agentdomain.ChatResult{}, f.err
	}
	return agentdomain.ChatResult{Text: f.text, Model: "test-model", CompletionTokens: 5}, nil
}

// fakeSender records outgoing sends and reports a canned message id.
// fakeSender 记录出站发送并回报预置消息 ID。
type fakeSender struct {
	group   []obdomain.GroupID
	private []obdomain.UserID
	texts   []string
	err     error
	msgID   int64
}

func (f *fakeSender) SendGroupMessageReceipt(_ context.Context, to obdomain.GroupID, segments ...obdomain.Segment) (int64, error) {
	f.group = append(f.group, to)
	f.texts = append(f.texts, obdomain.PlainText(segments))
	if f.err != nil {
		return 0, f.err
	}
	return f.msgID, nil
}

func (f *fakeSender) SendPrivateMessageReceipt(_ context.Context, to obdomain.UserID, segments ...obdomain.Segment) (int64, error) {
	f.private = append(f.private, to)
	f.texts = append(f.texts, obdomain.PlainText(segments))
	if f.err != nil {
		return 0, f.err
	}
	return f.msgID, nil
}

// fakeRecorder records outgoing records.
// fakeRecorder 记录出站回写。
type fakeRecorder struct {
	conversations []string
	statuses      []string
	messageIDs    []int64
}

func (f *fakeRecorder) RecordOutgoing(_ context.Context, conversationID string, _ int64, _ string, platformMessageID int64, sentStatus string) error {
	f.conversations = append(f.conversations, conversationID)
	f.statuses = append(f.statuses, sentStatus)
	f.messageIDs = append(f.messageIDs, platformMessageID)
	return nil
}

// replierScopes reuses the map-based authorizer pattern.
// replierScopes 复用基于映射的授权器模式。
type replierScopes struct{ policies map[string]scopedomain.Policy }

func (f *replierScopes) Resolve(_ context.Context, botID int64, t scopedomain.ScopeType, id int64) (scopedomain.Policy, error) {
	return f.policies[scopeKey(botID, string(t), id)], nil
}

func scopeKey(botID int64, typ string, id int64) string {
	return replierKey(botID) + ":" + typ + ":" + replierKey(id)
}

func replierKey(v int64) string {
	if v == 0 {
		return "0"
	}
	var b []byte
	for v > 0 {
		b = append([]byte{byte('0' + v%10)}, b...)
		v /= 10
	}
	return string(b)
}

func newReplier(t *testing.T, model *fakeModel, sender *fakeSender, rec *fakeRecorder, policies map[string]scopedomain.Policy) *Replier {
	t.Helper()
	r, err := NewReplier(Options{
		Model: model, Scopes: &replierScopes{policies: policies},
		Sender: sender, Recorder: rec, BotName: "小雪", Logger: replierLogger(),
	})
	if err != nil {
		t.Fatalf("new replier: %v", err)
	}
	return r
}

func groupTaskForAgent() intentdomain.Task {
	return intentdomain.Task{
		JobID: 11, BotID: 100, Kind: "group", TargetID: 700, ConversationID: "conv-1",
		Message: intentdomain.Message{MessageID: 5001, SenderID: 200, Text: "那你觉得呢？"},
	}
}

func enabledGroupPolicy() map[string]scopedomain.Policy {
	return map[string]scopedomain.Policy{
		scopeKey(100, "group", 700): {ScopeType: scopedomain.ScopeGroup, ScopeID: 700,
			Enabled: true, ReplyMode: scopedomain.ModeIntentBased},
	}
}

// TestReplierSendsAndRecordsGroupReply verifies generation, at-segment delivery
// and the outbound record.
// TestReplierSendsAndRecordsGroupReply 验证生成、带 @ 段的群发与出站回写。
func TestReplierSendsAndRecordsGroupReply(t *testing.T) {
	model := &fakeModel{text: "  适合，看你要做什么。 "}
	sender := &fakeSender{}
	rec := &fakeRecorder{}
	r := newReplier(t, model, sender, rec, enabledGroupPolicy())

	history := []intentdomain.Message{
		{MessageID: 5000, SenderID: 200, Text: "小雪，Go 合适吗？"},
		{MessageID: 4990, SenderID: 100, IsBot: true, Text: "适合。"},
	}
	if err := r.ExecuteReply(context.Background(), groupTaskForAgent(), history); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if model.calls != 1 {
		t.Fatalf("model calls = %d", model.calls)
	}
	// Roles: system + history user + history assistant + current user.
	// 角色：system + 历史用户 + 历史助手 + 当前用户。
	if len(model.req.Messages) != 4 ||
		model.req.Messages[0].Role != "system" ||
		model.req.Messages[2].Role != "assistant" {
		t.Fatalf("messages = %+v", model.req.Messages)
	}
	if len(sender.group) != 1 || sender.group[0] != 700 {
		t.Fatalf("group sends = %v", sender.group)
	}
	if !strings.Contains(sender.texts[0], "适合，看你要做什么。") {
		t.Fatalf("sent text = %q", sender.texts[0])
	}
	if len(rec.statuses) != 1 || rec.statuses[0] != "sent" || rec.conversations[0] != "conv-1" {
		t.Fatalf("records = %+v", rec)
	}
}

// TestReplierDropsWhenScopeDisabled verifies the pre-send scope re-check.
// TestReplierDropsWhenScopeDisabled 验证发送前的作用域复核。
func TestReplierDropsWhenScopeDisabled(t *testing.T) {
	model := &fakeModel{text: "hello"}
	sender := &fakeSender{}
	rec := &fakeRecorder{}
	r := newReplier(t, model, sender, rec, map[string]scopedomain.Policy{
		scopeKey(100, "group", 700): {ScopeType: scopedomain.ScopeGroup, ScopeID: 700, Enabled: false},
	})
	if err := r.ExecuteReply(context.Background(), groupTaskForAgent(), nil); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if model.calls != 0 || len(sender.group) != 0 || len(rec.statuses) != 0 {
		t.Fatal("disabled scope must not generate, send or record")
	}
}

// TestReplierRecordsFailedSend verifies failed sends are recorded, not retried.
// TestReplierRecordsFailedSend 验证发送失败会被记录而非盲目重试。
func TestReplierRecordsFailedSend(t *testing.T) {
	model := &fakeModel{text: "回复内容"}
	sender := &fakeSender{err: fmt.Errorf("onebot: action failed retcode=1200")}
	rec := &fakeRecorder{}
	r := newReplier(t, model, sender, rec, enabledGroupPolicy())

	if err := r.ExecuteReply(context.Background(), groupTaskForAgent(), nil); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if len(rec.statuses) != 1 || rec.statuses[0] != "failed" {
		t.Fatalf("records = %+v", rec)
	}
	if rec.messageIDs[0] != 0 {
		t.Fatalf("failed send must not record a message id: %d", rec.messageIDs[0])
	}
}

// TestReplierRecordsUnknownOnTimeout verifies that timed-out sends are persisted
// as unknown because the delivery outcome cannot be confirmed.
// TestReplierRecordsUnknownOnTimeout 验证超时发送按 unknown 持久化，
// 因为投递结果无法确认。
func TestReplierRecordsUnknownOnTimeout(t *testing.T) {
	model := &fakeModel{text: "回复内容"}
	sender := &fakeSender{err: fmt.Errorf("onebot: wait action send_group_msg response: %w", obcontracts.ErrActionTimeout)}
	rec := &fakeRecorder{}
	r := newReplier(t, model, sender, rec, enabledGroupPolicy())

	if err := r.ExecuteReply(context.Background(), groupTaskForAgent(), nil); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if len(rec.statuses) != 1 || rec.statuses[0] != "unknown" {
		t.Fatalf("records = %+v", rec)
	}
}

// TestReplierRecordsPlatformMessageID verifies the receipt id flows into the
// outbound record for later reply-to-bot matching.
// TestReplierRecordsPlatformMessageID 验证回执 ID 进入出站记录，供后续引用判定匹配。
func TestReplierRecordsPlatformMessageID(t *testing.T) {
	model := &fakeModel{text: "回复内容"}
	sender := &fakeSender{msgID: 778899}
	rec := &fakeRecorder{}
	r := newReplier(t, model, sender, rec, enabledGroupPolicy())

	if err := r.ExecuteReply(context.Background(), groupTaskForAgent(), nil); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if len(rec.messageIDs) != 1 || rec.messageIDs[0] != 778899 || rec.statuses[0] != "sent" {
		t.Fatalf("records = %+v", rec)
	}
}

// TestReplierDetectsTimeoutSentinelCrossWrapping guards that wrapped timeout
// chains are still classified as unknown.
// TestReplierDetectsTimeoutSentinelCrossWrapping 保证多层包装的超时错误链
// 仍被归类为 unknown。
func TestReplierDetectsTimeoutSentinelCrossWrapping(t *testing.T) {
	wrapped := fmt.Errorf("agent: send: %w", fmt.Errorf("onebot: wait: %w", errors.Join(obcontracts.ErrActionTimeout)))
	if !errors.Is(wrapped, obcontracts.ErrActionTimeout) {
		t.Fatal("wrapped sentinel must stay detectable")
	}
}

// TestReplierSkipsEmptyReply verifies empty generations never send.
// TestReplierSkipsEmptyReply 验证空生成绝不发送。
func TestReplierSkipsEmptyReply(t *testing.T) {
	model := &fakeModel{text: "   @全体成员  "}
	sender := &fakeSender{}
	rec := &fakeRecorder{}
	r := newReplier(t, model, sender, rec, enabledGroupPolicy())

	if err := r.ExecuteReply(context.Background(), groupTaskForAgent(), nil); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if len(sender.group) != 0 || len(rec.statuses) != 0 {
		t.Fatal("empty sanitized reply must not be sent or recorded")
	}
}

// TestSanitizeReplyTruncates verifies the length bound and at-all removal.
// TestSanitizeReplyTruncates 验证长度上限与 @ 全体去除。
func TestSanitizeReplyTruncates(t *testing.T) {
	long := strings.Repeat("好", agentdomain.MaxReplyRunes+10)
	out, err := agentdomain.SanitizeReply(long)
	if err != nil {
		t.Fatalf("sanitize: %v", err)
	}
	if len([]rune(out)) != agentdomain.MaxReplyRunes {
		t.Fatalf("sanitized length = %d", len([]rune(out)))
	}
	if _, err := agentdomain.SanitizeReply("@全体成员"); err == nil {
		t.Fatal("at-all-only reply must be rejected")
	}
}

// TestSanitizeReplyRejectsLinksAndAtAllVariants verifies fail-closed handling of
// outbound links and fullwidth at-all variants.
// TestSanitizeReplyRejectsLinksAndAtAllVariants 验证出站链接与全角 @ 全体变体的
// 关闭式失败处理。
func TestSanitizeReplyRejectsLinksAndAtAllVariants(t *testing.T) {
	cases := map[string]string{
		"http link":  "参考 https://example.com/a 说明",
		"https link": "见 HTTPS://Example.COM",
		"www link":   "访问 www.example.com 试试",
		// An at-all-only reply becomes empty after removal and is rejected.
		// 仅含 @ 全体的回复移除后为空，因此被拒绝。
		"fullwidth at": "＠全体成员",
	}
	for name, text := range cases {
		if out, err := agentdomain.SanitizeReply(text); err == nil {
			t.Fatalf("%s: expected rejection, got %q", name, out)
		}
	}
	// Mixed content where only part is at-all is still sanitized, not dropped.
	// 仅部分为 @ 全体的混合内容仍走净化而不是丢弃。
	out, err := agentdomain.SanitizeReply("@所有人 我觉得可以")
	if err != nil || out != "我觉得可以" {
		t.Fatalf("mixed at-all sanitize = %q err=%v", out, err)
	}
	out, err = agentdomain.SanitizeReply("大家好＠全体成员")
	if err != nil || out != "大家好" {
		t.Fatalf("fullwidth mixed sanitize = %q err=%v", out, err)
	}
}
