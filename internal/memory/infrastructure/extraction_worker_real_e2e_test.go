package infrastructure

import (
	"context"
	"io"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"

	memoryapp "github.com/HappyLadySauceM/Nagi/internal/memory/application"
	scopeapp "github.com/HappyLadySauceM/Nagi/internal/scope/application"
	scopedomain "github.com/HappyLadySauceM/Nagi/internal/scope/domain"
	scopeinfra "github.com/HappyLadySauceM/Nagi/internal/scope/infrastructure"
	"github.com/HappyLadySauceM/Nagi/pkg/db/dbtest"
)

// TestExtractionWorkerRealModelEndToEnd exercises one real model call over
// synthetic stored messages and verifies the resulting candidate rows reach
// the real _test PostgreSQL database. It is opt-in and never sends QQ messages.
// TestExtractionWorkerRealModelEndToEnd 使用合成消息执行一次真实模型调用，并验证
// 候选写入真实 _test PostgreSQL；该用例显式开启，不发送 QQ 消息。
func TestExtractionWorkerRealModelEndToEnd(t *testing.T) {
	if os.Getenv("NAGI_REAL_MODEL") != "1" {
		t.Skip("set NAGI_REAL_MODEL=1 to run the real model end-to-end probe")
	}
	pool := dbtest.Open(t)
	repo, err := NewRepo(pool)
	if err != nil {
		t.Fatalf("new repo: %v", err)
	}
	ctx := context.Background()
	const botID = 997601
	const groupID = 997602
	if _, err := pool.Exec(ctx, `INSERT INTO bot_accounts (bot_id, nickname) VALUES ($1, 'extraction-real-model-test') ON CONFLICT DO NOTHING`, botID); err != nil {
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
	scopeService, err := scopeapp.NewService(scopeRepo)
	if err != nil {
		t.Fatalf("new scope service: %v", err)
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
	for index, text := range []string{"用户A喜欢乌龙茶", "用户B周末喜欢爬山"} {
		if _, err := pool.Exec(ctx, `INSERT INTO messages (conversation_id, bot_id, direction, platform_message_id, sender_id, text_content, segments, memory_authorization_event_id) VALUES ($1,$2,'incoming',$3,$4,$5,'[]'::jsonb,$6)`, conversationID, botID, time.Now().UnixNano()+int64(index), 997603+int64(index), text, eventID); err != nil {
			t.Fatalf("seed message %d: %v", index, err)
		}
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

	worker, err := memoryapp.NewExtractionWorker(repo, realModelExtractorFromConfig(t), scopeService, memoryapp.ExtractionWorkerOptions{
		Enabled:          func() bool { return true },
		Concurrency:      1,
		BatchMaxMessages: 10,
		BatchMaxRunes:    4000,
		MaxWait:          0,
		PollInterval:     10 * time.Millisecond,
		MaxAttempts:      2,
		RetryDelays:      []time.Duration{time.Second},
		Lease:            30 * time.Second,
	}, testLogger())
	if err != nil {
		t.Fatalf("new worker: %v", err)
	}
	runCtx, cancelRun := context.WithCancel(ctx)
	runDone := make(chan error, 1)
	go func() { runDone <- worker.Run(runCtx) }()
	defer func() {
		cancelRun()
		select {
		case err := <-runDone:
			if err != nil {
				t.Errorf("worker run: %v", err)
			}
		case <-time.After(2 * time.Second):
			t.Error("worker did not stop")
		}
	}()

	waitForExtractionCount(t, pool, botID, `SELECT count(*) FROM memory_extraction_jobs WHERE bot_id=$1 AND status='done'`, 2)
	var candidates int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM memories WHERE bot_id=$1 AND status='candidate' AND origin='extraction' AND source_message_id IS NOT NULL AND content <> ''`, botID).Scan(&candidates); err != nil {
		t.Fatalf("read candidates: %v", err)
	}
	if candidates == 0 {
		t.Fatal("real model produced no persisted candidate")
	}
	t.Logf("real model persisted %d candidate(s)", candidates)
}

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}
