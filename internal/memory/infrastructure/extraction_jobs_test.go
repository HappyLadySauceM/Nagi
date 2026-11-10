package infrastructure

import (
	"context"
	"database/sql"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/HappyLadySauceM/Nagi/internal/memory/domain"
	scopedomain "github.com/HappyLadySauceM/Nagi/internal/scope/domain"
	scopeinfra "github.com/HappyLadySauceM/Nagi/internal/scope/infrastructure"
	"github.com/HappyLadySauceM/Nagi/pkg/db/dbtest"
)

// TestExtractionJobManagementReadsAndRetries verifies the management read model
// and that a manual retry preserves cumulative attempts while starting a new
// round.
// TestExtractionJobManagementReadsAndRetries 验证管理读取模型，且手动重试保留
// 累计尝试次数并开启新一轮。
func TestExtractionJobManagementReadsAndRetries(t *testing.T) {
	pool := dbtest.Open(t)
	repo, err := NewRepo(pool)
	if err != nil {
		t.Fatalf("new repo: %v", err)
	}
	ctx := context.Background()
	const botID = 994001
	const groupID = 994002
	if _, err := pool.Exec(ctx, `INSERT INTO bot_accounts (bot_id, nickname) VALUES ($1, 'extraction-job-test') ON CONFLICT DO NOTHING`, botID); err != nil {
		t.Fatalf("seed bot: %v", err)
	}
	conversationID := uuid.New()
	if _, err := pool.Exec(ctx, `INSERT INTO conversations (id, bot_id, kind, target_id, last_message_at) VALUES ($1,$2,'group',$3,now()) ON CONFLICT DO NOTHING`, conversationID, botID, groupID); err != nil {
		t.Fatalf("seed conversation: %v", err)
	}
	var messageID int64
	scopeRepo, err := scopeinfra.NewRepo(pool)
	if err != nil {
		t.Fatalf("new scope repo: %v", err)
	}
	policy := scopedomain.Default(scopedomain.ScopeGroup, groupID)
	policy.Enabled = true
	policy.StoreMessages = true
	policy.AllowMemoryWrite = true
	if err := scopeRepo.Upsert(ctx, botID, policy); err != nil {
		t.Fatalf("seed policy: %v", err)
	}
	eventID, authorized, err := scopeRepo.ResolveMemoryAuthorization(ctx, botID, scopedomain.ScopeGroup, groupID)
	if err != nil || !authorized || eventID == 0 {
		t.Fatalf("resolve authorization: event=%d authorized=%v err=%v", eventID, authorized, err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO messages (conversation_id, bot_id, direction, platform_message_id, sender_id, text_content, segments, memory_authorization_event_id) VALUES ($1,$2,'incoming',9941001,994003,'tea', '[]'::jsonb,$3) RETURNING id`, conversationID, botID, eventID).Scan(&messageID); err != nil {
		t.Fatalf("seed message: %v", err)
	}
	jobID := uuid.New()
	if _, err := pool.Exec(ctx, `INSERT INTO memory_extraction_jobs (id, bot_id, conversation_id, scope_type, scope_id, message_id, authorization_event_id, status, attempts, round_attempts, manual_retries) VALUES ($1,$2,$3,'group',$4,$5,$6,'failed',5,3,0)`, jobID, botID, conversationID, groupID, messageID, eventID); err != nil {
		t.Fatalf("seed job: %v", err)
	}
	defer func() {
		_, _ = pool.Exec(ctx, `DELETE FROM memory_extraction_jobs WHERE bot_id=$1`, botID)
		_, _ = pool.Exec(ctx, `DELETE FROM messages WHERE bot_id=$1`, botID)
		_, _ = pool.Exec(ctx, `DELETE FROM conversations WHERE bot_id=$1`, botID)
		_, _ = pool.Exec(ctx, `DELETE FROM scope_policies WHERE bot_id=$1`, botID)
		_, _ = pool.Exec(ctx, `DELETE FROM scope_memory_authorization_events WHERE bot_id=$1`, botID)
	}()

	items, total, err := repo.ListExtractionJobs(ctx, domain.ExtractionJobFilter{BotID: botID, Page: 1, Limit: 10})
	if err != nil {
		t.Fatalf("list jobs: %v", err)
	}
	if total != 1 || len(items) != 1 || items[0].ID != jobID.String() || !items[0].SourceAvailable || items[0].MessageID != messageID {
		t.Fatalf("job list = %+v total=%d", items, total)
	}
	if items[0].Attempts != 5 || items[0].RoundAttempts != 3 {
		t.Fatalf("attempt counters = attempts:%d round:%d", items[0].Attempts, items[0].RoundAttempts)
	}

	job, found, err := repo.GetExtractionJob(ctx, jobID.String())
	if err != nil || !found || job.Status != "failed" || job.AuthorizationEventID != eventID {
		t.Fatalf("get job = %+v found=%v err=%v", job, found, err)
	}

	retried, err := repo.RetryExtractionJob(ctx, jobID.String(), 1)
	if err != nil || !retried {
		t.Fatalf("retry job: retried=%v err=%v", retried, err)
	}
	var status string
	var attempts, roundAttempts, manualRetries int
	if err := pool.QueryRow(ctx, `SELECT status, attempts, round_attempts, manual_retries FROM memory_extraction_jobs WHERE id=$1`, jobID).Scan(&status, &attempts, &roundAttempts, &manualRetries); err != nil {
		t.Fatalf("read retried job: %v", err)
	}
	if status != "pending" || attempts != 5 || roundAttempts != 0 || manualRetries != 1 {
		t.Fatalf("retried job state status=%s attempts=%d round=%d manual=%d", status, attempts, roundAttempts, manualRetries)
	}
	if retriedAgain, err := repo.RetryExtractionJob(ctx, jobID.String(), 1); err != nil || retriedAgain {
		t.Fatalf("second retry should be rejected: retried=%v err=%v", retriedAgain, err)
	}

	var nullMessageID sql.NullInt64
	if err := pool.QueryRow(ctx, `SELECT message_id FROM memory_extraction_jobs WHERE id=$1`, jobID).Scan(&nullMessageID); err != nil || !nullMessageID.Valid {
		t.Fatalf("message id after retry = %v err=%v", nullMessageID, err)
	}
}

// TestRetryExtractionJobRechecksAuthorizationUnderLock verifies a retry waits
// for a concurrent revocation transaction and refuses to update the job once
// the effective authorization generation has changed.
// TestRetryExtractionJobRechecksAuthorizationUnderLock 验证重试会等待并发撤权事务，
// 并在有效授权代际变化后拒绝更新任务。
func TestRetryExtractionJobRechecksAuthorizationUnderLock(t *testing.T) {
	pool := dbtest.Open(t)
	repo, err := NewRepo(pool)
	if err != nil {
		t.Fatalf("new repo: %v", err)
	}
	ctx := context.Background()
	const botID = 994101
	const groupID = 994102
	if _, err := pool.Exec(ctx, `INSERT INTO bot_accounts (bot_id, nickname) VALUES ($1, 'retry-lock-test') ON CONFLICT DO NOTHING`, botID); err != nil {
		t.Fatalf("seed bot: %v", err)
	}
	conversationID := uuid.New()
	if _, err := pool.Exec(ctx, `INSERT INTO conversations (id, bot_id, kind, target_id, last_message_at) VALUES ($1,$2,'group',$3,now())`, conversationID, botID, groupID); err != nil {
		t.Fatalf("seed conversation: %v", err)
	}
	scopeRepo, err := scopeinfra.NewRepo(pool)
	if err != nil {
		t.Fatalf("new scope repo: %v", err)
	}
	policy := scopedomain.Default(scopedomain.ScopeGroup, groupID)
	policy.Enabled = true
	policy.StoreMessages = true
	policy.AllowMemoryWrite = true
	if err := scopeRepo.Upsert(ctx, botID, policy); err != nil {
		t.Fatalf("seed policy: %v", err)
	}
	eventID, authorized, err := scopeRepo.ResolveMemoryAuthorization(ctx, botID, scopedomain.ScopeGroup, groupID)
	if err != nil || !authorized || eventID == 0 {
		t.Fatalf("resolve authorization: event=%d authorized=%v err=%v", eventID, authorized, err)
	}
	var messageID int64
	if err := pool.QueryRow(ctx, `INSERT INTO messages (conversation_id, bot_id, direction, platform_message_id, sender_id, text_content, segments, memory_authorization_event_id) VALUES ($1,$2,'incoming',9941101,994103,'tea','[]'::jsonb,$3) RETURNING id`, conversationID, botID, eventID).Scan(&messageID); err != nil {
		t.Fatalf("seed message: %v", err)
	}
	jobID := uuid.New()
	if _, err := pool.Exec(ctx, `INSERT INTO memory_extraction_jobs (id, bot_id, conversation_id, scope_type, scope_id, message_id, authorization_event_id, status, attempts, round_attempts, manual_retries) VALUES ($1,$2,$3,'group',$4,$5,$6,'failed',1,0,0)`, jobID, botID, conversationID, groupID, messageID, eventID); err != nil {
		t.Fatalf("seed job: %v", err)
	}
	defer func() {
		_, _ = pool.Exec(ctx, `DELETE FROM memory_extraction_jobs WHERE bot_id=$1`, botID)
		_, _ = pool.Exec(ctx, `DELETE FROM messages WHERE bot_id=$1`, botID)
		_, _ = pool.Exec(ctx, `DELETE FROM conversations WHERE bot_id=$1`, botID)
		_, _ = pool.Exec(ctx, `DELETE FROM scope_policies WHERE bot_id=$1`, botID)
		_, _ = pool.Exec(ctx, `DELETE FROM scope_memory_authorization_events WHERE bot_id=$1`, botID)
	}()

	lockTx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin lock transaction: %v", err)
	}
	defer lockTx.Rollback(ctx)
	for _, key := range []string{
		fmt.Sprintf("memory-auth:%d:group:%d", botID, groupID),
		fmt.Sprintf("memory-auth:%d:group:0", botID),
	} {
		if _, err := lockTx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, key); err != nil {
			t.Fatalf("lock authorization: %v", err)
		}
	}
	var revokedEventID int64
	if err := lockTx.QueryRow(ctx, `INSERT INTO scope_memory_authorization_events (bot_id, scope_type, scope_id, event_kind, authorized, policy_version) VALUES ($1,'group',$2,'updated',false,2) RETURNING id`, botID, groupID).Scan(&revokedEventID); err != nil {
		t.Fatalf("seed revoked event: %v", err)
	}
	if _, err := lockTx.Exec(ctx, `UPDATE scope_policies SET allow_memory_write=false, memory_authorization_event_id=$1, version=version+1 WHERE bot_id=$2 AND scope_type='group' AND scope_id=$3`, revokedEventID, botID, groupID); err != nil {
		t.Fatalf("disable policy: %v", err)
	}

	done := make(chan struct {
		retried bool
		err     error
	}, 1)
	go func() {
		retried, err := repo.RetryExtractionJob(ctx, jobID.String(), 1)
		done <- struct {
			retried bool
			err     error
		}{retried: retried, err: err}
	}()
	select {
	case got := <-done:
		t.Fatalf("retry completed before revocation transaction released lock: retried=%v err=%v", got.retried, got.err)
	case <-time.After(100 * time.Millisecond):
	}
	if err := lockTx.Commit(ctx); err != nil {
		t.Fatalf("commit revocation: %v", err)
	}
	select {
	case got := <-done:
		if got.err != nil || got.retried {
			t.Fatalf("retry after revocation = retried:%v err:%v, want rejected", got.retried, got.err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("retry did not finish after revocation commit")
	}
	var status string
	var manualRetries int
	if err := pool.QueryRow(ctx, `SELECT status, manual_retries FROM memory_extraction_jobs WHERE id=$1`, jobID).Scan(&status, &manualRetries); err != nil {
		t.Fatalf("read job after rejected retry: %v", err)
	}
	if status != "failed" || manualRetries != 0 {
		t.Fatalf("job after rejected retry status=%s manual_retries=%d, want failed/0", status, manualRetries)
	}
}
