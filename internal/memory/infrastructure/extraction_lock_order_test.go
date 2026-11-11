package infrastructure

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	conversationdomain "github.com/HappyLadySauceM/Nagi/internal/conversation/domain"
	conversationinfra "github.com/HappyLadySauceM/Nagi/internal/conversation/infrastructure"
	"github.com/HappyLadySauceM/Nagi/internal/memory/domain"
	scopeinfra "github.com/HappyLadySauceM/Nagi/internal/scope/infrastructure"
	"github.com/HappyLadySauceM/Nagi/pkg/db"
)

// TestExtractionClaimLocksConversationBeforeAuthorization verifies the claim
// path does not hold the memory-authorization advisory lock while waiting for
// the conversation row. Ingest takes the conversation row first and then the
// same authorization lock, so the reverse order deadlocks with an active
// inbound message transaction.
// TestExtractionClaimLocksConversationBeforeAuthorization 验证认领路径不会在等待
// 会话行时持有记忆授权 advisory lock；Ingest 先锁会话行再取同一授权锁，反向顺序
// 会与正在提交的入站消息事务互相死锁。
func TestExtractionClaimLocksConversationBeforeAuthorization(t *testing.T) {
	pool, repo, scope, messageID := seedExtractionBoundaryFixture(t, 997601, 997602)
	ctx := context.Background()
	if _, err := repo.CreateExtractionJobs(ctx, scope, []domain.ExtractionSourceMessage{{
		ID: messageID, BotID: scope.BotID, ConversationID: scope.ConversationID,
		ScopeType: scope.ScopeType, ScopeID: scope.ScopeID, SenderID: 997503,
		Text: "锁序测试", AuthorizationEventID: scope.AuthorizationEventID,
	}}, 0); err != nil {
		t.Fatalf("create job: %v", err)
	}
	conversationID, err := uuid.Parse(scope.ConversationID)
	if err != nil {
		t.Fatalf("parse conversation: %v", err)
	}
	holder, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin holder: %v", err)
	}
	defer holder.Rollback(ctx)
	if _, err := holder.Exec(ctx, `SELECT id FROM conversations WHERE id=$1 FOR UPDATE`, conversationID); err != nil {
		t.Fatalf("lock conversation: %v", err)
	}

	type claimResult struct {
		found bool
		err   error
	}
	done := make(chan claimResult, 1)
	go func() {
		_, found, claimErr := repo.ClaimReadyExtractionBatch(ctx, domain.ExtractionClaimOptions{MaxMessages: 10, MaxRunes: 4000, Lease: time.Minute})
		done <- claimResult{found: found, err: claimErr}
	}()
	time.Sleep(150 * time.Millisecond)
	assertAuthorizationLocksFree(t, ctx, pool, scope.BotID, scope.ScopeType, scope.ScopeID)
	if err := holder.Rollback(ctx); err != nil {
		t.Fatalf("release conversation: %v", err)
	}
	select {
	case got := <-done:
		if got.err != nil {
			t.Fatalf("claim after release: %v", got.err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("claim did not finish after conversation release")
	}
}

// TestExtractionCreateJobsLocksConversationBeforeAuthorization verifies the
// scanner's job-creation transaction follows the same conversation ->
// authorization order as Ingest.
// TestExtractionCreateJobsLocksConversationBeforeAuthorization 验证扫描建任务事务
// 与 Ingest 一样遵循 conversation -> authorization 顺序。
func TestExtractionCreateJobsLocksConversationBeforeAuthorization(t *testing.T) {
	pool, repo, scope, messageID := seedExtractionBoundaryFixture(t, 997611, 997612)
	ctx := context.Background()
	conversationID, err := uuid.Parse(scope.ConversationID)
	if err != nil {
		t.Fatalf("parse conversation: %v", err)
	}
	holder, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin holder: %v", err)
	}
	defer holder.Rollback(ctx)
	if _, err := holder.Exec(ctx, `SELECT id FROM conversations WHERE id=$1 FOR UPDATE`, conversationID); err != nil {
		t.Fatalf("lock conversation: %v", err)
	}
	done := make(chan error, 1)
	go func() {
		_, createErr := repo.CreateExtractionJobs(ctx, scope, []domain.ExtractionSourceMessage{{
			ID: messageID, BotID: scope.BotID, ConversationID: scope.ConversationID,
			ScopeType: scope.ScopeType, ScopeID: scope.ScopeID, SenderID: 997503,
			Text: "锁序测试", AuthorizationEventID: scope.AuthorizationEventID,
		}}, 0)
		done <- createErr
	}()
	time.Sleep(150 * time.Millisecond)
	assertAuthorizationLocksFree(t, ctx, pool, scope.BotID, scope.ScopeType, scope.ScopeID)
	if err := holder.Rollback(ctx); err != nil {
		t.Fatalf("release conversation: %v", err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("create jobs after release: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("create jobs did not finish after conversation release")
	}
}

// TestIngestAndExtractionClaimConcurrent exercises the real message-ingest
// transaction against the extraction claim path on the same conversation. The
// bounded context fails the test if the two transactions deadlock.
// TestIngestAndExtractionClaimConcurrent 在同一会话上并发执行真实消息入库和提取认领；
// 若两个事务死锁，有界 context 会让用例失败。
func TestIngestAndExtractionClaimConcurrent(t *testing.T) {
	pool, repo, scope, messageID := seedExtractionBoundaryFixture(t, 997621, 997622)
	ctx := context.Background()
	scopeRepo, err := scopeinfra.NewRepo(pool)
	if err != nil {
		t.Fatalf("new scope repo: %v", err)
	}
	conversationRepo, err := conversationinfra.NewRepo(pool, conversationinfra.WithMemoryAuthorizationStamper(scopeRepo))
	if err != nil {
		t.Fatalf("new conversation repo: %v", err)
	}
	if _, err := repo.CreateExtractionJobs(ctx, scope, []domain.ExtractionSourceMessage{{
		ID: messageID, BotID: scope.BotID, ConversationID: scope.ConversationID,
		ScopeType: scope.ScopeType, ScopeID: scope.ScopeID, SenderID: 997503,
		Text: "并发锁序测试", AuthorizationEventID: scope.AuthorizationEventID,
	}}, 0); err != nil {
		t.Fatalf("create job: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM message_jobs WHERE incoming_message_id IN (SELECT id FROM messages WHERE bot_id=$1)`, scope.BotID)
	})
	runCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	start := make(chan struct{})
	var wg sync.WaitGroup
	ingestErr := make(chan error, 1)
	claimErr := make(chan error, 1)
	wg.Add(2)
	go func() {
		defer wg.Done()
		<-start
		_, err := conversationRepo.Ingest(runCtx, conversationdomain.IngestInput{
			BotID: scope.BotID, Kind: conversationdomain.KindGroup, TargetID: scope.ScopeID,
			PlatformMessageID: time.Now().UnixNano(), SenderID: 997503, Text: "并发入库",
		})
		ingestErr <- err
	}()
	go func() {
		defer wg.Done()
		<-start
		_, _, err := repo.ClaimReadyExtractionBatch(runCtx, domain.ExtractionClaimOptions{MaxMessages: 10, MaxRunes: 4000, Lease: time.Minute})
		claimErr <- err
	}()
	close(start)
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
		if err := <-ingestErr; err != nil {
			t.Fatalf("concurrent ingest: %v", err)
		}
		if err := <-claimErr; err != nil {
			t.Fatalf("concurrent claim: %v", err)
		}
	case <-runCtx.Done():
		t.Fatal("concurrent ingest/claim did not finish before context deadline")
	}
}

// TestIngestAndCreateExtractionJobsConcurrent exercises real ingest against
// scanner job creation on one conversation with a bounded context.
// TestIngestAndCreateExtractionJobsConcurrent 在同一会话上并发执行真实消息入库和
// 扫描建任务；用有界 context 捕获死锁。
func TestIngestAndCreateExtractionJobsConcurrent(t *testing.T) {
	pool, repo, scope, messageID := seedExtractionBoundaryFixture(t, 997631, 997632)
	ctx := context.Background()
	scopeRepo, err := scopeinfra.NewRepo(pool)
	if err != nil {
		t.Fatalf("new scope repo: %v", err)
	}
	conversationRepo, err := conversationinfra.NewRepo(pool, conversationinfra.WithMemoryAuthorizationStamper(scopeRepo))
	if err != nil {
		t.Fatalf("new conversation repo: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM message_jobs WHERE incoming_message_id IN (SELECT id FROM messages WHERE bot_id=$1)`, scope.BotID)
	})
	runCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	start := make(chan struct{})
	var wg sync.WaitGroup
	ingestErr := make(chan error, 1)
	createErr := make(chan error, 1)
	wg.Add(2)
	go func() {
		defer wg.Done()
		<-start
		_, err := conversationRepo.Ingest(runCtx, conversationdomain.IngestInput{
			BotID: scope.BotID, Kind: conversationdomain.KindGroup, TargetID: scope.ScopeID,
			PlatformMessageID: time.Now().UnixNano(), SenderID: 997503, Text: "并发入库",
		})
		ingestErr <- err
	}()
	go func() {
		defer wg.Done()
		<-start
		_, err := repo.CreateExtractionJobs(runCtx, scope, []domain.ExtractionSourceMessage{{
			ID: messageID, BotID: scope.BotID, ConversationID: scope.ConversationID,
			ScopeType: scope.ScopeType, ScopeID: scope.ScopeID, SenderID: 997503,
			Text: "并发建任务", AuthorizationEventID: scope.AuthorizationEventID,
		}}, 0)
		createErr <- err
	}()
	close(start)
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
		if err := <-ingestErr; err != nil {
			t.Fatalf("concurrent ingest: %v", err)
		}
		if err := <-createErr; err != nil {
			t.Fatalf("concurrent create jobs: %v", err)
		}
	case <-runCtx.Done():
		t.Fatal("concurrent ingest/create jobs did not finish before context deadline")
	}
}

func assertAuthorizationLocksFree(t *testing.T, ctx context.Context, pool *db.Pool, botID int64, scopeType string, scopeID int64) {
	t.Helper()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin authorization probe: %v", err)
	}
	defer tx.Rollback(ctx)
	for _, key := range []string{
		fmt.Sprintf("memory-auth:%d:%s:%d", botID, scopeType, scopeID),
		fmt.Sprintf("memory-auth:%d:%s:0", botID, scopeType),
	} {
		var free bool
		if err := tx.QueryRow(ctx, `SELECT pg_try_advisory_xact_lock(hashtextextended($1,0))`, key).Scan(&free); err != nil {
			t.Fatalf("probe authorization lock %s: %v", key, err)
		}
		if !free {
			t.Fatalf("auth lock %s is held while the extraction transaction waits for the conversation row", key)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit authorization probe: %v", err)
	}
}
