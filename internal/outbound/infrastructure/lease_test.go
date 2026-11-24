package infrastructure

import (
	"context"
	"errors"
	conversationinfra "github.com/HappyLadySauceM/Nagi/internal/conversation/infrastructure"
	"github.com/HappyLadySauceM/Nagi/internal/outbound/application"
	"github.com/HappyLadySauceM/Nagi/internal/outbound/domain"
	scopedomain "github.com/HappyLadySauceM/Nagi/internal/scope/domain"
	scopeinfra "github.com/HappyLadySauceM/Nagi/internal/scope/infrastructure"
	"testing"
	"time"
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

func TestJobFailurePreservesKnownReceiptAndRefundsPrepared(t *testing.T) {
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
	var receipt int64
	if err := f.pool.QueryRow(ctx, "SELECT status,platform_message_id FROM agent_outbound_messages WHERE id=$1", sent.ID).Scan(&status, &receipt); err != nil || status != "sent" || receipt != 112233 {
		t.Fatalf("known receipt stranded: %s %v", status, err)
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
	if err := f.pool.QueryRow(ctx, "SELECT count(*) FROM messages WHERE outbound_id=$1 AND sent_status='sent' AND platform_message_id=112233", sent.ID).Scan(&histories); err != nil || histories != 1 {
		t.Fatalf("acknowledged history=%d err=%v", histories, err)
	}
	if err := conversations.RecoverStaleJobs(ctx); err != nil {
		t.Fatal(err)
	}
	if err := f.repo.Settle(ctx, sent.ID, application.Outcome{Status: domain.StatusSent, PlatformMessageID: 112233}, f.claim); err != nil {
		t.Fatalf("same receipt did not reconcile idempotently: %v", err)
	}
}

func TestDeliveryScopeLockSerializesTargetTemplateFallback(t *testing.T) {
	f := newLedgerFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	scopes, err := scopeinfra.NewRepo(f.pool)
	if err != nil {
		t.Fatal(err)
	}
	target := f.botID + 31781
	template := scopedomain.Policy{ScopeType: scopedomain.ScopePrivate, Enabled: true, StoreMessages: true}
	if err := scopes.Upsert(ctx, f.botID, template); err != nil {
		t.Fatal(err)
	}
	independent := template
	independent.ScopeID, independent.Enabled = target, false
	if err := scopes.Upsert(ctx, f.botID, independent); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = f.pool.Exec(context.Background(), "DELETE FROM scope_policies WHERE bot_id=$1 AND scope_type='private' AND scope_id IN (0,$2)", f.botID, target)
	})
	record, err := f.repo.Reserve(ctx, domain.ReserveInput{
		ClaimToken: f.claim, JobID: f.jobID, ConversationID: f.conversationID, BotID: f.botID,
		Text: "scope lock", TargetKind: "private", TargetID: target,
	})
	if err != nil {
		t.Fatal(err)
	}
	tx, err := f.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if err := f.repo.lockScopedDelivery(ctx, tx, record.ID, f.claim); err != nil {
		t.Fatal(err)
	}
	var blocker int
	if err := tx.QueryRow(ctx, "SELECT pg_backend_pid()").Scan(&blocker); err != nil {
		t.Fatal(err)
	}
	deleted := make(chan error, 1)
	go func() {
		ok, err := scopes.Delete(ctx, f.botID, scopedomain.ScopePrivate, target)
		if err == nil && !ok {
			err = errors.New("target override not deleted")
		}
		deleted <- err
	}()
	ticker := time.NewTicker(5 * time.Millisecond)
	defer ticker.Stop()
	for {
		var waiting bool
		if err := f.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_stat_activity
 WHERE $1=ANY(pg_blocking_pids(pid)) AND wait_event='advisory'
 AND query LIKE 'SELECT pg_advisory_xact_lock(%')`, blocker).Scan(&waiting); err != nil {
			t.Fatal(err)
		}
		if waiting {
			break
		}
		select {
		case err := <-deleted:
			t.Fatalf("delete escaped shared target lock: %v", err)
		case <-ticker.C:
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if err := <-deleted; err != nil {
		t.Fatal(err)
	}
	if err := f.repo.MarkSending(ctx, record.ID, f.claim); err != nil {
		t.Fatal(err)
	}
	if err := f.repo.Settle(ctx, record.ID, application.Outcome{Status: domain.StatusSent, PlatformMessageID: -41}, f.claim); err != nil {
		t.Fatal(err)
	}
	var history int
	if err := f.pool.QueryRow(ctx, `SELECT count(*) FROM messages m JOIN conversations c ON c.id=m.conversation_id
 WHERE m.outbound_id=$1 AND c.kind='private' AND c.target_id=$2`, record.ID, target).Scan(&history); err != nil || history != 1 {
		t.Fatalf("history after fallback: %d %v", history, err)
	}
}
