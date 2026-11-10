package infrastructure

import (
	"context"
	"database/sql"
	"testing"

	"github.com/google/uuid"

	"github.com/HappyLadySauceM/Nagi/internal/memory/domain"
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
	if err := pool.QueryRow(ctx, `INSERT INTO messages (conversation_id, bot_id, direction, platform_message_id, sender_id, text_content, segments) VALUES ($1,$2,'incoming',9941001,994003,'tea', '[]'::jsonb) RETURNING id`, conversationID, botID).Scan(&messageID); err != nil {
		t.Fatalf("seed message: %v", err)
	}
	jobID := uuid.New()
	if _, err := pool.Exec(ctx, `INSERT INTO memory_extraction_jobs (id, bot_id, conversation_id, scope_type, scope_id, message_id, authorization_event_id, status, attempts, round_attempts, manual_retries) VALUES ($1,$2,$3,'group',$4,$5,77,'failed',5,3,0)`, jobID, botID, conversationID, groupID, messageID); err != nil {
		t.Fatalf("seed job: %v", err)
	}
	defer func() {
		_, _ = pool.Exec(ctx, `DELETE FROM memory_extraction_jobs WHERE bot_id=$1`, botID)
		_, _ = pool.Exec(ctx, `DELETE FROM messages WHERE bot_id=$1`, botID)
		_, _ = pool.Exec(ctx, `DELETE FROM conversations WHERE bot_id=$1`, botID)
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
	if err != nil || !found || job.Status != "failed" || job.AuthorizationEventID != 77 {
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
