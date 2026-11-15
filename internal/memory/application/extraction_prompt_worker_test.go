package application

import (
	"context"
	"errors"
	"testing"

	"github.com/HappyLadySauceM/Nagi/internal/memory/domain"
	scopecontracts "github.com/HappyLadySauceM/Nagi/internal/scope/contracts"
)

type fakeExtractionPromptResolver struct {
	resolved domain.ExtractionPrompt
	err      error
	calls    int
	seen     *domain.PromptSnapshot
}

func (f *fakeExtractionPromptResolver) Resolve(_ context.Context, request domain.ExtractionRequest) (domain.ExtractionPrompt, error) {
	f.calls++
	f.seen = request.PromptSnapshot
	if f.err != nil {
		return domain.ExtractionPrompt{}, f.err
	}
	if request.PromptSnapshot != nil {
		return domain.ExtractionPrompt{
			Snapshot:     *request.PromptSnapshot,
			SystemPrompt: "snapshot system",
			UserTemplate: "snapshot user {{messages_json}}",
		}, nil
	}
	return f.resolved, nil
}

func testPromptSnapshot() domain.PromptSnapshot {
	return domain.PromptSnapshot{
		PromptID: "memory.extraction", Kind: "memory_extraction", Provider: "openai",
		BotID: 1, TargetType: "group", TargetID: 7, ConversationID: "conv",
		FinalHash: "final-hash", RenderedHash: "rendered-hash",
		Layers: []domain.PromptSnapshotLayer{{Layer: "account_default", VersionID: 10, Version: 1, Source: "local", Hash: "layer-hash"}},
	}
}

func TestExtractionWorkerPersistsPromptSnapshotBeforeExtract(t *testing.T) {
	repo := &fakeExtractionRepo{}
	extractor := &fakeExtractor{}
	resolver := &fakeExtractionPromptResolver{resolved: domain.ExtractionPrompt{
		Snapshot: testPromptSnapshot(), SystemPrompt: "system", UserTemplate: "user {{messages_json}}",
	}}
	worker := newTestExtractionWorker(t, repo, extractor, fakeMemoryAuthorizer{auth: scopecontracts.MemoryAuthorization{EventID: 3, Authorized: true}})
	worker.opts.PromptResolver = resolver
	worker.processBatch(context.Background(), domain.ExtractionBatch{
		Scope:    domain.ExtractionScope{BotID: 1, ConversationID: "conv", ScopeType: "group", ScopeID: 7, AuthorizationEventID: 3},
		Jobs:     []domain.ExtractionJob{{ID: "job"}},
		Messages: []domain.ExtractionSourceMessage{{ID: 11, BotID: 1, ConversationID: "conv", ScopeType: "group", ScopeID: 7, SenderID: 42, AuthorizationEventID: 3}},
	})
	if len(repo.persistedSnapshots) != 1 || extractor.calls != 1 || extractor.lastRequest == nil || extractor.lastRequest.Prompt == nil {
		t.Fatalf("snapshot persisted=%d extractor calls=%d request=%+v", len(repo.persistedSnapshots), extractor.calls, extractor.lastRequest)
	}
	if extractor.lastRequest.Prompt.SystemPrompt != "system" {
		t.Fatalf("extractor prompt = %#v", extractor.lastRequest.Prompt)
	}
}

func TestExtractionWorkerReusesStoredPromptSnapshotOnRetry(t *testing.T) {
	repo := &fakeExtractionRepo{}
	extractor := &fakeExtractor{}
	resolver := &fakeExtractionPromptResolver{}
	worker := newTestExtractionWorker(t, repo, extractor, fakeMemoryAuthorizer{auth: scopecontracts.MemoryAuthorization{EventID: 3, Authorized: true}})
	worker.opts.PromptResolver = resolver
	snapshot := testPromptSnapshot()
	worker.processBatch(context.Background(), domain.ExtractionBatch{
		Scope:          domain.ExtractionScope{BotID: 1, ConversationID: "conv", ScopeType: "group", ScopeID: 7, AuthorizationEventID: 3},
		Jobs:           []domain.ExtractionJob{{ID: "job", PromptSnapshot: &snapshot}},
		Messages:       []domain.ExtractionSourceMessage{{ID: 11, BotID: 1, ConversationID: "conv", ScopeType: "group", ScopeID: 7, SenderID: 42, AuthorizationEventID: 3}},
		PromptSnapshot: &snapshot,
	})
	if resolver.calls != 1 || resolver.seen == nil || len(repo.persistedSnapshots) != 0 {
		t.Fatalf("retry resolver calls=%d seen=%v persisted=%d", resolver.calls, resolver.seen, len(repo.persistedSnapshots))
	}
	if extractor.calls != 1 || extractor.lastRequest == nil || extractor.lastRequest.Prompt == nil {
		t.Fatalf("retry extractor calls=%d request=%+v", extractor.calls, extractor.lastRequest)
	}
}

func TestExtractionWorkerFailsWhenPromptSnapshotCannotPersist(t *testing.T) {
	repo := &fakeExtractionRepo{persistErr: errors.New("write failed")}
	extractor := &fakeExtractor{}
	resolver := &fakeExtractionPromptResolver{resolved: domain.ExtractionPrompt{Snapshot: testPromptSnapshot(), SystemPrompt: "system", UserTemplate: "user {{messages_json}}"}}
	worker := newTestExtractionWorker(t, repo, extractor, fakeMemoryAuthorizer{auth: scopecontracts.MemoryAuthorization{EventID: 3, Authorized: true}})
	worker.opts.PromptResolver = resolver
	worker.processBatch(context.Background(), domain.ExtractionBatch{
		Scope:    domain.ExtractionScope{BotID: 1, ConversationID: "conv", ScopeType: "group", ScopeID: 7, AuthorizationEventID: 3},
		Jobs:     []domain.ExtractionJob{{ID: "job"}},
		Messages: []domain.ExtractionSourceMessage{{ID: 11, BotID: 1, ConversationID: "conv", ScopeType: "group", ScopeID: 7, SenderID: 42, AuthorizationEventID: 3}},
	})
	if repo.failed != "prompt_snapshot_failed" || extractor.calls != 0 {
		t.Fatalf("failed=%q extractor calls=%d", repo.failed, extractor.calls)
	}
}

// TestExtractionWorkerFailsWhenPromptResolverUnavailable verifies a worker
// without a prompt resolver fails the batch before the model call instead of
// silently using the currently published prompt.
// TestExtractionWorkerFailsWhenPromptResolverUnavailable 验证缺少 Prompt 解析器时
// 批次在模型调用前明确失败，不静默使用当前发布 Prompt。
func TestExtractionWorkerFailsWhenPromptResolverUnavailable(t *testing.T) {
	repo := &fakeExtractionRepo{}
	extractor := &fakeExtractor{}
	worker := newTestExtractionWorker(t, repo, extractor, fakeMemoryAuthorizer{auth: scopecontracts.MemoryAuthorization{EventID: 3, Authorized: true}})
	worker.opts.PromptResolver = nil
	worker.processBatch(context.Background(), domain.ExtractionBatch{
		Scope:    domain.ExtractionScope{BotID: 1, ConversationID: "conv", ScopeType: "group", ScopeID: 7, AuthorizationEventID: 3},
		Jobs:     []domain.ExtractionJob{{ID: "job"}},
		Messages: []domain.ExtractionSourceMessage{{ID: 11, BotID: 1, ConversationID: "conv", ScopeType: "group", ScopeID: 7, SenderID: 42, AuthorizationEventID: 3}},
	})
	if repo.failed != "prompt_snapshot_unavailable" || extractor.calls != 0 {
		t.Fatalf("failed=%q extractor calls=%d", repo.failed, extractor.calls)
	}
}
