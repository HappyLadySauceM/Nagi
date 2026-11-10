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
	"time"
	"unicode/utf8"

	"go.opentelemetry.io/otel"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	agentcontracts "github.com/HappyLadySauceM/Nagi/internal/agent/contracts"
	agentdomain "github.com/HappyLadySauceM/Nagi/internal/agent/domain"
	auditcontracts "github.com/HappyLadySauceM/Nagi/internal/audit/contracts"
	auditdomain "github.com/HappyLadySauceM/Nagi/internal/audit/domain"
	intentdomain "github.com/HappyLadySauceM/Nagi/internal/intent/domain"
	memorycontracts "github.com/HappyLadySauceM/Nagi/internal/memory/contracts"
	"github.com/HappyLadySauceM/Nagi/internal/observability"
	obcontracts "github.com/HappyLadySauceM/Nagi/internal/onebot/contracts"
	obdomain "github.com/HappyLadySauceM/Nagi/internal/onebot/domain"
	outboundapp "github.com/HappyLadySauceM/Nagi/internal/outbound/application"
	outbounddomain "github.com/HappyLadySauceM/Nagi/internal/outbound/domain"
	scopedomain "github.com/HappyLadySauceM/Nagi/internal/scope/domain"
)

func replierLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// fakeRuntime returns canned text and records the request.
// fakeRuntime 返回预置文本并记录请求。
type fakeRuntime struct {
	onRun        func(agentcontracts.Request)
	req          agentcontracts.Request
	text         string
	err          error
	calls        int
	toolsExposed int
	toolRuns     []agentcontracts.ToolRun
	// suppress marks the run as having touched credentials.
	// suppress 标记本次运行接触过凭证。
	suppress bool
}

func (f *fakeRuntime) Run(ctx context.Context, req agentcontracts.Request) (agentcontracts.Result, error) {
	f.calls++
	f.req = req
	if f.suppress {
		observability.SuppressContent(ctx)
	}
	if f.onRun != nil {
		f.onRun(req)
	}
	if f.err != nil {
		return agentcontracts.Result{}, f.err
	}
	return agentcontracts.Result{
		Text: f.text, Model: "test-model", OutputUsage: 5,
		ToolsExposed: f.toolsExposed, ToolRuns: f.toolRuns,
	}, nil
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
	outboundIDs   []string
}

// fakeRunAudit captures the mandatory local timeline used by one worker run.
// fakeRunAudit 捕获单个工作器运行所需的本地时间线。
type fakeRunAudit struct {
	events    []auditdomain.AIEvent
	finished  []auditdomain.AIEvent
	failStage string
}

func (*fakeRunAudit) StartRun(context.Context, auditdomain.AIRun) (auditdomain.AIRun, error) {
	return auditdomain.AIRun{}, nil
}

func (*fakeRunAudit) FinishRun(context.Context, string, string, string) error { return nil }

func (f *fakeRunAudit) RecordEvent(_ context.Context, event auditdomain.AIEvent) (auditdomain.AIEvent, error) {
	if event.Stage == f.failStage {
		return auditdomain.AIEvent{}, errors.New("audit unavailable")
	}
	event.ID = fmt.Sprintf("audit-%d", len(f.events)+1)
	f.events = append(f.events, event)
	return event, nil
}

func (f *fakeRunAudit) FinishEvent(_ context.Context, id, status, code string, output []byte) error {
	f.finished = append(f.finished, auditdomain.AIEvent{ID: id, Status: status, ErrorCode: code, Output: output})
	return nil
}

func (*fakeRunAudit) RecordArtifact(context.Context, auditdomain.AIArtifact) (auditdomain.AIArtifact, error) {
	return auditdomain.AIArtifact{}, nil
}

func (f *fakeRecorder) RecordOutgoing(_ context.Context, conversationID string, _ int64, _ string, platformMessageID int64, sentStatus string, outboundID string, claim ...string) error {
	f.conversations = append(f.conversations, conversationID)
	f.statuses = append(f.statuses, sentStatus)
	f.messageIDs = append(f.messageIDs, platformMessageID)
	f.outboundIDs = append(f.outboundIDs, outboundID)
	return nil
}

// fakeLedger stands in for the outbound coordinator and records each step.
// fakeLedger 替代出站协调器并记录每一步。
type fakeLedger struct {
	denied       []string
	refusals     []string
	reserved     []outboundapp.Request
	sending      []string
	settled      []outboundapp.Outcome
	delivered    bool
	deliveredErr error
	// deliveredTargets declares per-target deliveries when non-nil.
	// deliveredTargets 非空时按目标声明投递结果。
	deliveredTargets map[string]bool
	reserveErr       error
	markErr          error
	settleErr        error
}

func (f *fakeLedger) Reserve(_ context.Context, req outboundapp.Request) (outbounddomain.Record, error) {
	f.reserved = append(f.reserved, req)
	if f.reserveErr != nil {
		return outbounddomain.Record{}, f.reserveErr
	}
	sequence := len(f.reserved)
	return outbounddomain.Record{
		ID: fmt.Sprintf("out-%d", sequence), JobID: req.JobID, ConversationID: req.ConversationID,
		Sequence: sequence, Text: req.Text, Status: outbounddomain.StatusPrepared,
	}, nil
}

func (f *fakeLedger) MarkSending(_ context.Context, id string, claim ...string) error {
	if f.markErr != nil {
		return f.markErr
	}
	f.sending = append(f.sending, id)
	return nil
}

func (f *fakeLedger) Settle(_ context.Context, _ string, outcome outboundapp.Outcome, claim ...string) error {
	if f.settleErr != nil {
		return f.settleErr
	}
	f.settled = append(f.settled, outcome)
	return nil
}

func (f *fakeLedger) RecordRefusal(_ context.Context, _ int64, reason string, _ ...string) error {
	f.refusals = append(f.refusals, reason)
	return nil
}

func (f *fakeLedger) HasDelivered(_ context.Context, _ int64) (bool, error) {
	return f.delivered, f.deliveredErr
}

func (f *fakeLedger) HasDeliveredTo(_ context.Context, _ int64, kind string, targetID int64) (bool, error) {
	if f.deliveredErr != nil {
		return false, f.deliveredErr
	}
	if f.deliveredTargets == nil {
		return f.delivered, nil
	}
	return f.deliveredTargets[fmt.Sprintf("%s:%d", kind, targetID)], nil
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
	return newReplierWithLedger(t, fake, sender, rec, policies, &fakeLedger{})
}

func newReplierWithLedger(t *testing.T, fake *fakeRuntime, sender *fakeSender, rec *fakeRecorder,
	policies map[string]scopedomain.Policy, ledger *fakeLedger,
) *Replier {
	t.Helper()
	if sender.err == nil && sender.msgID == 0 {
		sender.msgID = 1
	}
	r, err := NewReplier(Options{
		Runtime: fake, Scopes: &replierScopes{policies: policies},
		Senders: sender, Recorder: rec, Outbound: ledger,
		Sending: outbounddomain.Limits{MaxMessages: 3, MaxCharsPerMessage: 500, MaxTotalChars: 1500},
		BotName: "小雪", Logger: replierLogger(),
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
			StoreMessages: true, Enabled: true, AllowHistoryRead: true, ReplyMode: scopedomain.ModeIntentBased,
			ToolPermissions: map[string]bool{"qq.group.read": false}},
	}
}
func memoryReadGroupPolicy() map[string]scopedomain.Policy {
	policies := enabledGroupPolicy()
	p := policies[scopeKey(100, "group", 700)]
	p.AllowMemoryRead = true
	policies[scopeKey(100, "group", 700)] = p
	return policies
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
	// Scope overrides must reach the runtime; dropping them would silently
	// restore a permission the administrator revoked.
	// 作用域覆盖必须传给运行时；丢失会悄悄恢复管理员已撤销的权限。
	if granted, ok := fake.req.PermissionOverrides["qq.group.read"]; !ok || granted {
		t.Fatalf("permission overrides = %+v", fake.req.PermissionOverrides)
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
	// The logical message must point at the delivery record that produced it.
	// 逻辑消息必须关联到产生它的投递记录。
	if len(rec.outboundIDs) != 1 || rec.outboundIDs[0] != "out-1" {
		t.Fatalf("outbound ids = %v", rec.outboundIDs)
	}
}

// TestReplierWritesDeliveryAuditBeforeTransport proves a missing local audit
// start record cannot reach OneBot and the settled ledger outcome is retained.
// TestReplierWritesDeliveryAuditBeforeTransport 验证发送前已写入本地审计开始记录，且记录落定后的台账结果。
func TestReplierWritesDeliveryAuditBeforeTransport(t *testing.T) {
	fake := &fakeRuntime{text: "可以。"}
	sender := &fakeSender{}
	rec := &fakeRecorder{}
	r := newReplier(t, fake, sender, rec, enabledGroupPolicy())
	audit := &fakeRunAudit{}
	ctx := auditcontracts.WithRun(context.Background(), audit, "run-1")
	if err := r.ExecuteReply(ctx, groupTaskForAgent(), nil); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if len(sender.group) != 1 || len(audit.events) != 2 {
		t.Fatalf("sends=%v events=%+v", sender.group, audit.events)
	}
	if audit.events[0].Stage != "agent.generation" || audit.events[1].Stage != "outbound.deliver" || audit.events[1].Sequence != 2 {
		t.Fatalf("events = %+v", audit.events)
	}
	if len(audit.finished) != 2 || audit.finished[1].Status != "succeeded" {
		t.Fatalf("finished = %+v", audit.finished)
	}
	var output map[string]any
	if err := json.Unmarshal(audit.finished[1].Output, &output); err != nil {
		t.Fatalf("decode delivery output: %v", err)
	}
	if output["status"] != string(outbounddomain.StatusSent) || output["platform_message_id"] != float64(1) {
		t.Fatalf("delivery output = %+v", output)
	}
}

func TestReplierStopsBeforeTransportWhenDeliveryAuditStartFails(t *testing.T) {
	fake := &fakeRuntime{text: "可以。"}
	sender := &fakeSender{}
	rec := &fakeRecorder{}
	ledger := &fakeLedger{}
	r := newReplierWithLedger(t, fake, sender, rec, enabledGroupPolicy(), ledger)
	audit := &fakeRunAudit{failStage: "outbound.deliver"}
	ctx := auditcontracts.WithRun(context.Background(), audit, "run-1")
	if err := r.ExecuteReply(ctx, groupTaskForAgent(), nil); err == nil {
		t.Fatal("delivery audit start failure must stop the transport")
	}
	if len(sender.group) != 0 || len(ledger.sending) != 0 {
		t.Fatalf("transport reached after audit failure: sends=%v sending=%v", sender.group, ledger.sending)
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
	sender := &fakeSender{err: definiteSendError{}}
	rec := &fakeRecorder{}
	ledger := &fakeLedger{}
	r := newReplierWithLedger(t, fake, sender, rec, enabledGroupPolicy(), ledger)

	if err := r.ExecuteReply(context.Background(), groupTaskForAgent(), nil); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if len(rec.statuses) != 1 || rec.statuses[0] != "failed" {
		t.Fatalf("records = %+v", rec)
	}
	if rec.messageIDs[0] != 0 {
		t.Fatalf("failed send must not record a message id: %d", rec.messageIDs[0])
	}
	// The ledger must carry the same outcome; dropping it would leave the
	// record stuck in sending and block every later retry.
	// 台账必须记录同样的结果；丢失会让记录停留在 sending 并阻塞后续重试。
	if len(ledger.settled) != 1 || ledger.settled[0].Status != outbounddomain.StatusFailed ||
		ledger.settled[0].ErrorCode != "send_failed" {
		t.Fatalf("settled = %+v", ledger.settled)
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
	ledger := &fakeLedger{}
	r := newReplierWithLedger(t, fake, sender, rec, enabledGroupPolicy(), ledger)

	if err := r.ExecuteReply(context.Background(), groupTaskForAgent(), nil); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if len(rec.statuses) != 1 || rec.statuses[0] != "unknown" {
		t.Fatalf("records = %+v", rec)
	}
	if len(ledger.settled) != 1 || ledger.settled[0].Status != outbounddomain.StatusUnknown ||
		ledger.settled[0].ErrorCode != "timeout" {
		t.Fatalf("settled = %+v", ledger.settled)
	}
}

// TestReplierDeniesOverBudget verifies that a budget rejection ends the job
// without sending or recording the reply.
// TestReplierDeniesOverBudget 验证预算拒绝会直接结束任务，不发送也不回写回复。
func TestReplierDeniesOverBudget(t *testing.T) {
	recorder := tracetest.NewSpanRecorder()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
	previous := otel.GetTracerProvider()
	otel.SetTracerProvider(provider)
	t.Cleanup(func() {
		otel.SetTracerProvider(previous)
		_ = provider.Shutdown(context.Background())
	})

	sender := &fakeSender{}
	rec := &fakeRecorder{}
	ledger := &fakeLedger{reserveErr: &outbounddomain.BudgetError{
		Dimension: outbounddomain.DimensionMessageChars, Limit: 2, Requested: 4,
	}}
	r := newReplierWithLedger(t, &fakeRuntime{text: "回复内容"}, sender, rec, enabledGroupPolicy(), ledger)

	if err := r.ExecuteReply(context.Background(), groupTaskForAgent(), nil); err != nil {
		t.Fatalf("budget denial must not fail the job: %v", err)
	}
	if len(sender.texts) != 0 || len(ledger.sending) != 0 || len(rec.statuses) != 0 {
		t.Fatalf("denied reply must not be sent (texts=%v sending=%v records=%+v)",
			sender.texts, ledger.sending, rec)
	}
	denied := false
	for _, span := range recorder.Ended() {
		if span.Name() != "nagi.outbound.reserve" {
			continue
		}
		for _, kv := range span.Attributes() {
			if string(kv.Key) == "nagi.outbound_denied" && kv.Value.AsBool() {
				denied = true
			}
		}
	}
	if !denied {
		t.Fatal("reserve span must report nagi.outbound_denied=true")
	}
}

// TestReplierSkipsAlreadyDelivered verifies that a retried job never sends the
// final text again once the ledger holds a delivered record.
// TestReplierSkipsAlreadyDelivered 验证台账已有投递记录时，重试的任务不会
// 再次发送最终文本。
func TestReplierSkipsAlreadyDelivered(t *testing.T) {
	sender := &fakeSender{}
	rec := &fakeRecorder{}
	ledger := &fakeLedger{delivered: true}
	r := newReplierWithLedger(t, &fakeRuntime{text: "回复内容"}, sender, rec, enabledGroupPolicy(), ledger)

	if err := r.ExecuteReply(context.Background(), groupTaskForAgent(), nil); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if len(ledger.reserved) != 0 || len(sender.texts) != 0 || len(rec.statuses) != 0 {
		t.Fatalf("delivered job must not send again (reserved=%v texts=%v records=%+v)",
			ledger.reserved, sender.texts, rec)
	}
}

// TestReplierFailsClosedWhenLedgerUnavailable verifies that an unreadable
// ledger blocks sending instead of risking a duplicate.
// TestReplierFailsClosedWhenLedgerUnavailable 验证台账不可读时阻止发送，
// 避免重复投递。
func TestReplierFailsClosedWhenLedgerUnavailable(t *testing.T) {
	sender := &fakeSender{}
	ledger := &fakeLedger{deliveredErr: errors.New("db down")}
	r := newReplierWithLedger(t, &fakeRuntime{text: "回复内容"}, sender, &fakeRecorder{}, enabledGroupPolicy(), ledger)

	if err := r.ExecuteReply(context.Background(), groupTaskForAgent(), nil); err == nil {
		t.Fatal("an unreadable ledger must fail the run instead of sending")
	}
	if len(sender.texts) != 0 {
		t.Fatalf("nothing may be sent without the ledger: %v", sender.texts)
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
		{name: "failed", sendErr: definiteSendError{}, want: "failed"},
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
				if span.Name() != "nagi.onebot.send" {
					continue
				}
				for _, kv := range span.Attributes() {
					if string(kv.Key) == "nagi.sent_status" {
						status = kv.Value.Emit()
					}
				}
			}
			if status != tc.want {
				t.Fatalf("nagi.sent_status = %q, want %q", status, tc.want)
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
	// Chinese text costs three bytes per rune, so the rune budget alone would
	// overflow the exporter cap and replace the whole prompt with a marker.
	// 中文每字三字节，只看字数会超出导出上限，令整份 prompt 被替换成标记。
	if len(payload) > maxTracePayloadBytes {
		t.Fatalf("payload too large: %d bytes", len(payload))
	}
	var decoded []agentdomain.ChatMessage
	if err := json.Unmarshal([]byte(payload), &decoded); err != nil {
		t.Fatalf("payload must stay valid JSON: %v", err)
	}
	if len(decoded) >= maxTraceMessages || len(decoded) < 2 {
		t.Fatalf("messages = %d, want a bounded set that keeps system and the newest turn", len(decoded))
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

	r := newReplier(t, &fakeRuntime{
		text: "回复内容", toolsExposed: 2,
		toolRuns: []agentcontracts.ToolRun{{ToolID: "system.current_time", Status: "ok"}},
	}, &fakeSender{msgID: 1}, &fakeRecorder{}, enabledGroupPolicy())
	if err := r.ExecuteReply(context.Background(), groupTaskForAgent(), nil); err != nil {
		t.Fatalf("execute: %v", err)
	}

	attrs := map[string]string{}
	for _, span := range recorder.Ended() {
		if span.Name() != "nagi.agent.run" {
			continue
		}
		for _, kv := range span.Attributes() {
			attrs[string(kv.Key)] = kv.Value.Emit()
		}
	}
	if _, ok := attrs["langfuse.observation.type"]; ok {
		t.Fatalf("the run span must not claim the generation type: %q", attrs["langfuse.observation.type"])
	}
	if attrs["nagi.model"] != "test-model" {
		t.Fatalf("model = %q", attrs["nagi.model"])
	}
	if attrs["nagi.usage.output"] != "5" {
		t.Fatalf("output usage = %q", attrs["nagi.usage.output"])
	}
	if attrs["nagi.agent.tools_exposed"] != "2" || attrs["nagi.agent.tool_calls"] != "1" {
		t.Fatalf("tool attributes = %q/%q", attrs["nagi.agent.tools_exposed"], attrs["nagi.agent.tool_calls"])
	}
	if _, ok := attrs["langfuse.observation.model.name"]; ok {
		t.Fatal("the run span must not carry a model attribute that infers a generation")
	}
	if attrs["nagi.trace.truncated"] != "false" {
		t.Fatalf("truncated = %q, want false", attrs["nagi.trace.truncated"])
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

// TestReplierUsesPerScopeLimits verifies a scope-specific policy overrides the
// static sending budget before the reservation is made.
// TestReplierUsesPerScopeLimits 验证按作用域策略会在预留前覆盖静态发送预算。
func TestReplierUsesPerScopeLimits(t *testing.T) {
	fake := &fakeRuntime{text: "好的"}
	sender := &fakeSender{msgID: 7}
	rec := &fakeRecorder{}
	ledger := &fakeLedger{}
	r, err := NewReplier(Options{
		Runtime: fake, Scopes: &replierScopes{policies: map[string]scopedomain.Policy{
			"100:group:700": {ScopeType: scopedomain.ScopeGroup, ScopeID: 700, StoreMessages: true, Enabled: true, ReplyMode: scopedomain.ModeAlwaysReply},
		}},
		Senders: sender, Recorder: rec, Outbound: ledger,
		Sending: outbounddomain.Limits{MaxMessages: 3, MaxCharsPerMessage: 500, MaxTotalChars: 1500},
		SendingLimits: func(context.Context, int64, string, int64) outbounddomain.Limits {
			return outbounddomain.Limits{MaxMessages: 1, MaxCharsPerMessage: 80, MaxTotalChars: 100}
		},
		Logger: replierLogger(),
	})
	if err != nil {
		t.Fatalf("new replier: %v", err)
	}
	if err := r.ExecuteReply(context.Background(), groupTaskForAgent(), nil); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if len(ledger.reserved) != 1 {
		t.Fatalf("reserved = %d", len(ledger.reserved))
	}
	got := ledger.reserved[0].Limits
	if got.MaxMessages != 1 || got.MaxCharsPerMessage != 80 || got.MaxTotalChars != 100 {
		t.Fatalf("limits = %+v, want policy limits", got)
	}
}

func TestSendToolAttemptSuppressesFinalFallback(t *testing.T) {
	runtime := &fakeRuntime{text: "must not duplicate", toolRuns: []agentcontracts.ToolRun{{ToolID: "qq.send_voice_current", Status: "error"}}}
	sender := &fakeSender{}
	r := newReplier(t, runtime, sender, &fakeRecorder{}, enabledGroupPolicy())
	if err := r.ExecuteReply(context.Background(), groupTaskForAgent(), nil); err != nil {
		t.Fatal(err)
	}
	if len(sender.texts) != 0 {
		t.Fatal("tool attempt triggered final fallback")
	}
}

func TestSensitiveRunDropsAutomaticReply(t *testing.T) {
	runtime := &fakeRuntime{text: "cookies=secret", suppress: true}
	sender := &fakeSender{}
	ledger := &fakeLedger{}
	r := newReplierWithLedger(t, runtime, sender, &fakeRecorder{}, enabledGroupPolicy(), ledger)
	if err := r.ExecuteReply(context.Background(), groupTaskForAgent(), nil); err != nil {
		t.Fatal(err)
	}
	if len(sender.texts) != 0 {
		t.Fatalf("sensitive reply sent: %v", sender.texts)
	}
	if len(ledger.denied) != 1 || ledger.denied[0] != "output_rejected" {
		t.Fatalf("denial = %v", ledger.denied)
	}
}

func TestCrossTargetSendKeepsSourceReply(t *testing.T) {
	runtime := &fakeRuntime{text: "来源会话的回答", toolRuns: []agentcontracts.ToolRun{{ToolID: "qq.send_msg", Status: "ok"}}}
	sender := &fakeSender{}
	ledger := &fakeLedger{deliveredTargets: map[string]bool{}}
	r := newReplierWithLedger(t, runtime, sender, &fakeRecorder{}, enabledGroupPolicy(), ledger)
	if err := r.ExecuteReply(context.Background(), groupTaskForAgent(), nil); err != nil {
		t.Fatal(err)
	}
	if len(sender.texts) != 1 || sender.texts[0] != "来源会话的回答" {
		t.Fatalf("source reply suppressed by cross-target send: %v", sender.texts)
	}
}

func TestCrossTargetRefusalKeepsSourceReply(t *testing.T) {
	runtime := &fakeRuntime{text: "来源回答", toolRuns: []agentcontracts.ToolRun{{ToolID: "qq.send_msg", Status: "denied", ErrorCode: "authorization_revoked"}}}
	sender := &fakeSender{}
	ledger := &fakeLedger{}
	r := newReplierWithLedger(t, runtime, sender, &fakeRecorder{}, enabledGroupPolicy(), ledger)
	if err := r.ExecuteReply(context.Background(), groupTaskForAgent(), nil); err != nil {
		t.Fatal(err)
	}
	if len(sender.texts) != 1 || sender.texts[0] != "来源回答" {
		t.Fatalf("source reply lost after cross-target refusal: %v", sender.texts)
	}
	if len(ledger.denied) != 0 || len(ledger.refusals) != 1 {
		t.Fatalf("denied=%v refusals=%v", ledger.denied, ledger.refusals)
	}
}

func TestStandardSendToSourceSuppressesFinalReply(t *testing.T) {
	runtime := &fakeRuntime{text: "must not duplicate", toolRuns: []agentcontracts.ToolRun{{ToolID: "qq.send_msg", Status: "ok"}}}
	sender := &fakeSender{}
	ledger := &fakeLedger{deliveredTargets: map[string]bool{"group:700": true}}
	r := newReplierWithLedger(t, runtime, sender, &fakeRecorder{}, enabledGroupPolicy(), ledger)
	if err := r.ExecuteReply(context.Background(), groupTaskForAgent(), nil); err != nil {
		t.Fatal(err)
	}
	if len(sender.texts) != 0 {
		t.Fatalf("duplicate reply sent: %v", sender.texts)
	}
}

type definiteSendError struct{}

func (definiteSendError) Error() string         { return "platform action rejected" }
func (definiteSendError) DefiniteFailure() bool { return true }

func (f *fakeLedger) RecordDenied(_ context.Context, _ int64, reason string, claim ...string) error {
	f.denied = append(f.denied, reason)
	return nil
}

func TestLaterSendRefusalIsNotLostAfterEarlierSend(t *testing.T) {
	runtime := &fakeRuntime{text: "must not duplicate", toolRuns: []agentcontracts.ToolRun{{ToolID: "qq.send_voice_current", Status: "ok"}, {ToolID: "qq.send_voice_current", Status: "denied", ErrorCode: "tool_call_budget"}}}
	sender := &fakeSender{}
	ledger := &fakeLedger{}
	r := newReplierWithLedger(t, runtime, sender, &fakeRecorder{}, enabledGroupPolicy(), ledger)
	if err := r.ExecuteReply(context.Background(), groupTaskForAgent(), nil); err != nil {
		t.Fatal(err)
	}
	if len(ledger.denied) != 1 || ledger.denied[0] != "tool_call_budget" || len(sender.texts) != 0 {
		t.Fatalf("later refusal lost: %+v", ledger.denied)
	}
}

func TestGuardRefusalIsRecordedBeforeGenerationCompletes(t *testing.T) {
	ledger := &fakeLedger{}
	runtime := &fakeRuntime{text: "must not send final"}
	runtime.onRun = func(req agentcontracts.Request) {
		req.OnToolRun(agentcontracts.ToolRun{ToolID: "qq.send_voice_current", Status: "ok"})
		req.OnToolRun(agentcontracts.ToolRun{ToolID: "qq.send_voice_current", Status: "denied", ErrorCode: "authorization_revoked"})
		if len(ledger.denied) != 1 || ledger.denied[0] != "authorization_revoked" {
			t.Fatalf("refusal was deferred until run end: %+v", ledger.denied)
		}
	}
	sender := &fakeSender{}
	r := newReplierWithLedger(t, runtime, sender, &fakeRecorder{}, enabledGroupPolicy(), ledger)
	if err := r.ExecuteReply(context.Background(), groupTaskForAgent(), nil); err != nil {
		t.Fatal(err)
	}
	if len(ledger.denied) != 1 || len(sender.texts) != 0 {
		t.Fatalf("duplicate refusal or fallback: denied=%v sends=%v", ledger.denied, sender.texts)
	}
}

func TestReplierDoesNotWriteHistoryWhenSettlementFails(t *testing.T) {
	runtime := &fakeRuntime{text: "receipt without settlement"}
	sender := &fakeSender{msgID: 7788}
	recorder := &fakeRecorder{}
	ledger := &fakeLedger{settleErr: errors.New("injected commit failure")}
	replier := newReplierWithLedger(t, runtime, sender, recorder, enabledGroupPolicy(), ledger)
	if err := replier.ExecuteReply(context.Background(), groupTaskForAgent(), nil); err == nil {
		t.Fatal("failed settlement was reported as success")
	}
	if len(recorder.statuses) != 0 {
		t.Fatalf("history escaped failed settlement: %+v", recorder)
	}
	if len(sender.group) != 1 {
		t.Fatalf("platform calls=%d", len(sender.group))
	}
}

// TestReplierDropsHistoryAfterPermissionRevocation verifies stale intent history never reaches generation.
// TestReplierDropsHistoryAfterPermissionRevocation 验证撤销权限后意图阶段的旧历史不会进入生成。
func TestReplierDropsHistoryAfterPermissionRevocation(t *testing.T) {
	fake := &fakeRuntime{text: "ok"}
	policies := enabledGroupPolicy()
	p := policies[scopeKey(100, "group", 700)]
	p.AllowHistoryRead = false
	policies[scopeKey(100, "group", 700)] = p
	r := newReplier(t, fake, &fakeSender{}, &fakeRecorder{}, policies)
	if err := r.ExecuteReply(context.Background(), groupTaskForAgent(), []intentdomain.Message{{MessageID: 1, Text: "private history", Segments: []obdomain.Segment{obdomain.Image("https://example.com/private.png")}}}); err != nil {
		t.Fatal(err)
	}
	if len(fake.req.Messages) != 2 {
		t.Fatal("revoked history reached the model")
	}
}

func TestSendingGrantStopsAutomaticReply(t *testing.T) {
	for _, duringRun := range []bool{false, true} {
		t.Run(fmt.Sprint(duringRun), func(t *testing.T) {
			policies := enabledGroupPolicy()
			revoke := func() {
				p := policies["100:group:700"]
				p.ToolPermissions = map[string]bool{"qq.reply.current": false}
				policies["100:group:700"] = p
			}
			runtime := &fakeRuntime{text: "must not send"}
			if duringRun {
				runtime.onRun = func(agentcontracts.Request) { revoke() }
			} else {
				revoke()
			}
			sender := &fakeSender{}
			ledger := &fakeLedger{}
			replier := newReplierWithLedger(t, runtime, sender, &fakeRecorder{}, policies, ledger)
			if err := replier.ExecuteReply(context.Background(), groupTaskForAgent(), nil); err != nil {
				t.Fatal(err)
			}
			if len(sender.texts) != 0 || len(ledger.reserved) != 0 {
				t.Fatal("disabled sending produced delivery")
			}
		})
	}
}

// fakeMemoryReader returns canned approved memories and records the request.
// fakeMemoryReader 返回预置的已批准记忆并记录请求。
type fakeMemoryReader struct {
	items []memorycontracts.Memory
	err   error
	req   memorycontracts.RecallRequest
	calls int
}

func (f *fakeMemoryReader) Recall(_ context.Context, req memorycontracts.RecallRequest) ([]memorycontracts.Memory, error) {
	f.calls++
	f.req = req
	if f.err != nil {
		return nil, f.err
	}
	return f.items, nil
}

// TestReplierInjectsBoundedApprovedMemories verifies approved memory reaches the
// system prompt as data with identity/version metadata available to the run
// audit, while the entry cap prevents an unbounded prompt.
// TestReplierInjectsBoundedApprovedMemories 验证已批准记忆作为数据进入系统提示，
// 运行审计能定位命中标识与版本，同时条数上限阻止提示无界增长。
func TestReplierInjectsBoundedApprovedMemories(t *testing.T) {
	fake := &fakeRuntime{text: "好的"}
	memory := &fakeMemoryReader{items: []memorycontracts.Memory{
		{ID: "m1", Content: "群友喜欢晚上聊天", Visibility: "bot_shared", UpdatedAt: time.Date(2026, 11, 10, 8, 0, 0, 0, time.UTC)},
		{ID: "m2", Content: "项目后端使用 Go", Visibility: "group", UpdatedAt: time.Date(2026, 11, 10, 9, 0, 0, 0, time.UTC)},
		{ID: "m3", Content: "第三条记忆", Visibility: "group", UpdatedAt: time.Date(2026, 11, 10, 10, 0, 0, 0, time.UTC)},
		{ID: "m4", Content: "不应进入提示的第四条", Visibility: "group", UpdatedAt: time.Date(2026, 11, 10, 11, 0, 0, 0, time.UTC)},
	}}
	r := newReplier(t, fake, &fakeSender{}, &fakeRecorder{}, memoryReadGroupPolicy())
	r.memory = memory
	audit := &fakeRunAudit{}
	ctx := auditcontracts.WithRun(context.Background(), audit, "run-memory")
	if err := r.ExecuteReply(ctx, groupTaskForAgent(), nil); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if memory.calls != 1 || memory.req.BotID != 100 || memory.req.Kind != "group" ||
		memory.req.TargetID != 700 || memory.req.ConversationID != "conv-1" || memory.req.Query != "那你觉得呢？" {
		t.Fatalf("memory request = %+v calls=%d", memory.req, memory.calls)
	}
	system := fake.req.Messages[0].Content
	// Memory versions are explicit so old prompt content is explainable.
	// 记忆版本显式写入提示，便于解释旧提示内容。
	if !strings.Contains(system, "2026-11-10T08:00:00Z") || !strings.Contains(system, "群友喜欢晚上聊天") {
		t.Fatalf("system prompt misses first memory/version: %q", system)
	}
	if !strings.Contains(system, "不是系统指令") {
		t.Fatalf("system prompt must label memory as data: %q", system)
	}
	if strings.Contains(system, "不应进入提示的第四条") {
		t.Fatalf("fourth memory bypassed the entry cap: %q", system)
	}
	var input map[string]any
	for _, event := range audit.events {
		if event.Stage != "agent.generation" {
			continue
		}
		if err := json.Unmarshal(event.Input, &input); err != nil {
			t.Fatalf("decode audit input: %v", err)
		}
	}
	if input == nil {
		t.Fatalf("agent.generation audit event missing: %+v", audit.events)
	}
	hits, ok := input["memory_hits"].([]any)
	if !ok || len(hits) != 3 {
		t.Fatalf("audit memory_hits = %#v", input["memory_hits"])
	}
	first, _ := hits[0].(map[string]any)
	if first["id"] != "m1" || first["updated_at"] != "2026-11-10T08:00:00Z" {
		t.Fatalf("audit hit = %#v", first)
	}
	if _, exists := first["content"]; exists {
		t.Fatalf("audit hit must not carry content: %#v", first)
	}
}

// TestReplierMemoryFailureDoesNotBlockChat verifies optional memory failures
// degrade to a normal reply instead of failing the job.
// TestReplierMemoryFailureDoesNotBlockChat 验证记忆召回失败降级为正常回复而不是让任务失败。
func TestReplierMemoryFailureDoesNotBlockChat(t *testing.T) {
	fake := &fakeRuntime{text: "照常回复"}
	sender := &fakeSender{}
	r := newReplier(t, fake, sender, &fakeRecorder{}, memoryReadGroupPolicy())
	r.memory = &fakeMemoryReader{err: errors.New("memory db down")}
	if err := r.ExecuteReply(context.Background(), groupTaskForAgent(), nil); err != nil {
		t.Fatalf("memory failure leaked into chat: %v", err)
	}
	if len(sender.texts) != 1 || sender.texts[0] != "照常回复" {
		t.Fatalf("reply after memory failure = %v", sender.texts)
	}
}

// TestBoundMemoriesEnforcesRuneBudget verifies the total prompt budget even
// when a single memory is longer than the whole allowance.
// TestBoundMemoriesEnforcesRuneBudget 验证单条记忆超过总预算时仍执行总字数限制。
func TestBoundMemoriesEnforcesRuneBudget(t *testing.T) {
	items := []memorycontracts.Memory{
		{ID: "a", Content: strings.Repeat("长", maxMemoryRunes+100)},
		{ID: "b", Content: "不应进入"},
	}
	got := boundMemories(items)
	if len(got) != 1 {
		t.Fatalf("bounded memories = %+v", got)
	}
	if runes := len([]rune(got[0].Content)); runes > maxMemoryRunes {
		t.Fatalf("content runes = %d, want <= %d", runes, maxMemoryRunes)
	}
	if !strings.HasSuffix(got[0].Content, "…") {
		t.Fatalf("truncated memory must end with ellipsis: %q", got[0].Content)
	}
}

// TestReplierRecordsMemoryHitsOnRunSpan verifies the observable run record can
// locate the recalled memory id and version, without exporting the memory body
// as metadata.
// TestReplierRecordsMemoryHitsOnRunSpan 验证可观测运行记录能定位召回记忆的标识与版本，
// 且元数据不包含记忆正文。
func TestReplierRecordsMemoryHitsOnRunSpan(t *testing.T) {
	recorder := tracetest.NewSpanRecorder()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
	previous := otel.GetTracerProvider()
	otel.SetTracerProvider(provider)
	t.Cleanup(func() {
		otel.SetTracerProvider(previous)
		_ = provider.Shutdown(context.Background())
	})

	r := newReplier(t, &fakeRuntime{text: "好的"}, &fakeSender{msgID: 1}, &fakeRecorder{}, memoryReadGroupPolicy())
	r.memory = &fakeMemoryReader{items: []memorycontracts.Memory{
		{ID: "m-hit", Content: "不应出现在元数据", Visibility: "group", UpdatedAt: time.Date(2026, 11, 10, 7, 30, 0, 0, time.UTC)},
	}}
	if err := r.ExecuteReply(context.Background(), groupTaskForAgent(), nil); err != nil {
		t.Fatalf("execute: %v", err)
	}
	hits := ""
	for _, span := range recorder.Ended() {
		if span.Name() != "nagi.agent.run" {
			continue
		}
		for _, kv := range span.Attributes() {
			if string(kv.Key) == "nagi.memory.hits" {
				hits = kv.Value.Emit()
			}
		}
	}
	if !strings.Contains(hits, "m-hit") || !strings.Contains(hits, "2026-11-10T07:30:00Z") {
		t.Fatalf("memory hits attribute = %q", hits)
	}
	if strings.Contains(hits, "不应出现在元数据") {
		t.Fatalf("memory body leaked into span metadata: %q", hits)
	}
}

// TestReplierMemoryReadPermissionCombinations verifies memory recall and chat
// history are authorized independently: only allow_memory_read controls the
// former and only allow_history_read controls the latter.
// TestReplierMemoryReadPermissionCombinations 验证长期记忆与聊天历史独立授权：
// 只有 allow_memory_read 控制记忆注入，只有 allow_history_read 控制历史进入提示。
func TestReplierMemoryReadPermissionCombinations(t *testing.T) {
	cases := []struct {
		name        string
		memoryRead  bool
		historyRead bool
		wantMemory  bool
		wantHistory bool
	}{
		{name: "memory_on_history_on", memoryRead: true, historyRead: true, wantMemory: true, wantHistory: true},
		{name: "memory_on_history_off", memoryRead: true, historyRead: false, wantMemory: true, wantHistory: false},
		{name: "memory_off_history_on", memoryRead: false, historyRead: true, wantMemory: false, wantHistory: true},
		{name: "memory_off_history_off", memoryRead: false, historyRead: false, wantMemory: false, wantHistory: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			policies := enabledGroupPolicy()
			key := scopeKey(100, "group", 700)
			p := policies[key]
			p.AllowMemoryRead = tc.memoryRead
			p.AllowHistoryRead = tc.historyRead
			policies[key] = p
			fake := &fakeRuntime{text: "好的"}
			memory := &fakeMemoryReader{items: []memorycontracts.Memory{{ID: "m1", Content: "长期记忆哨兵", Visibility: "group"}}}
			r := newReplier(t, fake, &fakeSender{}, &fakeRecorder{}, policies)
			r.memory = memory
			history := []intentdomain.Message{{MessageID: 5000, SenderID: 200, Text: "历史原文哨兵"}}
			if err := r.ExecuteReply(context.Background(), groupTaskForAgent(), history); err != nil {
				t.Fatalf("execute: %v", err)
			}
			system := fake.req.Messages[0].Content
			if got := strings.Contains(system, "长期记忆哨兵"); got != tc.wantMemory {
				t.Fatalf("memory injected=%v, want %v; prompt=%q", got, tc.wantMemory, system)
			}
			historySeen := false
			for _, message := range fake.req.Messages[1:] {
				if strings.Contains(message.Content, "历史原文哨兵") {
					historySeen = true
				}
			}
			if historySeen != tc.wantHistory {
				t.Fatalf("history injected=%v, want %v; messages=%+v", historySeen, tc.wantHistory, fake.req.Messages)
			}
			wantCalls := 0
			if tc.memoryRead {
				wantCalls = 1
			}
			if memory.calls != wantCalls {
				t.Fatalf("memory calls=%d, want %d", memory.calls, wantCalls)
			}
		})
	}
}

// TestReplierMemoryRecallRevocationStopsNextInjection verifies a revoked
// allow_memory_read grant stops the next run from loading or injecting memory.
// TestReplierMemoryRecallRevocationStopsNextInjection 验证撤销 allow_memory_read 后，
// 下一次运行不再加载或注入长期记忆。
func TestReplierMemoryRecallRevocationStopsNextInjection(t *testing.T) {
	policies := memoryReadGroupPolicy()
	key := scopeKey(100, "group", 700)
	memory := &fakeMemoryReader{items: []memorycontracts.Memory{{ID: "m1", Content: "不应再次注入", Visibility: "group"}}}
	r := newReplier(t, &fakeRuntime{text: "好的"}, &fakeSender{}, &fakeRecorder{}, policies)
	r.memory = memory
	if err := r.ExecuteReply(context.Background(), groupTaskForAgent(), nil); err != nil {
		t.Fatalf("first execute: %v", err)
	}
	if memory.calls != 1 {
		t.Fatalf("first run memory calls = %d", memory.calls)
	}
	p := policies[key]
	p.AllowMemoryRead = false
	policies[key] = p
	fake2 := &fakeRuntime{text: "好的"}
	r2 := newReplier(t, fake2, &fakeSender{}, &fakeRecorder{}, policies)
	r2.memory = memory
	if err := r2.ExecuteReply(context.Background(), groupTaskForAgent(), nil); err != nil {
		t.Fatalf("second execute: %v", err)
	}
	if memory.calls != 1 {
		t.Fatalf("revoked memory was still read: calls=%d", memory.calls)
	}
	if strings.Contains(fake2.req.Messages[0].Content, "不应再次注入") {
		t.Fatalf("revoked memory was injected: %q", fake2.req.Messages[0].Content)
	}
}

// TestReplierRendersExactMemorySource verifies a stored source is shown only
// when present and is also included in the audit hit metadata.
// TestReplierRendersExactMemorySource 验证仅在已存来源存在时展示来源，
// 并将来源写入审计命中元数据。
func TestReplierRendersExactMemorySource(t *testing.T) {
	fake := &fakeRuntime{text: "好的"}
	memory := &fakeMemoryReader{items: []memorycontracts.Memory{{
		ID: "m-source", Content: "项目部署在 Docker", Visibility: "group",
		Source: "群文件", SourceMessageID: 42,
		UpdatedAt: time.Date(2026, 11, 10, 8, 0, 0, 0, time.UTC),
	}}}
	r := newReplier(t, fake, &fakeSender{}, &fakeRecorder{}, memoryReadGroupPolicy())
	r.memory = memory
	audit := &fakeRunAudit{}
	if err := r.ExecuteReply(auditcontracts.WithRun(context.Background(), audit, "run-source"), groupTaskForAgent(), nil); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if system := fake.req.Messages[0].Content; !strings.Contains(system, "来源：消息#42，群文件") {
		t.Fatalf("source missing from prompt: %q", system)
	}
	var input map[string]any
	for _, event := range audit.events {
		if event.Stage == "agent.generation" {
			if err := json.Unmarshal(event.Input, &input); err != nil {
				t.Fatalf("decode audit input: %v", err)
			}
		}
	}
	hits, ok := input["memory_hits"].([]any)
	if !ok || len(hits) != 1 {
		t.Fatalf("memory_hits = %#v", input["memory_hits"])
	}
	hit, _ := hits[0].(map[string]any)
	if hit["source"] != "群文件" || hit["source_message_id"] != float64(42) {
		t.Fatalf("hit source = %#v", hit)
	}
}
