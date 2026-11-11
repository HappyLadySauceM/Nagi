package infrastructure

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	memoryapp "github.com/HappyLadySauceM/Nagi/internal/memory/application"
	"github.com/HappyLadySauceM/Nagi/internal/memory/domain"
	scopedomain "github.com/HappyLadySauceM/Nagi/internal/scope/domain"
	scopeinfra "github.com/HappyLadySauceM/Nagi/internal/scope/infrastructure"
	"github.com/HappyLadySauceM/Nagi/pkg/db"
	"github.com/HappyLadySauceM/Nagi/pkg/db/dbtest"
)

// seedExtractionBoundaryFixture creates one authorized group conversation and
// one source message with its current authorization stamp.
// seedExtractionBoundaryFixture 创建一个已授权群会话及一条带当前授权戳的来源消息。
func seedExtractionBoundaryFixture(t *testing.T, botID, groupID int64) (*db.Pool, *Repo, domain.ExtractionScope, int64) {
	t.Helper()
	pool := dbtest.Open(t)
	repo, err := NewRepo(pool)
	if err != nil {
		t.Fatalf("new repo: %v", err)
	}
	ctx := context.Background()
	if _, err := pool.Exec(ctx, `INSERT INTO bot_accounts (bot_id, nickname) VALUES ($1, 'extraction-boundary-edge-test') ON CONFLICT DO NOTHING`, botID); err != nil {
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
		t.Fatalf("resolve event: id=%d authorized=%v err=%v", eventID, authorized, err)
	}
	var messageID int64
	if err := pool.QueryRow(ctx, `INSERT INTO messages (conversation_id, bot_id, direction, platform_message_id, sender_id, text_content, segments, memory_authorization_event_id) VALUES ($1,$2,'incoming',$3,997503,'边界测试','[]'::jsonb,$4) RETURNING id`, conversationID, botID, time.Now().UnixNano(), eventID).Scan(&messageID); err != nil {
		t.Fatalf("seed message: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM memories WHERE bot_id=$1`, botID)
		_, _ = pool.Exec(ctx, `DELETE FROM memory_extraction_jobs WHERE bot_id=$1`, botID)
		_, _ = pool.Exec(ctx, `DELETE FROM memory_extraction_cursors WHERE bot_id=$1`, botID)
		_, _ = pool.Exec(ctx, `DELETE FROM messages WHERE bot_id=$1`, botID)
		_, _ = pool.Exec(ctx, `DELETE FROM conversations WHERE bot_id=$1`, botID)
		_, _ = pool.Exec(ctx, `DELETE FROM scope_policies WHERE bot_id=$1`, botID)
		_, _ = pool.Exec(ctx, `DELETE FROM scope_memory_authorization_events WHERE bot_id=$1`, botID)
		_, _ = pool.Exec(ctx, `DELETE FROM bot_accounts WHERE bot_id=$1`, botID)
	})
	return pool, repo, domain.ExtractionScope{
		BotID: botID, ConversationID: conversationID.String(), ScopeType: "group", ScopeID: groupID, AuthorizationEventID: eventID,
	}, messageID
}

// TestExtractionFinalizeRejectsExpiredLease verifies a late executor cannot
// write candidates after its lease has expired.
// TestExtractionFinalizeRejectsExpiredLease 验证租约过期后的晚到执行者不能写入候选。
func TestExtractionFinalizeRejectsExpiredLease(t *testing.T) {
	pool, repo, scope, messageID := seedExtractionBoundaryFixture(t, 997501, 997502)
	ctx := context.Background()
	if _, err := repo.CreateExtractionJobs(ctx, scope, []domain.ExtractionSourceMessage{{
		ID: messageID, BotID: scope.BotID, ConversationID: scope.ConversationID,
		ScopeType: scope.ScopeType, ScopeID: scope.ScopeID, SenderID: 997503,
		Text: "边界测试", AuthorizationEventID: scope.AuthorizationEventID,
	}}, 0); err != nil {
		t.Fatalf("create job: %v", err)
	}
	batch, found, err := repo.ClaimReadyExtractionBatch(ctx, domain.ExtractionClaimOptions{MaxMessages: 10, MaxRunes: 4000, Lease: 20 * time.Millisecond})
	if err != nil || !found {
		t.Fatalf("claim found=%v err=%v", found, err)
	}
	time.Sleep(40 * time.Millisecond)
	if _, err := repo.FinalizeExtractionBatch(ctx, batch, nil); !errors.Is(err, memoryapp.ErrExtractionLeaseLost) {
		t.Fatalf("finalize after lease expiry error = %v, want lease lost", err)
	}
	var count int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM memories WHERE bot_id=$1`, scope.BotID).Scan(&count); err != nil || count != 0 {
		t.Fatalf("memories after expired finalize = %d err=%v", count, err)
	}
}

// TestExtractionFinalizeRejectsMissingSource verifies a job whose source
// message disappeared cannot be completed with fabricated candidates.
// TestExtractionFinalizeRejectsMissingSource 验证来源消息消失的任务不能以候选完成。
func TestExtractionFinalizeRejectsMissingSource(t *testing.T) {
	pool, repo, scope, messageID := seedExtractionBoundaryFixture(t, 997511, 997512)
	ctx := context.Background()
	if _, err := repo.CreateExtractionJobs(ctx, scope, []domain.ExtractionSourceMessage{{
		ID: messageID, BotID: scope.BotID, ConversationID: scope.ConversationID,
		ScopeType: scope.ScopeType, ScopeID: scope.ScopeID, SenderID: 997503,
		Text: "边界测试", AuthorizationEventID: scope.AuthorizationEventID,
	}}, 0); err != nil {
		t.Fatalf("create job: %v", err)
	}
	batch, found, err := repo.ClaimReadyExtractionBatch(ctx, domain.ExtractionClaimOptions{MaxMessages: 10, MaxRunes: 4000, Lease: time.Minute})
	if err != nil || !found {
		t.Fatalf("claim found=%v err=%v", found, err)
	}
	if _, err := pool.Exec(ctx, `DELETE FROM messages WHERE id=$1`, messageID); err != nil {
		t.Fatalf("delete source: %v", err)
	}
	if _, err := repo.FinalizeExtractionBatch(ctx, batch, nil); !errors.Is(err, memoryapp.ErrExtractionSourceMissing) {
		t.Fatalf("finalize after source deletion error = %v, want source missing", err)
	}
	var count int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM memories WHERE bot_id=$1`, scope.BotID).Scan(&count); err != nil || count != 0 {
		t.Fatalf("memories after missing-source finalize = %d err=%v", count, err)
	}
}
