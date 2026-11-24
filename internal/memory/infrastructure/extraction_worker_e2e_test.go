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
			Content:         message.Text,
			Confidence:      0.9,
			SourceMessageID: message.ID,
			Subject:         "sender",
		})
	}
	return out, nil
}

// stubExtractionPromptResolver freezes a deterministic prompt snapshot for the
// worker end-to-end tests without touching the live prompt service.
// stubExtractionPromptResolver 为工作器端到端测试固定确定性的 Prompt 快照，
// 不访问线上 Prompt 服务。
type stubExtractionPromptResolver struct{}

func (stubExtractionPromptResolver) Resolve(_ context.Context, request domain.ExtractionRequest) (domain.ExtractionPrompt, error) {
	return domain.ExtractionPrompt{
		Snapshot: domain.PromptSnapshot{
			PromptID: "memory.extraction", Kind: "memory_extraction", Provider: "openai",
			BotID: request.Scope.BotID, TargetType: request.Scope.ScopeType, TargetID: request.Scope.ScopeID,
			ConversationID: request.Scope.ConversationID,
			FinalHash:      "stub-final-hash", RenderedHash: "stub-rendered-hash",
			Layers: []domain.PromptSnapshotLayer{{Layer: "account_default", VersionID: 1, Version: 1, Source: "builtin", Hash: "stub-layer-hash"}},
		},
		SystemPrompt: extractionSystemPrompt,
		UserTemplate: "聊天消息：\n{{messages_json}}",
	}, nil
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
	policy.AllowMemoryRead = true
	if err := scopeRepo.Upsert(ctx, botID, policy); err != nil {
		t.Fatalf("seed policy: %v", err)
	}
	eventID, authorized, err := scopeRepo.ResolveMemoryAuthorization(ctx, botID, scopedomain.ScopeGroup, groupID)
	if err != nil || !authorized || eventID == 0 {
		t.Fatalf("resolve authorization: event=%d authorized=%v err=%v", eventID, authorized, err)
	}
	for _, text := range []string{"我喜欢乌龙茶", "我喜欢爬山"} {
		if _, err := pool.Exec(ctx, `INSERT INTO messages (conversation_id, bot_id, direction, platform_message_id, sender_id, text_content, segments, memory_authorization_event_id) VALUES ($1,$2,'incoming',$3,997203,$4,jsonb_build_array(jsonb_build_object('type','text','data',jsonb_build_object('text',$4::text))),$5)`, conversationID, botID, time.Now().UnixNano(), text, eventID); err != nil {
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
		PromptResolver:   stubExtractionPromptResolver{},
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
	memoryService.SetReadPolicies(scopeService)
	var memoryID, subjectID string
	if err := pool.QueryRow(ctx, `SELECT memory_id::text, subject_id FROM memories WHERE bot_id=$1 AND status='candidate' ORDER BY created_at LIMIT 1`, botID).Scan(&memoryID, &subjectID); err != nil {
		t.Fatalf("read candidate: %v", err)
	}
	req := memorycontracts.RecallRequest{BotID: botID, Kind: "group", TargetID: groupID, Query: "喜欢", Limit: 5, SubjectType: "user", SubjectID: subjectID}
	before, err := memoryService.Recall(ctx, req)
	if err != nil || len(before.Items) != 0 {
		t.Fatalf("candidate recall before approval = %d err=%v", len(before.Items), err)
	}
	if err := memoryService.Approve(ctx, memoryID, "admin"); err != nil {
		t.Fatalf("approve candidate: %v", err)
	}
	after, err := memoryService.Recall(ctx, req)
	if err != nil || len(after.Items) != 1 || !strings.Contains(after.Items[0].Content, "本人自述：我喜欢") {
		t.Fatalf("approved recall = %+v err=%v", after, err)
	}
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
