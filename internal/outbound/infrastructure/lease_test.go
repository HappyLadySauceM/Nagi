package infrastructure

import (
	"context"
	"errors"
	conversationinfra "github.com/HappyLadySauceM/Nagi/internal/conversation/infrastructure"
	"github.com/HappyLadySauceM/Nagi/internal/outbound/application"
	"github.com/HappyLadySauceM/Nagi/internal/outbound/domain"
	"testing"
)

func TestExpiredSendingBecomesUnknownAndRejectsLateReceipt(t *testing.T) {
	f := newLedgerFixture(t)
	ctx := context.Background()
	record := f.reserve(t, "uncertain", domain.Limits{MaxMessages: 3})
	if err := f.repo.MarkSending(ctx, record.ID, f.claim); err != nil {
		t.Fatal(err)
	}
	if _, err := f.pool.Exec(ctx, "UPDATE message_jobs SET lease_expires_at=clock_timestamp()-interval '1 second' WHERE id=$1", f.jobID); err != nil {
		t.Fatal(err)
	}
	conversations, _ := conversationinfra.NewRepo(f.pool)
	if err := conversations.RecoverStaleJobs(ctx); err != nil {
		t.Fatal(err)
	}
	if err := f.repo.Settle(ctx, record.ID, application.Outcome{Status: domain.StatusSent, PlatformMessageID: 42}, f.claim); !errors.Is(err, ErrLeaseLost) {
		t.Fatalf("late settlement accepted: %v", err)
	}
	calls := 0
	_, err := application.Deliver(ctx, f.repo, record.ID, func(context.Context) (int64, error) { calls++; return 99, nil }, f.claim)
	if err == nil || calls != 0 {
		t.Fatalf("stale sender called platform: calls=%d err=%v", calls, err)
	}
	var status string
	if err := f.pool.QueryRow(ctx, "SELECT status FROM agent_outbound_messages WHERE id=$1", record.ID).Scan(&status); err != nil || status != "unknown" {
		t.Fatalf("status=%s err=%v", status, err)
	}
}

func TestSettlementAndHistoryRollbackTogether(t *testing.T) {
	f := newLedgerFixture(t)
	ctx := context.Background()
	var target int64
	if err := f.pool.QueryRow(ctx, "SELECT target_id FROM conversations WHERE id=$1", f.conversationID).Scan(&target); err != nil {
		t.Fatal(err)
	}
	if err := f.pool.Client.ScopePolicy.Create().SetBotID(f.botID).SetScopeType("group").SetScopeID(target).SetEnabled(true).SetStoreMessages(true).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = f.pool.Exec(ctx, "DELETE FROM scope_policies WHERE bot_id=$1 AND scope_id=$2", f.botID, target)
	})
	record := f.reserve(t, "atomic history", domain.Limits{MaxMessages: 3})
	if err := f.repo.MarkSending(ctx, record.ID, f.claim); err != nil {
		t.Fatal(err)
	}
	_, err := f.pool.Exec(ctx, `CREATE FUNCTION nagi_test_reject_history() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.direction='outgoing' THEN RAISE EXCEPTION 'injected history failure'; END IF; RETURN NEW; END $$; CREATE TRIGGER nagi_test_reject_history BEFORE INSERT ON messages FOR EACH ROW EXECUTE FUNCTION nagi_test_reject_history()`)
	if err != nil {
		t.Fatal(err)
	}
	drop := func() {
		if _, err := f.pool.Exec(ctx, "DROP TRIGGER IF EXISTS nagi_test_reject_history ON messages; DROP FUNCTION IF EXISTS nagi_test_reject_history()"); err != nil {
			t.Error(err)
		}
	}
	t.Cleanup(drop)
	outcome := application.Outcome{Status: domain.StatusSent, PlatformMessageID: 2468}
	if err := f.repo.Settle(ctx, record.ID, outcome, f.claim); err == nil {
		t.Fatal("injected history failure was ignored")
	}
	var status string
	var count int
	if err := f.pool.QueryRow(ctx, "SELECT status FROM agent_outbound_messages WHERE id=$1", record.ID).Scan(&status); err != nil || status != "sending" {
		t.Fatalf("settlement escaped rollback: %s %v", status, err)
	}
	if err := f.pool.QueryRow(ctx, "SELECT count(*) FROM messages WHERE outbound_id=$1", record.ID).Scan(&count); err != nil || count != 0 {
		t.Fatalf("partial history: %d %v", count, err)
	}
	drop()
	if err := f.repo.Settle(ctx, record.ID, outcome, f.claim); err != nil {
		t.Fatal(err)
	}
	if err := f.pool.QueryRow(ctx, "SELECT count(*) FROM messages WHERE outbound_id=$1 AND sent_status='sent'", record.ID).Scan(&count); err != nil || count != 1 {
		t.Fatalf("atomic history missing: %d %v", count, err)
	}
}

func TestDeliveryListAcceptsMissingToolCallID(t *testing.T) {
	f := newLedgerFixture(t)
	record := f.reserve(t, "no tool call", domain.Limits{MaxMessages: 1})
	rows, err := f.repo.ListByJob(context.Background(), f.jobID)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].ID != record.ID || rows[0].ToolCallID != "" {
		t.Fatalf("nullable tool call list=%+v", rows)
	}
}

func TestJobFailureClosesUncertainDeliveryAndRefundsPrepared(t *testing.T) {
	f := newLedgerFixture(t)
	ctx := context.Background()
	var target int64
	if err := f.pool.QueryRow(ctx, "SELECT target_id FROM conversations WHERE id=$1", f.conversationID).Scan(&target); err != nil {
		t.Fatal(err)
	}
	if err := f.pool.Client.ScopePolicy.Create().SetBotID(f.botID).SetScopeType("group").SetScopeID(target).SetEnabled(true).SetStoreMessages(true).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = f.pool.Exec(ctx, "DELETE FROM scope_policies WHERE bot_id=$1 AND scope_id=$2", f.botID, target)
	})
	sent := f.reserve(t, "possibly delivered", domain.Limits{MaxMessages: 3})
	pending := f.reserve(t, "not yet sent", domain.Limits{MaxMessages: 3})
	_, err := f.pool.Exec(ctx, `CREATE FUNCTION nagi_test_fail_settlement() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.direction='outgoing' THEN RAISE EXCEPTION 'injected settlement failure'; END IF; RETURN NEW; END $$; CREATE TRIGGER nagi_test_fail_settlement BEFORE INSERT ON messages FOR EACH ROW EXECUTE FUNCTION nagi_test_fail_settlement()`)
	if err != nil {
		t.Fatal(err)
	}
	drop := func() {
		if _, err := f.pool.Exec(ctx, "DROP TRIGGER IF EXISTS nagi_test_fail_settlement ON messages; DROP FUNCTION IF EXISTS nagi_test_fail_settlement()"); err != nil {
			t.Error(err)
		}
	}
	t.Cleanup(drop)
	calls := 0
	outcome, err := application.Deliver(ctx, f.repo, sent.ID, func(context.Context) (int64, error) { calls++; return 112233, nil }, f.claim)
	if err == nil || outcome.Status != "" || calls != 1 {
		t.Fatalf("failed settlement reported durable outcome: %+v %v %d", outcome, err, calls)
	}
	drop()
	conversations, _ := conversationinfra.NewRepo(f.pool)
	if err := conversations.FailJob(ctx, f.jobID, "process_error", 0, 5, f.claim); err != nil {
		t.Fatal(err)
	}
	var status, jobState string
	var live bool
	var budget, histories int
	if err := f.pool.QueryRow(ctx, "SELECT status FROM agent_outbound_messages WHERE id=$1", sent.ID).Scan(&status); err != nil || status != "unknown" {
		t.Fatalf("uncertain send stranded: %s %v", status, err)
	}
	if err := f.pool.QueryRow(ctx, "SELECT status FROM agent_outbound_messages WHERE id=$1", pending.ID).Scan(&status); err != nil || status != "cancelled" {
		t.Fatalf("reservation stranded: %s %v", status, err)
	}
	if err := f.pool.QueryRow(ctx, "SELECT status,claim_token IS NOT NULL FROM message_jobs WHERE id=$1", f.jobID).Scan(&jobState, &live); err != nil || jobState != "failed" || live {
		t.Fatalf("job state=%s live=%v err=%v", jobState, live, err)
	}
	if err := f.pool.QueryRow(ctx, "SELECT reserved_messages FROM agent_run_budgets WHERE job_id=$1", f.jobID).Scan(&budget); err != nil || budget != 1 {
		t.Fatalf("budget=%d err=%v", budget, err)
	}
	if err := f.pool.QueryRow(ctx, "SELECT count(*) FROM messages WHERE outbound_id=$1 AND sent_status='unknown'", sent.ID).Scan(&histories); err != nil || histories != 1 {
		t.Fatalf("uncertain history=%d err=%v", histories, err)
	}
	if err := conversations.RecoverStaleJobs(ctx); err != nil {
		t.Fatal(err)
	}
	if err := f.repo.Settle(ctx, sent.ID, application.Outcome{Status: domain.StatusSent, PlatformMessageID: 112233}, f.claim); !errors.Is(err, ErrLeaseLost) {
		t.Fatalf("late receipt overwrote finalized unknown: %v", err)
	}
}
