// Tests for the LLM replier: scope re-check, sanitization, send and record.
// LLM 回复器测试：作用域复核、净化、发送与记录。
package application

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"testing"
	"unicode/utf8"

	"go.opentelemetry.io/otel"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	agentcontracts "github.com/HappyLadySauceM/QQBot/internal/agent/contracts"
	agentdomain "github.com/HappyLadySauceM/QQBot/internal/agent/domain"
	intentdomain "github.com/HappyLadySauceM/QQBot/internal/intent/domain"
	obcontracts "github.com/HappyLadySauceM/QQBot/internal/onebot/contracts"
	obdomain "github.com/HappyLadySauceM/QQBot/internal/onebot/domain"
	scopedomain "github.com/HappyLadySauceM/QQBot/internal/scope/domain"
)

func replierLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// fakeRuntime returns canned text and records the request.
// fakeRuntime 返回预置文本并记录请求。
type fakeRuntime struct {
	req   agentcontracts.Request
	text  string
	err   error
	calls int
}

func (f *fakeRuntime) Run(_ context.Context, req agentcontracts.Request) (agentcontracts.Result, error) {
	f.calls++
	f.req = req
	if f.err != nil {
		return agentcontracts.Result{}, f.err
	}
	return agentcontracts.Result{Text: f.text, Model: "test-model", OutputUsage: 5}, nil
}

// fakeSender records outgoing sends and reports a canned message id.
// fakeSender 记录出站发送并回报预置消息 ID。
type fakeSender struct {
	group   []obdomain.GroupID
	private []obdomain.UserID
	texts   []string
	botIDs  []int64
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

// ReceiptSender records which account the reply was routed through.
// ReceiptSender 记录回复实际使用的账号。
func (f *fakeSender) ReceiptSender(botID int64) (ReceiptSender, bool) {
	f.botIDs = append(f.botIDs, botID)
	return f, true
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

func newReplier(t *testing.T, fake *fakeRuntime, sender *fakeSender, rec *fakeRecorder, policies map[string]scopedomain.Policy) *Replier {
	t.Helper()
	r, err := NewReplier(Options{
		Runtime: fake, Scopes: &replierScopes{policies: policies},
		Senders: sender, Recorder: rec, BotName: "小雪", Logger: replierLogger(),
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
	fake := &fakeRuntime{text: "  适合，看你要做什么。 "}
	sender := &fakeSender{}
	rec := &fakeRecorder{}
	r := newReplier(t, fake, sender, rec, enabledGroupPolicy())

	history := []intentdomain.Message{
		{MessageID: 5000, SenderID: 200, Text: "小雪，Go 合适吗？"},
		{MessageID: 4990, SenderID: 100, IsBot: true, Text: "适合。"},
	}
	if err := r.ExecuteReply(context.Background(), groupTaskForAgent(), history); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if fake.calls != 1 {
		t.Fatalf("model calls = %d", fake.calls)
	}
	// Roles: system + history user + history assistant + current user.
	// 角色：system + 历史用户 + 历史助手 + 当前用户。
	if len(fake.req.Messages) != 4 ||
		fake.req.Messages[0].Role != "system" ||
		fake.req.Messages[2].Role != "assistant" {
		t.Fatalf("messages = %+v", fake.req.Messages)
	}
	if len(sender.group) != 1 || sender.group[0] != 700 {
		t.Fatalf("group sends = %v", sender.group)
	}
	if len(sender.botIDs) != 1 || sender.botIDs[0] != 100 {
		t.Fatalf("reply routed through accounts %v, want [100]", sender.botIDs)
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
	fake := &fakeRuntime{text: "hello"}
	sender := &fakeSender{}
	rec := &fakeRecorder{}
	r := newReplier(t, fake, sender, rec, map[string]scopedomain.Policy{
		scopeKey(100, "group", 700): {ScopeType: scopedomain.ScopeGroup, ScopeID: 700, Enabled: false},
	})
	if err := r.ExecuteReply(context.Background(), groupTaskForAgent(), nil); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if fake.calls != 0 || len(sender.group) != 0 || len(rec.statuses) != 0 {
		t.Fatal("disabled scope must not generate, send or record")
	}
}

// TestReplierRecordsFailedSend verifies failed sends are recorded, not retried.
// TestReplierRecordsFailedSend 验证发送失败会被记录而非盲目重试。
func TestReplierRecordsFailedSend(t *testing.T) {
	fake := &fakeRuntime{text: "回复内容"}
	sender := &fakeSender{err: fmt.Errorf("onebot: action failed retcode=1200")}
	rec := &fakeRecorder{}
	r := newReplier(t, fake, sender, rec, enabledGroupPolicy())

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
	fake := &fakeRuntime{text: "回复内容"}
	sender := &fakeSender{err: fmt.Errorf("onebot: wait action send_group_msg response: %w", obcontracts.ErrActionTimeout)}
	rec := &fakeRecorder{}
	r := newReplier(t, fake, sender, rec, enabledGroupPolicy())

	if err := r.ExecuteReply(context.Background(), groupTaskForAgent(), nil); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if len(rec.statuses) != 1 || rec.statuses[0] != "unknown" {
		t.Fatalf("records = %+v", rec)
	}
}

// TestReplierAttributesSentStatusOnSpan verifies that delivery outcome reaches
// the send span before it ends, including the unknown timeout case.
// TestReplierAttributesSentStatusOnSpan 验证投递结果在发送 span 结束前写入，
// 覆盖结果未知的超时场景。
func TestReplierAttributesSentStatusOnSpan(t *testing.T) {
	cases := []struct {
		name    string
		sendErr error
		want    string
	}{
		{name: "sent", want: "sent"},
		{name: "failed", sendErr: fmt.Errorf("onebot: action failed retcode=1200"), want: "failed"},
		{name: "unknown", sendErr: fmt.Errorf("onebot: wait: %w", obcontracts.ErrActionTimeout), want: "unknown"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			recorder := tracetest.NewSpanRecorder()
			provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
			previous := otel.GetTracerProvider()
			otel.SetTracerProvider(provider)
			t.Cleanup(func() {
				otel.SetTracerProvider(previous)
				_ = provider.Shutdown(context.Background())
			})

			fake := &fakeRuntime{text: "回复内容"}
			sender := &fakeSender{err: tc.sendErr, msgID: 4242}
			r := newReplier(t, fake, sender, &fakeRecorder{}, enabledGroupPolicy())
			if err := r.ExecuteReply(context.Background(), groupTaskForAgent(), nil); err != nil {
				t.Fatalf("execute: %v", err)
			}

			status := ""
			for _, span := range recorder.Ended() {
				if span.Name() != "qqbot.onebot.send" {
					continue
				}
				for _, kv := range span.Attributes() {
					if string(kv.Key) == "qqbot.sent_status" {
						status = kv.Value.Emit()
					}
				}
			}
			if status != tc.want {
				t.Fatalf("qqbot.sent_status = %q, want %q", status, tc.want)
			}
		})
	}
}

// TestReplierRecordsPlatformMessageID verifies the receipt id flows into the
// outbound record for later reply-to-bot matching.
// TestReplierRecordsPlatformMessageID 验证回执 ID 进入出站记录，供后续引用判定匹配。
func TestReplierRecordsPlatformMessageID(t *testing.T) {
	fake := &fakeRuntime{text: "回复内容"}
	sender := &fakeSender{msgID: 778899}
	rec := &fakeRecorder{}
	r := newReplier(t, fake, sender, rec, enabledGroupPolicy())

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

// TestReplierIncludesCurrentMessageOnce verifies that the persisted copy of the
// current message in history is not appended a second time.
// TestReplierIncludesCurrentMessageOnce 验证历史中已落库的当前消息不会被再次追加。
func TestReplierIncludesCurrentMessageOnce(t *testing.T) {
	fake := &fakeRuntime{text: "回复内容"}
	r := newReplier(t, fake, &fakeSender{}, &fakeRecorder{}, enabledGroupPolicy())
	history := []intentdomain.Message{
		{MessageID: 4001, SenderID: 200, Text: "早先的提问"},
		{MessageID: 4002, SenderID: 100, IsBot: true, Text: "早先的回答"},
		{MessageID: 5001, SenderID: 200, Text: "那你觉得呢？"},
	}

	if err := r.ExecuteReply(context.Background(), groupTaskForAgent(), history); err != nil {
		t.Fatalf("execute: %v", err)
	}
	occurrences := 0
	for _, message := range fake.req.Messages {
		if strings.Contains(message.Content, "那你觉得呢？") {
			occurrences++
		}
	}
	if occurrences != 1 {
		t.Fatalf("current message appears %d times, want 1: %+v", occurrences, fake.req.Messages)
	}
	if len(fake.req.Messages) != 4 {
		t.Fatalf("messages = %d, want system + 2 history + current", len(fake.req.Messages))
	}
	if !strings.Contains(fake.req.Messages[1].Content, "早先的提问") {
		t.Fatalf("older history must survive dedup: %+v", fake.req.Messages[1])
	}
	last := fake.req.Messages[len(fake.req.Messages)-1]
	if last.Role != "user" || !strings.Contains(last.Content, "那你觉得呢？") {
		t.Fatalf("current message must stay last: %+v", last)
	}
}

// TestReplierKeepsHistoryWithoutMessageID verifies that dedup never drops
// entries when the current message has no platform id.
// TestReplierKeepsHistoryWithoutMessageID 验证当前消息没有平台 ID 时不去重历史项。
func TestReplierKeepsHistoryWithoutMessageID(t *testing.T) {
	fake := &fakeRuntime{text: "回复内容"}
	r := newReplier(t, fake, &fakeSender{}, &fakeRecorder{}, enabledGroupPolicy())
	task := groupTaskForAgent()
	task.Message.MessageID = 0
	history := []intentdomain.Message{
		{MessageID: 0, SenderID: 200, Text: "那你觉得呢？"},
	}

	if err := r.ExecuteReply(context.Background(), task, history); err != nil {
		t.Fatalf("execute: %v", err)
	}
	occurrences := 0
	for _, message := range fake.req.Messages {
		if strings.Contains(message.Content, "那你觉得呢？") {
			occurrences++
		}
	}
	if occurrences != 2 {
		t.Fatalf("history must be kept without an id, got %d occurrences", occurrences)
	}
}

// TestTraceMessagesJSONIsBoundedAndValid verifies the exported payload stays
// valid JSON within the exporter truncation limit.
// TestTraceMessagesJSONIsBoundedAndValid 验证导出载荷在截断阈值内仍是合法 JSON。
func TestTraceMessagesJSONIsBoundedAndValid(t *testing.T) {
	messages := []agentdomain.ChatMessage{{Role: "system", Content: "系统提示"}}
	for i := 0; i < 40; i++ {
		messages = append(messages, agentdomain.ChatMessage{Role: "user", Content: strings.Repeat("长", 2000)})
	}
	payload, truncated, err := traceMessagesJSON(messages)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !truncated {
		t.Fatal("dropping or clipping messages must mark the payload as truncated")
	}
	if utf8.RuneCountInString(payload) >= 8192 {
		t.Fatalf("payload too large: %d runes", utf8.RuneCountInString(payload))
	}
	var decoded []agentdomain.ChatMessage
	if err := json.Unmarshal([]byte(payload), &decoded); err != nil {
		t.Fatalf("payload must stay valid JSON: %v", err)
	}
	if len(decoded) != maxTraceMessages {
		t.Fatalf("messages = %d, want %d", len(decoded), maxTraceMessages)
	}
	if decoded[0].Role != "system" {
		t.Fatalf("system prompt must be kept first: %+v", decoded[0])
	}
	for _, message := range decoded {
		if utf8.RuneCountInString(message.Content) > maxTraceMessageRunes+1 {
			t.Fatalf("message not clipped: %d runes", utf8.RuneCountInString(message.Content))
		}
	}
}

// TestReplierAttributesGenerationMetadata verifies the run span reports usage
// and model, and leaves the generation type to Eino's model callback so one
// model call is never counted twice.
// TestReplierAttributesGenerationMetadata 验证运行 span 只报告用量与模型，
// generation 类型交给 Eino 模型回调，避免一次模型调用被重复计数。
func TestReplierAttributesGenerationMetadata(t *testing.T) {
	recorder := tracetest.NewSpanRecorder()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
	previous := otel.GetTracerProvider()
	otel.SetTracerProvider(provider)
	t.Cleanup(func() {
		otel.SetTracerProvider(previous)
		_ = provider.Shutdown(context.Background())
	})

	r := newReplier(t, &fakeRuntime{text: "回复内容"}, &fakeSender{msgID: 1}, &fakeRecorder{}, enabledGroupPolicy())
	if err := r.ExecuteReply(context.Background(), groupTaskForAgent(), nil); err != nil {
		t.Fatalf("execute: %v", err)
	}

	attrs := map[string]string{}
	for _, span := range recorder.Ended() {
		if span.Name() != "qqbot.agent.run" {
			continue
		}
		for _, kv := range span.Attributes() {
			attrs[string(kv.Key)] = kv.Value.Emit()
		}
	}
	if _, ok := attrs["langfuse.observation.type"]; ok {
		t.Fatalf("the run span must not claim the generation type: %q", attrs["langfuse.observation.type"])
	}
	if attrs["qqbot.model"] != "test-model" {
		t.Fatalf("model = %q", attrs["qqbot.model"])
	}
	if attrs["qqbot.usage.output"] != "5" {
		t.Fatalf("output usage = %q", attrs["qqbot.usage.output"])
	}
	if _, ok := attrs["langfuse.observation.model.name"]; ok {
		t.Fatal("the run span must not carry a model attribute that infers a generation")
	}
	if attrs["qqbot.trace.truncated"] != "false" {
		t.Fatalf("truncated = %q, want false", attrs["qqbot.trace.truncated"])
	}
	if _, ok := attrs["langfuse.observation.usage_details"]; ok {
		t.Fatal("model usage belongs to the Eino callback, not the run span")
	}
}

// TestTraceMessagesJSONMarksSmallPayloadsUncut verifies ordinary prompts are
// not reported as truncated.
// TestTraceMessagesJSONMarksSmallPayloadsUncut 验证普通 prompt 不会被标记为裁剪。
func TestTraceMessagesJSONMarksSmallPayloadsUncut(t *testing.T) {
	payload, truncated, err := traceMessagesJSON([]agentdomain.ChatMessage{
		{Role: "system", Content: "系统提示"},
		{Role: "user", Content: "你好"},
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if truncated {
		t.Fatal("small payload must not be marked truncated")
	}
	var decoded []agentdomain.ChatMessage
	if err := json.Unmarshal([]byte(payload), &decoded); err != nil || len(decoded) != 2 {
		t.Fatalf("payload = %q err=%v", payload, err)
	}
}

// TestReplierSkipsEmptyReply verifies empty generations never send.
// TestReplierSkipsEmptyReply 验证空生成绝不发送。
func TestReplierSkipsEmptyReply(t *testing.T) {
	fake := &fakeRuntime{text: "   @全体成员  "}
	sender := &fakeSender{}
	rec := &fakeRecorder{}
	r := newReplier(t, fake, sender, rec, enabledGroupPolicy())

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
