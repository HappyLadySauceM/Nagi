// Tests for the qq.send_current tool.
// qq.send_current 工具的测试。
package replytool

import (
	"context"
	"errors"
	"strings"
	"testing"

	obcontracts "github.com/HappyLadySauceM/Nagi/internal/onebot/contracts"
	obdomain "github.com/HappyLadySauceM/Nagi/internal/onebot/domain"
	outboundapp "github.com/HappyLadySauceM/Nagi/internal/outbound/application"
	outbounddomain "github.com/HappyLadySauceM/Nagi/internal/outbound/domain"
	pluginsapp "github.com/HappyLadySauceM/Nagi/internal/plugins/application"
	"github.com/HappyLadySauceM/Nagi/internal/plugins/contracts"
)

type fakeSender struct {
	groupID  int64
	private  int64
	text     string
	message  int64
	err      error
	groupCal int
}

func (f *fakeSender) SendPrivateMessageReceipt(_ context.Context, to obdomain.UserID, segs ...obdomain.Segment) (int64, error) {
	f.private = int64(to)
	f.text = segmentsText(segs)
	return f.message, f.err
}

func (f *fakeSender) SendGroupMessageReceipt(_ context.Context, to obdomain.GroupID, segs ...obdomain.Segment) (int64, error) {
	f.groupCal++
	f.groupID = int64(to)
	f.text = segmentsText(segs)
	return f.message, f.err
}

func segmentsText(segs []obdomain.Segment) string {
	var b strings.Builder
	for _, seg := range segs {
		b.WriteString(seg.Data["text"])
	}
	return b.String()
}

func (f *fakeSender) ReceiptSender(int64) (Sender, bool) { return f, true }

type fakeLedger struct {
	denied   []string
	denyErr  error
	reserved []outboundapp.Request
	settled  []outboundapp.Outcome
	nextSeq  int
	err      error
}

func (f *fakeLedger) Reserve(_ context.Context, req outboundapp.Request) (outbounddomain.Record, error) {
	if f.err != nil {
		return outbounddomain.Record{}, f.err
	}
	f.reserved = append(f.reserved, req)
	f.nextSeq++
	return outbounddomain.Record{ID: "rec", Sequence: f.nextSeq, Status: outbounddomain.StatusPrepared}, nil
}

func (f *fakeLedger) MarkSending(_ context.Context, _ string, _ ...string) error { return nil }

func (f *fakeLedger) Settle(_ context.Context, _ string, outcome outboundapp.Outcome, claim ...string) error {
	f.settled = append(f.settled, outcome)
	return nil
}

type fakeRecorder struct {
	calls int
	text  string
}

func (f *fakeRecorder) RecordOutgoing(_ context.Context, _ string, _ int64, text string, _ int64, _ string, _ string, claim ...string) error {
	f.calls++
	f.text = text
	return nil
}

func newTool(t *testing.T, sender *fakeSender, ledger *fakeLedger, recorder *fakeRecorder) contracts.RegisteredTool {
	t.Helper()
	if sender.err == nil && sender.message == 0 {
		sender.message = 1
	}
	tools, err := NewProvider(Options{
		Senders:  sender,
		Ledger:   ledger,
		Recorder: recorder,
		Limits:   outbounddomain.Limits{MaxMessages: 3, MaxCharsPerMessage: 100, MaxTotalChars: 300},
	}).Tools(context.Background())
	if err != nil {
		t.Fatalf("tools: %v", err)
	}
	if len(tools) != 1 {
		t.Fatalf("tools = %d", len(tools))
	}
	return tools[0]
}

func groupContext() context.Context {
	return pluginsapp.WithScope(context.Background(), pluginsapp.Scope{
		BotID: 1, JobID: 9, ConversationID: "conv-1", Kind: contracts.ScopeKindGroup, TargetID: 700,
	})
}

func TestSendCurrentReservesSettlesAndRecords(t *testing.T) {
	sender := &fakeSender{message: 4242}
	ledger := &fakeLedger{}
	recorder := &fakeRecorder{}
	tool := newTool(t, sender, ledger, recorder)

	out, err := tool.Invoke(groupContext(), `{"text":"  你好呀  "}`)
	if err != nil {
		t.Fatalf("invoke: %v", err)
	}
	if !strings.Contains(out, `"status":"sent"`) || !strings.Contains(out, `"platform_message_id":4242`) {
		t.Fatalf("receipt = %s", out)
	}
	if len(ledger.reserved) != 1 || ledger.reserved[0].JobID != 9 || ledger.reserved[0].ConversationID != "conv-1" {
		t.Fatalf("reserved = %+v", ledger.reserved)
	}
	if ledger.reserved[0].Text != "你好呀" {
		t.Fatalf("text not sanitized: %q", ledger.reserved[0].Text)
	}
	if sender.groupID != 700 || sender.text != "你好呀" {
		t.Fatalf("sent to %d text %q", sender.groupID, sender.text)
	}
	if len(ledger.settled) != 1 || ledger.settled[0].Status != outbounddomain.StatusSent {
		t.Fatalf("settled = %+v", ledger.settled)
	}
	if recorder.calls != 1 {
		t.Fatalf("record calls = %d", recorder.calls)
	}
}

func TestSendCurrentRejectsBudgetWithoutSending(t *testing.T) {
	sender := &fakeSender{message: 1}
	ledger := &fakeLedger{err: &outbounddomain.BudgetError{Dimension: outbounddomain.DimensionMessages, Limit: 3, Requested: 4}}
	tool := newTool(t, sender, ledger, &fakeRecorder{})

	out, err := tool.Invoke(groupContext(), `{"text":"再发一条"}`)
	if !errors.Is(err, outbounddomain.ErrBudgetExceeded) {
		t.Fatalf("expected budget rejection: %v", err)
	}
	if !strings.Contains(out, "预算已用尽") {
		t.Fatalf("out = %s", out)
	}
	if sender.groupCal != 0 {
		t.Fatalf("sender must not be called when the budget is denied")
	}
}

func TestSendCurrentReportsTimeoutAsUnknown(t *testing.T) {
	sender := &fakeSender{err: obcontracts.ErrActionTimeout}
	ledger := &fakeLedger{}
	recorder := &fakeRecorder{}
	tool := newTool(t, sender, ledger, recorder)

	out, err := tool.Invoke(groupContext(), `{"text":"可能已送达"}`)
	if !errors.Is(err, obcontracts.ErrActionTimeout) {
		t.Fatalf("expected timeout: %v", err)
	}
	if !strings.Contains(out, `"status":"unknown"`) {
		t.Fatalf("out = %s", out)
	}
	if len(ledger.settled) != 1 || ledger.settled[0].Status != outbounddomain.StatusUnknown {
		t.Fatalf("settled = %+v", ledger.settled)
	}
	// The delivery may have happened, so the outgoing record still exists.
	if recorder.calls != 1 {
		t.Fatalf("record calls = %d", recorder.calls)
	}
}

func TestSendCurrentRejectsLinks(t *testing.T) {
	sender := &fakeSender{}
	ledger := &fakeLedger{}
	tool := newTool(t, sender, ledger, &fakeRecorder{})

	out, err := tool.Invoke(groupContext(), `{"text":"看 https://example.com"}`)
	if err == nil {
		t.Fatal("expected output rejection")
	}
	if out != "" {
		t.Fatalf("out = %s", out)
	}
	if len(ledger.reserved) != 0 || sender.groupCal != 0 {
		t.Fatalf("sanitize failure must not reserve or send")
	}
}

func TestSendCurrentFailsWithoutScope(t *testing.T) {
	tool := newTool(t, &fakeSender{}, &fakeLedger{}, &fakeRecorder{})
	if _, err := tool.Invoke(context.Background(), `{"text":"hi"}`); err == nil {
		t.Fatalf("expected an error without a run scope")
	}
}

func TestSendCurrentPropagatesLedgerErrors(t *testing.T) {
	ledger := &fakeLedger{err: errors.New("db down")}
	tool := newTool(t, &fakeSender{}, ledger, &fakeRecorder{})
	if _, err := tool.Invoke(groupContext(), `{"text":"hi"}`); err == nil {
		t.Fatalf("expected a ledger error")
	}
}

func (f *fakeLedger) RecordDenied(_ context.Context, _ int64, reason string, claim ...string) error {
	f.denied = append(f.denied, reason)
	return f.denyErr
}

func TestInvalidArgumentsStopSendingBeforeReservation(t *testing.T) {
	sender := &fakeSender{}
	ledger := &fakeLedger{}
	tool := newTool(t, sender, ledger, &fakeRecorder{})
	if _, err := tool.Invoke(groupContext(), "{invalid"); err == nil {
		t.Fatal("invalid arguments must fail")
	}
	if len(ledger.denied) != 1 || ledger.denied[0] != "invalid_arguments" || len(ledger.reserved) != 0 || sender.groupCal != 0 {
		t.Fatalf("invalid arguments were not recorded: %+v", ledger)
	}
}

func TestDenialPersistenceErrorIsPropagated(t *testing.T) {
	ledger := &fakeLedger{denyErr: errors.New("denial persistence failed")}
	tool := newTool(t, &fakeSender{}, ledger, &fakeRecorder{})
	if _, err := tool.Invoke(groupContext(), "{invalid"); !errors.Is(err, ledger.denyErr) {
		t.Fatalf("denial persistence failure lost: %v", err)
	}
}

func TestPublicSendRequiresSpecificAndCrossConversationGrants(t *testing.T) {
	for _, tc := range []struct {
		name                            string
		specific, cross, targetDisabled bool
		revoke                          string
		wantCalls, wantReservations     int
	}{
		{name: "cross alone", cross: true},
		{name: "specific alone", specific: true},
		{name: "both grants", specific: true, cross: true, wantCalls: 1, wantReservations: 1},
		{name: "disabled target", specific: true, cross: true, targetDisabled: true},
		{name: "specific revoked", specific: true, cross: true, revoke: "qq.api.send_private_msg", wantReservations: 1},
		{name: "cross revoked", specific: true, cross: true, revoke: "qq.cross_conversation", wantReservations: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ledger := &fakeLedger{}
			checks := make(map[string]int)
			scope := pluginsapp.Scope{BotID: 1, JobID: 9, ConversationID: "conv-1", Kind: "group", TargetID: 700,
				Permissions: map[string]bool{"qq.api.send_private_msg": tc.specific, "qq.cross_conversation": tc.cross},
				Reauthorize: func(_ context.Context, d contracts.ToolDescriptor) error {
					checks[d.Permission]++
					if d.Permission == tc.revoke && checks[d.Permission] == 2 {
						return errors.New("revoked")
					}
					return nil
				},
			}
			calls := 0
			text := "hello"
			_, err := SendPublic(pluginsapp.WithScope(context.Background(), scope), Options{
				Ledger: ledger, Limits: outbounddomain.Limits{MaxMessages: 3},
				AuthorizeTarget: func(context.Context, string, int64) error {
					if tc.targetDisabled {
						return errors.New("target disabled")
					}
					return nil
				},
			}, obdomain.PublicCommand{Name: obdomain.ActionSendPrivate, UserID: 22, Text: &text}, func(context.Context, obdomain.PublicCommand) (obdomain.PublicResult, error) {
				calls++
				return obdomain.PublicResult{MessageID: 1}, nil
			})
			if calls != tc.wantCalls || len(ledger.reserved) != tc.wantReservations || (err == nil) != (tc.wantCalls == 1) {
				t.Fatalf("calls=%d reservations=%d err=%v", calls, len(ledger.reserved), err)
			}
		})
	}
}
