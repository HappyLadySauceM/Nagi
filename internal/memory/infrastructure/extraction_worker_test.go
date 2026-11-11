package infrastructure

import (
	"context"
	"strconv"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/HappyLadySauceM/Nagi/internal/memory/domain"
	scopedomain "github.com/HappyLadySauceM/Nagi/internal/scope/domain"
	scopeinfra "github.com/HappyLadySauceM/Nagi/internal/scope/infrastructure"
	"github.com/HappyLadySauceM/Nagi/pkg/db/dbtest"
)

// TestExtractionRepositoryClaimAndFinalize exercises the real database scan,
// job creation, bounded claim, authorization recheck and atomic candidate
// insertion path.
// TestExtractionRepositoryClaimAndFinalize 验证真实数据库上的扫描、建任务、有界认领、
// 授权复核与候选原子落库链路。
func TestExtractionRepositoryClaimAndFinalize(t *testing.T) {
	pool := dbtest.Open(t)
	repo, err := NewRepo(pool)
	if err != nil {
		t.Fatalf("new repo: %v", err)
	}
	ctx := context.Background()
	const botID = 997001
	const groupID = 997002
	if _, err := pool.Exec(ctx, `INSERT INTO bot_accounts (bot_id, nickname) VALUES ($1, 'extraction-worker-test') ON CONFLICT DO NOTHING`, botID); err != nil {
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
	messageIDs := make([]int64, 0, 2)
	for _, text := range []string{"用户A喜欢喝乌龙茶", "用户A周末喜欢爬山"} {
		var id int64
		if err := pool.QueryRow(ctx, `INSERT INTO messages (conversation_id, bot_id, direction, platform_message_id, sender_id, text_content, segments, memory_authorization_event_id) VALUES ($1,$2,'incoming',$3,997003,$4,'[]'::jsonb,$5) RETURNING id`, conversationID, botID, time.Now().UnixNano(), text, eventID).Scan(&id); err != nil {
			t.Fatalf("seed message: %v", err)
		}
		messageIDs = append(messageIDs, id)
	}
	defer func() {
		_, _ = pool.Exec(ctx, `DELETE FROM memories WHERE bot_id=$1`, botID)
		_, _ = pool.Exec(ctx, `DELETE FROM memory_extraction_jobs WHERE bot_id=$1`, botID)
		_, _ = pool.Exec(ctx, `DELETE FROM memory_extraction_cursors WHERE bot_id=$1`, botID)
		_, _ = pool.Exec(ctx, `DELETE FROM messages WHERE bot_id=$1`, botID)
		_, _ = pool.Exec(ctx, `DELETE FROM conversations WHERE bot_id=$1`, botID)
		_, _ = pool.Exec(ctx, `DELETE FROM scope_policies WHERE bot_id=$1`, botID)
		_, _ = pool.Exec(ctx, `DELETE FROM scope_memory_authorization_events WHERE bot_id=$1`, botID)
		_, _ = pool.Exec(ctx, `DELETE FROM bot_accounts WHERE bot_id=$1`, botID)
	}()

	scopes, err := repo.ListAuthorizedScopes(ctx, 100)
	if err != nil {
		t.Fatalf("list scopes: %v", err)
	}
	var scope domain.ExtractionScope
	foundScope := false
	for _, item := range scopes {
		if item.BotID == botID && item.ConversationID == conversationID.String() {
			scope = item
			foundScope = true
		}
	}
	if !foundScope || scope.AuthorizationEventID != eventID {
		t.Fatalf("scope = %+v found=%v, want event %d", scope, foundScope, eventID)
	}
	messages, err := repo.ListUnregisteredMessages(ctx, botID, conversationID.String(), eventID, 10)
	if err != nil || len(messages) != 2 {
		t.Fatalf("unregistered messages = %d err=%v", len(messages), err)
	}
	if created, err := repo.CreateExtractionJobs(ctx, scope, messages, 0); err != nil || created != 2 {
		t.Fatalf("create jobs = %d err=%v", created, err)
	}
	batch, found, err := repo.ClaimReadyExtractionBatch(ctx, domain.ExtractionClaimOptions{MaxMessages: 10, MaxRunes: 4000, Lease: time.Minute})
	if err != nil || !found {
		t.Fatalf("claim batch found=%v err=%v", found, err)
	}
	if len(batch.Jobs) != 2 || len(batch.Messages) != 2 || batch.Scope.AuthorizationEventID != eventID {
		t.Fatalf("batch = %+v", batch)
	}
	source := batch.Messages[0]
	subjectID := strconv.FormatInt(source.SenderID, 10)
	candidate := domain.Memory{
		BotID: botID, Visibility: domain.VisibilityGroup, OwnerKey: strconv.FormatInt(groupID, 10),
		Content: "用户A喜欢喝乌龙茶", SourceMessageID: source.ID, SourceSenderID: source.SenderID,
		Source: "message:" + strconv.FormatInt(source.ID, 10), Origin: domain.OriginExtraction,
		SubjectType: domain.SubjectUser, SubjectID: subjectID,
		DedupeKey:  domain.ComputeDedupeKey(botID, domain.VisibilityGroup, strconv.FormatInt(groupID, 10), domain.SubjectUser, subjectID, "用户A喜欢喝乌龙茶"),
		Confidence: 0.9, Status: domain.StatusCandidate,
	}
	result, err := repo.FinalizeExtractionBatch(ctx, batch, []domain.Memory{candidate})
	if err != nil || result.Created != 1 {
		t.Fatalf("finalize result = %+v err=%v", result, err)
	}
	var status string
	var origin string
	var sourceMessageID int64
	if err := pool.QueryRow(ctx, `SELECT status, origin, source_message_id FROM memories WHERE bot_id=$1 AND content=$2`, botID, candidate.Content).Scan(&status, &origin, &sourceMessageID); err != nil {
		t.Fatalf("read candidate: %v", err)
	}
	if status != "candidate" || origin != "extraction" || sourceMessageID != source.ID {
		t.Fatalf("candidate row status=%s origin=%s source=%d", status, origin, sourceMessageID)
	}
	var done int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM memory_extraction_jobs WHERE bot_id=$1 AND status='done'`, botID).Scan(&done); err != nil || done != 2 {
		t.Fatalf("done jobs = %d err=%v", done, err)
	}
}

// TestExtractionClaimCancelsRevokedConversation verifies a claimed candidate
// page is not returned after the scope's memory-write authorization is revoked.
// TestExtractionClaimCancelsRevokedConversation 验证撤权后不会返回可认领批次，
// 且待处理任务被取消。
func TestExtractionClaimCancelsRevokedConversation(t *testing.T) {
	pool := dbtest.Open(t)
	repo, err := NewRepo(pool)
	if err != nil {
		t.Fatalf("new repo: %v", err)
	}
	ctx := context.Background()
	const botID = 997101
	const groupID = 997102
	if _, err := pool.Exec(ctx, `INSERT INTO bot_accounts (bot_id, nickname) VALUES ($1, 'extraction-revoke-test') ON CONFLICT DO NOTHING`, botID); err != nil {
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
	eventID, _, err := scopeRepo.ResolveMemoryAuthorization(ctx, botID, scopedomain.ScopeGroup, groupID)
	if err != nil || eventID == 0 {
		t.Fatalf("resolve event: id=%d err=%v", eventID, err)
	}
	var messageID int64
	if err := pool.QueryRow(ctx, `INSERT INTO messages (conversation_id, bot_id, direction, platform_message_id, sender_id, text_content, segments, memory_authorization_event_id) VALUES ($1,$2,'incoming',9971001,997103,'待撤权消息','[]'::jsonb,$3) RETURNING id`, conversationID, botID, eventID).Scan(&messageID); err != nil {
		t.Fatalf("seed message: %v", err)
	}
	defer func() {
		_, _ = pool.Exec(ctx, `DELETE FROM memories WHERE bot_id=$1`, botID)
		_, _ = pool.Exec(ctx, `DELETE FROM memory_extraction_jobs WHERE bot_id=$1`, botID)
		_, _ = pool.Exec(ctx, `DELETE FROM messages WHERE bot_id=$1`, botID)
		_, _ = pool.Exec(ctx, `DELETE FROM conversations WHERE bot_id=$1`, botID)
		_, _ = pool.Exec(ctx, `DELETE FROM scope_policies WHERE bot_id=$1`, botID)
		_, _ = pool.Exec(ctx, `DELETE FROM scope_memory_authorization_events WHERE bot_id=$1`, botID)
		_, _ = pool.Exec(ctx, `DELETE FROM bot_accounts WHERE bot_id=$1`, botID)
	}()
	if _, err := pool.Exec(ctx, `INSERT INTO memory_extraction_jobs (id, bot_id, conversation_id, scope_type, scope_id, message_id, authorization_event_id, status, next_run_at) VALUES ($1,$2,$3::uuid,'group',$4,$5,$6,'pending',now())`, uuid.New(), botID, conversationID, groupID, messageID, eventID); err != nil {
		t.Fatalf("seed job: %v", err)
	}
	policy.AllowMemoryWrite = false
	if err := scopeRepo.Upsert(ctx, botID, policy); err != nil {
		t.Fatalf("revoke policy: %v", err)
	}
	_, found, err := repo.ClaimReadyExtractionBatch(ctx, domain.ExtractionClaimOptions{MaxMessages: 10, MaxRunes: 4000, Lease: time.Minute})
	if err != nil || found {
		t.Fatalf("claim after revoke found=%v err=%v", found, err)
	}
	var status string
	if err := pool.QueryRow(ctx, `SELECT status FROM memory_extraction_jobs WHERE bot_id=$1`, botID).Scan(&status); err != nil {
		t.Fatalf("read job: %v", err)
	}
	if status != "cancelled" {
		t.Fatalf("job status = %s, want cancelled", status)
	}
}

// TestExtractionClaimKeepsConversationBoundary verifies one claim never mixes
// two conversations even when both are authorized under the same template.
// TestExtractionClaimKeepsConversationBoundary 验证即使两个会话继承同一模板，
// 一次认领也不会跨会话混合消息。
func TestExtractionClaimKeepsConversationBoundary(t *testing.T) {
	pool := dbtest.Open(t)
	repo, err := NewRepo(pool)
	if err != nil {
		t.Fatalf("new repo: %v", err)
	}
	ctx := context.Background()
	const botID = 997301
	scopeRepo, err := scopeinfra.NewRepo(pool)
	if err != nil {
		t.Fatalf("new scope repo: %v", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO bot_accounts (bot_id, nickname) VALUES ($1, 'extraction-boundary-test') ON CONFLICT DO NOTHING`, botID); err != nil {
		t.Fatalf("seed bot: %v", err)
	}
	template := scopedomain.Default(scopedomain.ScopeGroup, 0)
	template.Enabled = true
	template.StoreMessages = true
	template.AllowMemoryWrite = true
	if err := scopeRepo.Upsert(ctx, botID, template); err != nil {
		t.Fatalf("seed template: %v", err)
	}
	eventID, authorized, err := scopeRepo.ResolveMemoryAuthorization(ctx, botID, scopedomain.ScopeGroup, 980001)
	if err != nil || !authorized || eventID == 0 {
		t.Fatalf("resolve template event: event=%d authorized=%v err=%v", eventID, authorized, err)
	}
	conversations := make([]uuid.UUID, 0, 2)
	messages := make([]domain.ExtractionSourceMessage, 0, 2)
	for index, groupID := range []int64{980001, 980002} {
		conversationID := uuid.New()
		conversations = append(conversations, conversationID)
		if _, err := pool.Exec(ctx, `INSERT INTO conversations (id, bot_id, kind, target_id, last_message_at) VALUES ($1,$2,'group',$3,now())`, conversationID, botID, groupID); err != nil {
			t.Fatalf("seed conversation %d: %v", index, err)
		}
		var messageID int64
		if err := pool.QueryRow(ctx, `INSERT INTO messages (conversation_id, bot_id, direction, platform_message_id, sender_id, text_content, segments, memory_authorization_event_id) VALUES ($1,$2,'incoming',$3,997503,'boundary','[]'::jsonb,$4) RETURNING id`, conversationID, botID, time.Now().UnixNano()+int64(index), eventID).Scan(&messageID); err != nil {
			t.Fatalf("seed message %d: %v", index, err)
		}
		messages = append(messages, domain.ExtractionSourceMessage{ID: messageID, BotID: botID, ConversationID: conversationID.String(), ScopeType: "group", ScopeID: groupID, SenderID: 997503, Text: "boundary", AuthorizationEventID: eventID})
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
	for _, message := range messages {
		scope := domain.ExtractionScope{BotID: botID, ConversationID: message.ConversationID, ScopeType: "group", ScopeID: message.ScopeID, AuthorizationEventID: eventID}
		if _, err := repo.CreateExtractionJobs(ctx, scope, []domain.ExtractionSourceMessage{message}, 0); err != nil {
			t.Fatalf("create job: %v", err)
		}
	}
	batch, found, err := repo.ClaimReadyExtractionBatch(ctx, domain.ExtractionClaimOptions{MaxMessages: 10, MaxRunes: 4000, Lease: time.Minute})
	if err != nil || !found {
		t.Fatalf("claim first batch found=%v err=%v", found, err)
	}
	if len(batch.Jobs) != 1 || len(batch.Messages) != 1 {
		t.Fatalf("first batch jobs=%d messages=%d, want one conversation", len(batch.Jobs), len(batch.Messages))
	}
	firstConversation := batch.Messages[0].ConversationID
	if err := repo.CancelExtractionBatch(ctx, batch, "test_cleanup"); err != nil {
		t.Fatalf("cancel first batch: %v", err)
	}
	second, found, err := repo.ClaimReadyExtractionBatch(ctx, domain.ExtractionClaimOptions{MaxMessages: 10, MaxRunes: 4000, Lease: time.Minute})
	if err != nil || !found {
		t.Fatalf("claim second batch found=%v err=%v", found, err)
	}
	if len(second.Messages) != 1 || second.Messages[0].ConversationID == firstConversation {
		t.Fatalf("second batch = %+v, first conversation=%s", second.Messages, firstConversation)
	}
	_ = repo.CancelExtractionBatch(ctx, second, "test_cleanup")
}

// TestExtractionJobWaitsForBatchWindow verifies a single sparse message waits
// for MaxWait before it becomes claimable, then is processed as a partial batch.
// TestExtractionJobWaitsForBatchWindow 验证单条稀疏消息等待 MaxWait 后才可认领，
// 之后按不足批处理。
func TestExtractionJobWaitsForBatchWindow(t *testing.T) {
	pool := dbtest.Open(t)
	repo, err := NewRepo(pool)
	if err != nil {
		t.Fatalf("new repo: %v", err)
	}
	ctx := context.Background()
	const botID = 997401
	const groupID = 997402
	if _, err := pool.Exec(ctx, `INSERT INTO bot_accounts (bot_id, nickname) VALUES ($1, 'extraction-wait-test') ON CONFLICT DO NOTHING`, botID); err != nil {
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
		t.Fatalf("resolve event: event=%d authorized=%v err=%v", eventID, authorized, err)
	}
	var messageID int64
	if err := pool.QueryRow(ctx, `INSERT INTO messages (conversation_id, bot_id, direction, platform_message_id, sender_id, text_content, segments, memory_authorization_event_id) VALUES ($1,$2,'incoming',9974101,997403,'sparse','[]'::jsonb,$3) RETURNING id`, conversationID, botID, eventID).Scan(&messageID); err != nil {
		t.Fatalf("seed message: %v", err)
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
	if _, err := repo.CreateExtractionJobs(ctx, scope, []domain.ExtractionSourceMessage{{ID: messageID, BotID: botID, ConversationID: conversationID.String(), ScopeType: "group", ScopeID: groupID, SenderID: 997403, Text: "sparse", AuthorizationEventID: eventID}}, 150*time.Millisecond); err != nil {
		t.Fatalf("create job: %v", err)
	}
	if _, found, err := repo.ClaimReadyExtractionBatch(ctx, domain.ExtractionClaimOptions{MaxMessages: 10, MaxRunes: 4000, Lease: time.Minute}); err != nil || found {
		t.Fatalf("early claim found=%v err=%v, want not due", found, err)
	}
	time.Sleep(220 * time.Millisecond)
	batch, found, err := repo.ClaimReadyExtractionBatch(ctx, domain.ExtractionClaimOptions{MaxMessages: 10, MaxRunes: 4000, Lease: time.Minute})
	if err != nil || !found || len(batch.Messages) != 1 {
		t.Fatalf("late claim found=%v batch=%+v err=%v", found, batch, err)
	}
	_ = repo.CancelExtractionBatch(ctx, batch, "test_cleanup")
}

// TestFailExtractionBatchCountsFirstAttempt verifies max attempts includes the
// first model call: a one-attempt policy marks the job failed immediately.
// TestFailExtractionBatchCountsFirstAttempt 验证最大尝试次数包含首次调用：
// maxAttempts=1 时首次失败即置 failed。
func TestFailExtractionBatchCountsFirstAttempt(t *testing.T) {
	pool := dbtest.Open(t)
	repo, err := NewRepo(pool)
	if err != nil {
		t.Fatalf("new repo: %v", err)
	}
	ctx := context.Background()
	const botID = 997501
	const groupID = 997502
	if _, err := pool.Exec(ctx, `INSERT INTO bot_accounts (bot_id, nickname) VALUES ($1, 'extraction-fail-test') ON CONFLICT DO NOTHING`, botID); err != nil {
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
	eventID, _, err := scopeRepo.ResolveMemoryAuthorization(ctx, botID, scopedomain.ScopeGroup, groupID)
	if err != nil || eventID == 0 {
		t.Fatalf("resolve event: id=%d err=%v", eventID, err)
	}
	var messageID int64
	if err := pool.QueryRow(ctx, `INSERT INTO messages (conversation_id, bot_id, direction, platform_message_id, sender_id, text_content, segments, memory_authorization_event_id) VALUES ($1,$2,'incoming',9975001,997503,'失败测试','[]'::jsonb,$3) RETURNING id`, conversationID, botID, eventID).Scan(&messageID); err != nil {
		t.Fatalf("seed message: %v", err)
	}
	jobID := uuid.NewString()
	if _, err := pool.Exec(ctx, `INSERT INTO memory_extraction_jobs (id, bot_id, conversation_id, scope_type, scope_id, message_id, authorization_event_id, status, claim_token, lease_expires_at) VALUES ($1,$2,$3::uuid,'group',$4,$5,$6,'processing',$7,now()+interval '2 minutes')`, jobID, botID, conversationID, groupID, messageID, eventID, uuid.NewString()); err != nil {
		t.Fatalf("seed job: %v", err)
	}
	var claimToken string
	if err := pool.QueryRow(ctx, `SELECT claim_token::text FROM memory_extraction_jobs WHERE id=$1::uuid`, jobID).Scan(&claimToken); err != nil {
		t.Fatalf("read claim token: %v", err)
	}
	defer func() {
		_, _ = pool.Exec(ctx, `DELETE FROM memory_extraction_jobs WHERE bot_id=$1`, botID)
		_, _ = pool.Exec(ctx, `DELETE FROM messages WHERE bot_id=$1`, botID)
		_, _ = pool.Exec(ctx, `DELETE FROM conversations WHERE bot_id=$1`, botID)
		_, _ = pool.Exec(ctx, `DELETE FROM scope_policies WHERE bot_id=$1`, botID)
		_, _ = pool.Exec(ctx, `DELETE FROM scope_memory_authorization_events WHERE bot_id=$1`, botID)
		_, _ = pool.Exec(ctx, `DELETE FROM bot_accounts WHERE bot_id=$1`, botID)
	}()
	if err := repo.FailExtractionBatch(ctx, domain.ExtractionBatch{
		Scope:      domain.ExtractionScope{BotID: botID, ConversationID: conversationID.String(), ScopeType: "group", ScopeID: groupID, AuthorizationEventID: eventID},
		Jobs:       []domain.ExtractionJob{{ID: jobID}},
		ClaimToken: claimToken,
	}, "model_timeout", []time.Duration{time.Second}, 1); err != nil {
		t.Fatalf("fail batch: %v", err)
	}
	var status string
	var attempts int
	if err := pool.QueryRow(ctx, `SELECT status, attempts FROM memory_extraction_jobs WHERE id=$1::uuid`, jobID).Scan(&status, &attempts); err != nil {
		t.Fatalf("read job: %v", err)
	}
	if status != "failed" || attempts != 1 {
		t.Fatalf("job status=%s attempts=%d, want failed/1", status, attempts)
	}
}

// TestListAuthorizedScopesUsesRemovedGenerationAfterIndependentDelete verifies
// that deleting an independent policy and falling back to a template uses the
// new removed generation, matching ResolveMemoryAuthorization.
// TestListAuthorizedScopesUsesRemovedGenerationAfterIndependentDelete 验证独立策略
// 删除后回落到模板时使用新的 removed 代际，与 ResolveMemoryAuthorization 一致。
func TestListAuthorizedScopesUsesRemovedGenerationAfterIndependentDelete(t *testing.T) {
	pool := dbtest.Open(t)
	repo, err := NewRepo(pool)
	if err != nil {
		t.Fatalf("new repo: %v", err)
	}
	ctx := context.Background()
	const botID = 998001
	const groupID = 998002
	if _, err := pool.Exec(ctx, `INSERT INTO bot_accounts (bot_id, nickname) VALUES ($1, 'extraction-removed-scope-test') ON CONFLICT DO NOTHING`, botID); err != nil {
		t.Fatalf("seed bot: %v", err)
	}
	defer func() {
		_, _ = pool.Exec(ctx, `DELETE FROM memory_extraction_jobs WHERE bot_id=$1`, botID)
		_, _ = pool.Exec(ctx, `DELETE FROM messages WHERE bot_id=$1`, botID)
		_, _ = pool.Exec(ctx, `DELETE FROM conversations WHERE bot_id=$1`, botID)
		_, _ = pool.Exec(ctx, `DELETE FROM scope_policies WHERE bot_id=$1`, botID)
		_, _ = pool.Exec(ctx, `DELETE FROM scope_memory_authorization_events WHERE bot_id=$1`, botID)
		_, _ = pool.Exec(ctx, `DELETE FROM bot_accounts WHERE bot_id=$1`, botID)
	}()
	scopeRepo, err := scopeinfra.NewRepo(pool)
	if err != nil {
		t.Fatalf("new scope repo: %v", err)
	}
	template := scopedomain.Default(scopedomain.ScopeGroup, 0)
	template.Enabled = true
	template.StoreMessages = true
	template.AllowMemoryWrite = true
	if err := scopeRepo.Upsert(ctx, botID, template); err != nil {
		t.Fatalf("template upsert: %v", err)
	}
	independent := scopedomain.Default(scopedomain.ScopeGroup, groupID)
	independent.Enabled = true
	independent.StoreMessages = true
	independent.AllowMemoryWrite = false
	if err := scopeRepo.Upsert(ctx, botID, independent); err != nil {
		t.Fatalf("independent upsert: %v", err)
	}
	if _, err := scopeRepo.Delete(ctx, botID, scopedomain.ScopeGroup, groupID); err != nil {
		t.Fatalf("delete independent policy: %v", err)
	}
	removedEvent, authorized, err := scopeRepo.ResolveMemoryAuthorization(ctx, botID, scopedomain.ScopeGroup, groupID)
	if err != nil || !authorized || removedEvent == 0 {
		t.Fatalf("resolve removed generation: event=%d authorized=%v err=%v", removedEvent, authorized, err)
	}
	conversationID := uuid.New()
	if _, err := pool.Exec(ctx, `INSERT INTO conversations (id, bot_id, kind, target_id, last_message_at) VALUES ($1,$2,'group',$3,now())`, conversationID, botID, groupID); err != nil {
		t.Fatalf("seed conversation: %v", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO messages (conversation_id, bot_id, direction, platform_message_id, sender_id, text_content, segments, memory_authorization_event_id) VALUES ($1,$2,'incoming',$3,998003,'removed generation message','[]'::jsonb,$4)`, conversationID, botID, time.Now().UnixNano(), removedEvent); err != nil {
		t.Fatalf("seed message: %v", err)
	}
	scopes, err := repo.ListAuthorizedScopes(ctx, 100)
	if err != nil {
		t.Fatalf("list scopes: %v", err)
	}
	var found bool
	for _, scope := range scopes {
		if scope.BotID == botID && scope.ConversationID == conversationID.String() {
			found = true
			if scope.AuthorizationEventID != removedEvent {
				t.Fatalf("scope event=%d, want removed generation %d", scope.AuthorizationEventID, removedEvent)
			}
		}
	}
	if !found {
		t.Fatalf("conversation not found in authorized scopes; scopes=%+v", scopes)
	}
	messages, err := repo.ListUnregisteredMessages(ctx, botID, conversationID.String(), removedEvent, 10)
	if err != nil || len(messages) != 1 {
		t.Fatalf("removed-generation messages=%d err=%v, want 1", len(messages), err)
	}
}

// TestClaimMarksSourceMissingAndSkipsBlockedConversation verifies a purged
// source cannot keep its conversation at the head of the claim queue and is
// recorded as source_missing instead of remaining pending forever.
// TestClaimMarksSourceMissingAndSkipsBlockedConversation 验证来源被清理后不会占住
// 认领队首，并记录 source_missing，而不是永久保持 pending。
func TestClaimMarksSourceMissingAndSkipsBlockedConversation(t *testing.T) {
	pool := dbtest.Open(t)
	repo, err := NewRepo(pool)
	if err != nil {
		t.Fatalf("new repo: %v", err)
	}
	ctx := context.Background()
	const botID = 998101
	const blockedGroupID = 998102
	const readyGroupID = 998103
	if _, err := pool.Exec(ctx, `INSERT INTO bot_accounts (bot_id, nickname) VALUES ($1, 'extraction-source-missing-test') ON CONFLICT DO NOTHING`, botID); err != nil {
		t.Fatalf("seed bot: %v", err)
	}
	defer func() {
		_, _ = pool.Exec(ctx, `DELETE FROM memory_extraction_jobs WHERE bot_id=$1`, botID)
		_, _ = pool.Exec(ctx, `DELETE FROM messages WHERE bot_id=$1`, botID)
		_, _ = pool.Exec(ctx, `DELETE FROM conversations WHERE bot_id=$1`, botID)
		_, _ = pool.Exec(ctx, `DELETE FROM scope_policies WHERE bot_id=$1`, botID)
		_, _ = pool.Exec(ctx, `DELETE FROM scope_memory_authorization_events WHERE bot_id=$1`, botID)
		_, _ = pool.Exec(ctx, `DELETE FROM bot_accounts WHERE bot_id=$1`, botID)
	}()
	scopeRepo, err := scopeinfra.NewRepo(pool)
	if err != nil {
		t.Fatalf("new scope repo: %v", err)
	}
	template := scopedomain.Default(scopedomain.ScopeGroup, 0)
	template.Enabled = true
	template.StoreMessages = true
	template.AllowMemoryWrite = true
	if err := scopeRepo.Upsert(ctx, botID, template); err != nil {
		t.Fatalf("template upsert: %v", err)
	}
	eventID, authorized, err := scopeRepo.ResolveMemoryAuthorization(ctx, botID, scopedomain.ScopeGroup, blockedGroupID)
	if err != nil || !authorized || eventID == 0 {
		t.Fatalf("resolve event: id=%d authorized=%v err=%v", eventID, authorized, err)
	}
	type seeded struct {
		conversationID uuid.UUID
		groupID        int64
		messageID      int64
	}
	seededConversations := make([]seeded, 0, 2)
	for index, item := range []struct {
		groupID int64
		text    string
		oldest  bool
	}{
		{blockedGroupID, "blocked source will be deleted", true},
		{readyGroupID, "ready source", false},
	} {
		conversationID := uuid.New()
		lastMessageAt := "now()"
		if item.oldest {
			lastMessageAt = "now() - interval '1 hour'"
		}
		if _, err := pool.Exec(ctx, `INSERT INTO conversations (id, bot_id, kind, target_id, last_message_at) VALUES ($1,$2,'group',$3,`+lastMessageAt+`)`, conversationID, botID, item.groupID); err != nil {
			t.Fatalf("seed conversation %d: %v", index, err)
		}
		var messageID int64
		if err := pool.QueryRow(ctx, `INSERT INTO messages (conversation_id, bot_id, direction, platform_message_id, sender_id, text_content, segments, memory_authorization_event_id) VALUES ($1,$2,'incoming',$3,998103,$4,'[]'::jsonb,$5) RETURNING id`, conversationID, botID, time.Now().UnixNano()+int64(index), item.text, eventID).Scan(&messageID); err != nil {
			t.Fatalf("seed message %d: %v", index, err)
		}
		scope := domain.ExtractionScope{BotID: botID, ConversationID: conversationID.String(), ScopeType: "group", ScopeID: item.groupID, AuthorizationEventID: eventID}
		created, err := repo.CreateExtractionJobs(ctx, scope, []domain.ExtractionSourceMessage{{ID: messageID, BotID: botID, ConversationID: conversationID.String(), ScopeType: "group", ScopeID: item.groupID, SenderID: 998103, Text: item.text, AuthorizationEventID: eventID}}, 0)
		if err != nil || created != 1 {
			t.Fatalf("create job %d: created=%d err=%v", index, created, err)
		}
		seededConversations = append(seededConversations, seeded{conversationID: conversationID, groupID: item.groupID, messageID: messageID})
	}
	if _, err := pool.Exec(ctx, `DELETE FROM messages WHERE id=$1`, seededConversations[0].messageID); err != nil {
		t.Fatalf("delete blocked message: %v", err)
	}
	batch, found, err := repo.ClaimReadyExtractionBatch(ctx, domain.ExtractionClaimOptions{MaxMessages: 10, MaxRunes: 4000, Lease: time.Minute})
	if err != nil || !found {
		t.Fatalf("claim batch found=%v err=%v", found, err)
	}
	if len(batch.Messages) != 1 || batch.Messages[0].ConversationID != seededConversations[1].conversationID.String() {
		t.Fatalf("claimed batch=%+v, want ready conversation %s", batch, seededConversations[1].conversationID)
	}
	var status, code string
	if err := pool.QueryRow(ctx, `SELECT status, COALESCE(last_error_code, '') FROM memory_extraction_jobs WHERE bot_id=$1 AND conversation_id=$2::uuid`, botID, seededConversations[0].conversationID).Scan(&status, &code); err != nil {
		t.Fatalf("read blocked job: %v", err)
	}
	if status != "failed" || code != "source_missing" {
		t.Fatalf("blocked job status=%s code=%s, want failed/source_missing", status, code)
	}
	_ = repo.CancelExtractionBatch(ctx, batch, "test_cleanup")
}
