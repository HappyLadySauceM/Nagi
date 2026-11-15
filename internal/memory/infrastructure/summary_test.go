package infrastructure

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/HappyLadySauceM/Nagi/internal/memory/domain"
	"github.com/HappyLadySauceM/Nagi/pkg/db/dbtest"
)

func TestSummaryCountsMemoriesAndExtractionJobs(t *testing.T) {
	pool := dbtest.Open(t)
	ctx := context.Background()
	const botID = 996001
	const groupID = 996002
	_, _ = pool.Exec(ctx, `DELETE FROM memory_extraction_jobs WHERE bot_id=$1`, botID)
	_, _ = pool.Exec(ctx, `DELETE FROM memories WHERE bot_id=$1`, botID)
	_, _ = pool.Exec(ctx, `DELETE FROM conversations WHERE bot_id=$1`, botID)
	if _, err := pool.Exec(ctx, `INSERT INTO bot_accounts (bot_id, nickname) VALUES ($1, 'summary-test') ON CONFLICT DO NOTHING`, botID); err != nil {
		t.Fatalf("seed bot: %v", err)
	}
	conversationID := uuid.New()
	if err := pool.QueryRow(ctx, `INSERT INTO conversations (id, bot_id, kind, target_id, last_message_at) VALUES ($1,$2,'group',$3,now()) ON CONFLICT (bot_id, kind, target_id) DO UPDATE SET last_message_at=now() RETURNING id`, conversationID, botID, groupID).Scan(&conversationID); err != nil {
		t.Fatalf("seed conversation: %v", err)
	}
	repo, err := NewRepo(pool)
	if err != nil {
		t.Fatalf("new repo: %v", err)
	}
	seeds := []domain.Memory{
		{BotID: botID, Visibility: domain.VisibilityGroup, OwnerKey: "996002", Content: "active", Status: domain.StatusActive},
		{BotID: botID, Visibility: domain.VisibilityGroup, OwnerKey: "996002", Content: "candidate", Status: domain.StatusCandidate},
		{BotID: botID, Visibility: domain.VisibilityGroup, OwnerKey: "996002", Content: "superseded", Status: domain.StatusSuperseded},
		{BotID: botID, Visibility: domain.VisibilityBotShared, Content: "shared", Status: domain.StatusActive},
	}
	for _, seed := range seeds {
		if _, err := repo.Insert(ctx, seed); err != nil {
			t.Fatalf("seed memory %s: %v", seed.Content, err)
		}
	}
	for _, job := range []struct {
		status     string
		lastError  string
		claimToken bool
	}{
		{status: "pending"},
		{status: "processing", claimToken: true},
		{status: "failed", lastError: "model_timeout"},
	} {
		var claim any
		var lease any
		if job.claimToken {
			claim = uuid.New()
			lease = "now() + interval '1 minute'"
		}
		id := uuid.New()
		_, err := pool.Exec(ctx, `INSERT INTO memory_extraction_jobs (id, bot_id, conversation_id, scope_type, scope_id, message_id, authorization_event_id, status, attempts, round_attempts, manual_retries, last_error_code, claim_token, lease_expires_at) VALUES ($1,$2,$3,'group',$4,NULL,1,$5,0,0,0,$6,$7,`+leaseClause(lease)+`)`, id, botID, conversationID, groupID, job.status, nullableString(job.lastError), claim)
		if err != nil {
			t.Fatalf("seed job %s: %v", job.status, err)
		}
	}
	defer func() {
		_, _ = pool.Exec(ctx, `DELETE FROM memory_extraction_jobs WHERE bot_id=$1`, botID)
		_, _ = pool.Exec(ctx, `DELETE FROM memories WHERE bot_id=$1`, botID)
		_, _ = pool.Exec(ctx, `DELETE FROM conversations WHERE bot_id=$1`, botID)
	}()

	summary, err := repo.MemorySummary(ctx, botID)
	if err != nil {
		t.Fatalf("memory summary: %v", err)
	}
	if summary.Total != 4 || summary.Active != 2 || summary.Candidate != 1 || summary.Superseded != 1 {
		t.Fatalf("memory summary = %+v", summary)
	}
	if summary.ByVisibility[domain.VisibilityGroup] != 3 || summary.ByVisibility[domain.VisibilityBotShared] != 1 {
		t.Fatalf("visibility summary = %+v", summary.ByVisibility)
	}
	extraction, err := repo.ExtractionSummary(ctx, botID)
	if err != nil {
		t.Fatalf("extraction summary: %v", err)
	}
	if extraction.PendingJobs != 1 || extraction.ProcessingJobs != 1 || extraction.FailedJobs != 1 || extraction.LastErrorCode != "model_timeout" {
		t.Fatalf("extraction summary = %+v", extraction)
	}
}

func nullableString(value string) any {
	if value == "" {
		return nil
	}
	return value
}

func leaseClause(lease any) string {
	if lease == nil {
		return "NULL"
	}
	return lease.(string)
}
