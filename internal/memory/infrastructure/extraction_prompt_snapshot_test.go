package infrastructure

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/HappyLadySauceM/Nagi/internal/memory/domain"
	scopedomain "github.com/HappyLadySauceM/Nagi/internal/scope/domain"
	scopeinfra "github.com/HappyLadySauceM/Nagi/internal/scope/infrastructure"
	"github.com/HappyLadySauceM/Nagi/pkg/db/dbtest"
)

// TestExtractionPromptSnapshotPersistence verifies a claimed task can persist
// and read back its immutable prompt snapshot through the repository.
// TestExtractionPromptSnapshotPersistence 验证已认领任务能通过仓储持久化并读回
// 不可变 Prompt 快照。
func TestExtractionPromptSnapshotPersistence(t *testing.T) {
	pool := dbtest.Open(t)
	repo, err := NewRepo(pool)
	if err != nil {
		t.Fatalf("new repo: %v", err)
	}
	ctx := context.Background()
	const botID = 997501
	const groupID = 997502
	if _, err := pool.Exec(ctx, `INSERT INTO bot_accounts (bot_id, nickname) VALUES ($1, 'prompt-snapshot-test') ON CONFLICT DO NOTHING`, botID); err != nil {
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
	if err := pool.QueryRow(ctx, `INSERT INTO messages (conversation_id, bot_id, direction, platform_message_id, sender_id, text_content, segments, memory_authorization_event_id) VALUES ($1,$2,'incoming',9975001,997503,'snapshot', '[]'::jsonb,$3) RETURNING id`, conversationID, botID, eventID).Scan(&messageID); err != nil {
		t.Fatalf("seed message: %v", err)
	}
	defer func() {
		_, _ = pool.Exec(ctx, `DELETE FROM memory_extraction_jobs WHERE bot_id=$1`, botID)
		_, _ = pool.Exec(ctx, `DELETE FROM messages WHERE bot_id=$1`, botID)
		_, _ = pool.Exec(ctx, `DELETE FROM conversations WHERE bot_id=$1`, botID)
		_, _ = pool.Exec(ctx, `DELETE FROM scope_policies WHERE bot_id=$1`, botID)
		_, _ = pool.Exec(ctx, `DELETE FROM scope_memory_authorization_events WHERE bot_id=$1`, botID)
		_, _ = pool.Exec(ctx, `DELETE FROM bot_accounts WHERE bot_id=$1`, botID)
	}()

	scope := domain.ExtractionScope{BotID: botID, ConversationID: conversationID.String(), ScopeType: "group", ScopeID: groupID, AuthorizationEventID: eventID}
	created, err := repo.CreateExtractionJobs(ctx, scope, []domain.ExtractionSourceMessage{{
		ID: messageID, BotID: botID, ConversationID: conversationID.String(), ScopeType: "group", ScopeID: groupID,
		SenderID: 997503, Text: "snapshot", AuthorizationEventID: eventID,
	}}, 0)
	if err != nil || created != 1 {
		t.Fatalf("create jobs = %d err=%v", created, err)
	}
	batch, found, err := repo.ClaimReadyExtractionBatch(ctx, domain.ExtractionClaimOptions{MaxMessages: 10, MaxRunes: 4000, Lease: time.Minute})
	if err != nil || !found || len(batch.Jobs) != 1 {
		t.Fatalf("claim batch found=%v err=%v jobs=%d", found, err, len(batch.Jobs))
	}
	snapshot := domain.PromptSnapshot{
		PromptID: "memory.extraction", Kind: "memory_extraction", Provider: "openai",
		BotID: botID, TargetType: "group", TargetID: groupID, ConversationID: conversationID.String(),
		FinalHash: "final-hash", RenderedHash: "rendered-hash",
		Layers: []domain.PromptSnapshotLayer{{Layer: "account_default", VersionID: 10, Version: 1, Source: "local", Hash: "layer-hash"}},
	}
	if err := repo.PersistExtractionPromptSnapshot(ctx, batch, snapshot); err != nil {
		t.Fatalf("persist prompt snapshot: %v", err)
	}
	if err := repo.PersistExtractionPromptSnapshot(ctx, batch, snapshot); err != nil {
		t.Fatalf("persist identical snapshot should be idempotent: %v", err)
	}
	conflict := snapshot
	conflict.FinalHash = "other-final-hash"
	if err := repo.PersistExtractionPromptSnapshot(ctx, batch, conflict); err == nil {
		t.Fatal("conflicting prompt snapshot should fail")
	}
	job, found, err := repo.GetExtractionJob(ctx, batch.Jobs[0].ID)
	if err != nil || !found || job.PromptSnapshot == nil {
		t.Fatalf("get job found=%v err=%v snapshot=%+v", found, err, job.PromptSnapshot)
	}
	if job.PromptSnapshot.PromptID != snapshot.PromptID || job.PromptSnapshot.FinalHash != snapshot.FinalHash || len(job.PromptSnapshot.Layers) != 1 {
		t.Fatalf("read snapshot = %+v", job.PromptSnapshot)
	}
	if _, err := pool.Exec(ctx, `UPDATE memory_extraction_jobs SET status='failed', claim_token=NULL, lease_expires_at=NULL WHERE id=$1`, batch.Jobs[0].ID); err != nil {
		t.Fatalf("mark job failed: %v", err)
	}
	if retried, err := repo.RetryExtractionJob(ctx, batch.Jobs[0].ID, 1); err != nil || !retried {
		t.Fatalf("retry job: retried=%v err=%v", retried, err)
	}
	retryBatch, found, err := repo.ClaimReadyExtractionBatch(ctx, domain.ExtractionClaimOptions{MaxMessages: 10, MaxRunes: 4000, Lease: time.Minute})
	if err != nil || !found || retryBatch.PromptSnapshot == nil {
		t.Fatalf("retry claim found=%v err=%v snapshot=%+v", found, err, retryBatch.PromptSnapshot)
	}
	if retryBatch.PromptSnapshot.FinalHash != snapshot.FinalHash {
		t.Fatalf("retry snapshot final hash = %q, want %q", retryBatch.PromptSnapshot.FinalHash, snapshot.FinalHash)
	}
}

// TestExtractionClaimSeparatesSnapshotAndFreshJobs verifies a retried job with
// a stored prompt snapshot and a newly created job without one are claimed in
// separate homogeneous batches instead of deadlocking the conversation.
// TestExtractionClaimSeparatesSnapshotAndFreshJobs 验证已带 Prompt 快照的重试任务
// 与新建无快照任务会分批认领，不会互相卡住会话。
func TestExtractionClaimSeparatesSnapshotAndFreshJobs(t *testing.T) {
	pool := dbtest.Open(t)
	repo, err := NewRepo(pool)
	if err != nil {
		t.Fatalf("new repo: %v", err)
	}
	ctx := context.Background()
	const botID = 997511
	const groupID = 997512
	if _, err := pool.Exec(ctx, `INSERT INTO bot_accounts (bot_id, nickname) VALUES ($1, 'prompt-snapshot-claim-test') ON CONFLICT DO NOTHING`, botID); err != nil {
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
	messageIDs := make([]int64, 0, 2)
	for index, text := range []string{"first", "second"} {
		var messageID int64
		if err := pool.QueryRow(ctx, `INSERT INTO messages (conversation_id, bot_id, direction, platform_message_id, sender_id, text_content, segments, memory_authorization_event_id) VALUES ($1,$2,'incoming',$3,997513,$4,'[]'::jsonb,$5) RETURNING id`, conversationID, botID, 9975100+int64(index), text, eventID).Scan(&messageID); err != nil {
			t.Fatalf("seed message %d: %v", index, err)
		}
		messageIDs = append(messageIDs, messageID)
	}
	defer func() {
		_, _ = pool.Exec(ctx, `DELETE FROM memory_extraction_jobs WHERE bot_id=$1`, botID)
		_, _ = pool.Exec(ctx, `DELETE FROM memory_extraction_cursors WHERE bot_id=$1`, botID)
		_, _ = pool.Exec(ctx, `DELETE FROM messages WHERE bot_id=$1`, botID)
		_, _ = pool.Exec(ctx, `DELETE FROM conversations WHERE bot_id=$1`, botID)
		_, _ = pool.Exec(ctx, `DELETE FROM scope_policies WHERE bot_id=$1`, botID)
		_, _ = pool.Exec(ctx, `DELETE FROM scope_memory_authorization_events WHERE bot_id=$1`, botID)
		_, _ = pool.Exec(ctx, `DELETE FROM bot_accounts WHERE bot_id=$1`, botID)
	}()

	scope := domain.ExtractionScope{BotID: botID, ConversationID: conversationID.String(), ScopeType: "group", ScopeID: groupID, AuthorizationEventID: eventID}
	if created, err := repo.CreateExtractionJobs(ctx, scope, []domain.ExtractionSourceMessage{{
		ID: messageIDs[0], BotID: botID, ConversationID: conversationID.String(), ScopeType: "group", ScopeID: groupID,
		SenderID: 997513, Text: "first", AuthorizationEventID: eventID,
	}}, 0); err != nil || created != 1 {
		t.Fatalf("create first job = %d err=%v", created, err)
	}
	batch, found, err := repo.ClaimReadyExtractionBatch(ctx, domain.ExtractionClaimOptions{MaxMessages: 10, MaxRunes: 4000, Lease: time.Minute})
	if err != nil || !found || len(batch.Jobs) != 1 {
		t.Fatalf("claim first batch found=%v err=%v jobs=%d", found, err, len(batch.Jobs))
	}
	snapshot := domain.PromptSnapshot{
		PromptID: "memory.extraction", Kind: "memory_extraction", Provider: "openai",
		BotID: botID, TargetType: "group", TargetID: groupID, ConversationID: conversationID.String(),
		FinalHash: "final-hash", RenderedHash: "rendered-hash",
		Layers: []domain.PromptSnapshotLayer{{Layer: "account_default", VersionID: 10, Version: 1, Source: "local", Hash: "layer-hash"}},
	}
	if err := repo.PersistExtractionPromptSnapshot(ctx, batch, snapshot); err != nil {
		t.Fatalf("persist prompt snapshot: %v", err)
	}
	if _, err := pool.Exec(ctx, `UPDATE memory_extraction_jobs SET status='failed', claim_token=NULL, lease_expires_at=NULL WHERE id=$1`, batch.Jobs[0].ID); err != nil {
		t.Fatalf("mark first job failed: %v", err)
	}
	if retried, err := repo.RetryExtractionJob(ctx, batch.Jobs[0].ID, 1); err != nil || !retried {
		t.Fatalf("retry first job: retried=%v err=%v", retried, err)
	}
	// Pin the retried snapshot job ahead of the fresh job so the regression is
	// deterministic: previously both landed in one claim and hit "mixed prompt
	// snapshots in batch" on every worker tick.
	if _, err := pool.Exec(ctx, `UPDATE memory_extraction_jobs SET next_run_at=clock_timestamp()-interval '1 minute' WHERE id=$1`, batch.Jobs[0].ID); err != nil {
		t.Fatalf("pin retry next_run_at: %v", err)
	}
	if created, err := repo.CreateExtractionJobs(ctx, scope, []domain.ExtractionSourceMessage{{
		ID: messageIDs[1], BotID: botID, ConversationID: conversationID.String(), ScopeType: "group", ScopeID: groupID,
		SenderID: 997513, Text: "second", AuthorizationEventID: eventID,
	}}, 0); err != nil || created != 1 {
		t.Fatalf("create second job = %d err=%v", created, err)
	}

	snapshotBatch, found, err := repo.ClaimReadyExtractionBatch(ctx, domain.ExtractionClaimOptions{MaxMessages: 10, MaxRunes: 4000, Lease: time.Minute})
	if err != nil {
		t.Fatalf("claim snapshot job: %v", err)
	}
	if !found || len(snapshotBatch.Jobs) != 1 || snapshotBatch.PromptSnapshot == nil || snapshotBatch.Jobs[0].ID != batch.Jobs[0].ID {
		t.Fatalf("snapshot batch found=%v jobs=%d snapshot=%+v", found, len(snapshotBatch.Jobs), snapshotBatch.PromptSnapshot)
	}
	if _, err := pool.Exec(ctx, `UPDATE memory_extraction_jobs SET status='done', claim_token=NULL, lease_expires_at=NULL WHERE id=$1`, snapshotBatch.Jobs[0].ID); err != nil {
		t.Fatalf("mark snapshot job done: %v", err)
	}
	freshBatch, found, err := repo.ClaimReadyExtractionBatch(ctx, domain.ExtractionClaimOptions{MaxMessages: 10, MaxRunes: 4000, Lease: time.Minute})
	if err != nil {
		t.Fatalf("claim fresh job: %v", err)
	}
	if !found || len(freshBatch.Jobs) != 1 || freshBatch.PromptSnapshot != nil || freshBatch.Jobs[0].ID == batch.Jobs[0].ID {
		t.Fatalf("fresh batch found=%v jobs=%d snapshot=%+v", found, len(freshBatch.Jobs), freshBatch.PromptSnapshot)
	}
}
