package infrastructure

import (
	"context"
	"database/sql"
	"io"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	memoryapp "github.com/HappyLadySauceM/Nagi/internal/memory/application"
	memorycontracts "github.com/HappyLadySauceM/Nagi/internal/memory/contracts"
	"github.com/HappyLadySauceM/Nagi/internal/memory/domain"
	scopeapp "github.com/HappyLadySauceM/Nagi/internal/scope/application"
	scopedomain "github.com/HappyLadySauceM/Nagi/internal/scope/domain"
	scopeinfra "github.com/HappyLadySauceM/Nagi/internal/scope/infrastructure"
	"github.com/HappyLadySauceM/Nagi/pkg/db/dbtest"
)

type recordingExtractor struct {
	mu       sync.Mutex
	requests []domain.ExtractionRequest
}

func (r *recordingExtractor) Extract(_ context.Context, request domain.ExtractionRequest) ([]domain.ExtractionCandidate, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.requests = append(r.requests, request)
	out := make([]domain.ExtractionCandidate, 0, len(request.Messages))
	for _, message := range request.Messages {
		out = append(out, domain.ExtractionCandidate{
			Content:         "记忆:" + message.Text,
			Confidence:      0.9,
			SourceMessageID: message.ID,
			Subject:         "sender",
		})
	}
	return out, nil
}

// TestExtractionWorkerEndToEndWithRealPostgres verifies bounded batching from
// real stored messages through one model call, candidate persistence, and the
// approval gate on recall.
// TestExtractionWorkerEndToEndWithRealPostgres 验证真实存储消息经有界合批、一次模型调用、
// 候选落库，以及批准前不可召回、批准后可读的完整链路。
func TestExtractionWorkerEndToEndWithRealPostgres(t *testing.T) {
	pool := dbtest.Open(t)
	repo, err := NewRepo(pool)
	if err != nil {
		t.Fatalf("new repo: %v", err)
	}
	ctx := context.Background()
	const botID = 997201
	const groupID = 997202
	if _, err := pool.Exec(ctx, `INSERT INTO bot_accounts (bot_id, nickname) VALUES ($1, 'extraction-e2e-test') ON CONFLICT DO NOTHING`, botID); err != nil {
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
	for _, text := range []string{"用户A喜欢乌龙茶", "用户A周末喜欢爬山"} {
		if _, err := pool.Exec(ctx, `INSERT INTO messages (conversation_id, bot_id, direction, platform_message_id, sender_id, text_content, segments, memory_authorization_event_id) VALUES ($1,$2,'incoming',$3,997203,$4,'[]'::jsonb,$5)`, conversationID, botID, time.Now().UnixNano(), text, eventID); err != nil {
			t.Fatalf("seed message: %v", err)
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

	extractor := &recordingExtractor{}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	worker, err := memoryapp.NewExtractionWorker(repo, extractor, scopeService, memoryapp.ExtractionWorkerOptions{
		Enabled:          func() bool { return true },
		Concurrency:      1,
		BatchMaxMessages: 10,
		BatchMaxRunes:    4000,
		MaxWait:          0,
		PollInterval:     10 * time.Millisecond,
		MaxAttempts:      3,
		RetryDelays:      []time.Duration{30 * time.Millisecond, 60 * time.Millisecond},
		Lease:            time.Second,
	}, logger)
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
	extractor.mu.Lock()
	if len(extractor.requests) != 1 || len(extractor.requests[0].Messages) != 2 {
		t.Fatalf("extractor requests = %+v, want one request with two messages", extractor.requests)
	}
	if extractor.requests[0].MaxRunes != 4000 {
		t.Fatalf("max runes = %d, want 4000", extractor.requests[0].MaxRunes)
	}
	extractor.mu.Unlock()

	memoryService, err := memoryapp.NewService(repo)
	if err != nil {
		t.Fatalf("new memory service: %v", err)
	}
	before, err := memoryService.Recall(ctx, memoryContractsRecall(botID, groupID))
	if err != nil || len(before) != 0 {
		t.Fatalf("candidate recall before approval = %d err=%v", len(before), err)
	}
	var memoryID string
	if err := pool.QueryRow(ctx, `SELECT memory_id::text FROM memories WHERE bot_id=$1 AND status='candidate' ORDER BY created_at LIMIT 1`, botID).Scan(&memoryID); err != nil {
		t.Fatalf("read candidate: %v", err)
	}
	if err := memoryService.Approve(ctx, memoryID, "admin"); err != nil {
		t.Fatalf("approve candidate: %v", err)
	}
	after, err := memoryService.Recall(ctx, memoryContractsRecall(botID, groupID))
	if err != nil || len(after) != 1 || !strings.Contains(after[0].Content, "记忆:") {
		t.Fatalf("approved recall = %+v err=%v", after, err)
	}
}

func memoryContractsRecall(botID, groupID int64) memorycontracts.RecallRequest {
	return memorycontracts.RecallRequest{BotID: botID, Kind: "group", TargetID: groupID, Limit: 5}
}

func waitForExtractionCount(t *testing.T, pool interface {
	QueryRow(context.Context, string, ...any) *sql.Row
}, botID int64, query string, want int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		var got int
		if err := pool.QueryRow(context.Background(), query, botID).Scan(&got); err == nil && got == want {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("extraction condition not met: %s", query)
}
