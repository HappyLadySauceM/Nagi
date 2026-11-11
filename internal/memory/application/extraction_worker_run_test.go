package application

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/HappyLadySauceM/Nagi/internal/memory/domain"
	scopecontracts "github.com/HappyLadySauceM/Nagi/internal/scope/contracts"
	scopedomain "github.com/HappyLadySauceM/Nagi/internal/scope/domain"
)

type fakeExtractionRepo struct {
	scopes      []domain.ExtractionScope
	messages    []domain.ExtractionSourceMessage
	batch       domain.ExtractionBatch
	created     int
	createdWait time.Duration
	finalized   [][]domain.Memory
	cancelled   string
	failed      string
	scopesCalls int
	claimCalls  int
}

func (f *fakeExtractionRepo) ListAuthorizedScopes(context.Context, int) ([]domain.ExtractionScope, error) {
	f.scopesCalls++
	return f.scopes, nil
}

func (f *fakeExtractionRepo) ListUnregisteredMessages(context.Context, int64, string, int64, int) ([]domain.ExtractionSourceMessage, error) {
	return f.messages, nil
}

func (f *fakeExtractionRepo) CreateExtractionJobs(_ context.Context, _ domain.ExtractionScope, messages []domain.ExtractionSourceMessage, wait time.Duration) (int, error) {
	f.created = len(messages)
	f.createdWait = wait
	return len(messages), nil
}

func (f *fakeExtractionRepo) ClaimReadyExtractionBatch(context.Context, domain.ExtractionClaimOptions) (domain.ExtractionBatch, bool, error) {
	f.claimCalls++
	if len(f.batch.Jobs) == 0 {
		return domain.ExtractionBatch{}, false, nil
	}
	return f.batch, true, nil
}

func (f *fakeExtractionRepo) FinalizeExtractionBatch(_ context.Context, _ domain.ExtractionBatch, candidates []domain.Memory) (domain.FinalizeResult, error) {
	f.finalized = append(f.finalized, candidates)
	return domain.FinalizeResult{Created: len(candidates)}, nil
}

func (f *fakeExtractionRepo) FailExtractionBatch(_ context.Context, _ domain.ExtractionBatch, code string, _ []time.Duration, _ int) error {
	f.failed = code
	return nil
}

func (f *fakeExtractionRepo) CancelExtractionBatch(_ context.Context, _ domain.ExtractionBatch, code string) error {
	f.cancelled = code
	return nil
}

func (f *fakeExtractionRepo) RecoverStaleExtractionJobs(context.Context, int, []time.Duration) error {
	return nil
}

type fakeMemoryAuthorizer struct {
	auth scopecontracts.MemoryAuthorization
	err  error
}

func (f fakeMemoryAuthorizer) ResolveMemoryAuthorization(context.Context, int64, scopedomain.ScopeType, int64) (scopecontracts.MemoryAuthorization, error) {
	return f.auth, f.err
}

type fakeExtractor struct {
	candidates []domain.ExtractionCandidate
	err        error
	calls      int
}

func (f *fakeExtractor) Extract(context.Context, domain.ExtractionRequest) ([]domain.ExtractionCandidate, error) {
	f.calls++
	return f.candidates, f.err
}

func newTestExtractionWorker(t *testing.T, repo ExtractionRepository, extractor Extractor, authorizer scopecontracts.MemoryAuthorizer) *ExtractionWorker {
	t.Helper()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	worker, err := NewExtractionWorker(repo, extractor, authorizer, ExtractionWorkerOptions{
		Enabled: func() bool { return true }, Concurrency: 1, BatchMaxMessages: 10, BatchMaxRunes: 4000, MaxWait: 5 * time.Second, PollInterval: time.Second,
	}, logger)
	if err != nil {
		t.Fatalf("new worker: %v", err)
	}
	return worker
}

// TestExtractionWorkerDisabledDoesNotScanOrCallModel verifies the global
// switch gates scanning and model calls.
// TestExtractionWorkerDisabledDoesNotScanOrCallModel 验证全局开关关闭时不扫描、不调用模型。
func TestExtractionWorkerDisabledDoesNotScanOrCallModel(t *testing.T) {
	repo := &fakeExtractionRepo{scopes: []domain.ExtractionScope{{BotID: 1, ConversationID: "conv", ScopeType: "group", ScopeID: 7, AuthorizationEventID: 9}}, messages: []domain.ExtractionSourceMessage{{ID: 11}}}
	extractor := &fakeExtractor{}
	worker := newTestExtractionWorker(t, repo, extractor, fakeMemoryAuthorizer{})
	worker.opts.Enabled = func() bool { return false }
	if err := worker.Run(context.Background()); err != nil {
		t.Fatalf("run: %v", err)
	}
	if repo.scopesCalls != 0 || repo.claimCalls != 0 || extractor.calls != 0 || repo.created != 0 {
		t.Fatalf("disabled worker touched repo/model: scopes=%d claims=%d model=%d created=%d", repo.scopesCalls, repo.claimCalls, extractor.calls, repo.created)
	}
}

// TestExtractionWorkerSweepCreatesBoundedJobs verifies the scanner hands the
// configured wait and bounded messages to job creation.
// TestExtractionWorkerSweepCreatesBoundedJobs 验证扫描按配置等待与消息上限建任务。
func TestExtractionWorkerSweepCreatesBoundedJobs(t *testing.T) {
	repo := &fakeExtractionRepo{
		scopes:   []domain.ExtractionScope{{BotID: 1, ConversationID: "conv", ScopeType: "group", ScopeID: 7, AuthorizationEventID: 9}},
		messages: []domain.ExtractionSourceMessage{{ID: 11}, {ID: 12}},
	}
	worker := newTestExtractionWorker(t, repo, &fakeExtractor{}, fakeMemoryAuthorizer{})
	if err := worker.Sweep(context.Background()); err != nil {
		t.Fatalf("sweep: %v", err)
	}
	if repo.created != 2 || repo.createdWait != 5*time.Second {
		t.Fatalf("created=%d wait=%s", repo.created, repo.createdWait)
	}
}

// TestExtractionWorkerCancelsWhenAuthorizationChanged verifies a claimed batch
// is not sent to the model after authorization is revoked.
// TestExtractionWorkerCancelsWhenAuthorizationChanged 验证撤权后的已认领批次不会进入模型。
func TestExtractionWorkerCancelsWhenAuthorizationChanged(t *testing.T) {
	repo := &fakeExtractionRepo{}
	extractor := &fakeExtractor{candidates: []domain.ExtractionCandidate{{Content: "x", SourceMessageID: 1}}}
	authorizer := fakeMemoryAuthorizer{auth: scopecontracts.MemoryAuthorization{EventID: 2, Authorized: false}}
	worker := newTestExtractionWorker(t, repo, extractor, authorizer)
	worker.processBatch(context.Background(), domain.ExtractionBatch{
		Scope: domain.ExtractionScope{BotID: 1, ConversationID: "conv", ScopeType: "group", ScopeID: 7, AuthorizationEventID: 2},
		Jobs:  []domain.ExtractionJob{{ID: "job"}}, Messages: []domain.ExtractionSourceMessage{{ID: 1}},
	})
	if extractor.calls != 0 || repo.cancelled != "authorization_changed" {
		t.Fatalf("extractor calls=%d cancelled=%q", extractor.calls, repo.cancelled)
	}
}

// TestExtractionWorkerEmptyCandidatesSucceeds verifies an empty candidate list
// is a legal successful extraction result.
// TestExtractionWorkerEmptyCandidatesSucceeds 验证空候选列表是合法的成功提取结果。
func TestExtractionWorkerEmptyCandidatesSucceeds(t *testing.T) {
	repo := &fakeExtractionRepo{}
	worker := newTestExtractionWorker(t, repo, &fakeExtractor{}, fakeMemoryAuthorizer{auth: scopecontracts.MemoryAuthorization{EventID: 3, Authorized: true}})
	worker.processBatch(context.Background(), domain.ExtractionBatch{
		Scope:    domain.ExtractionScope{BotID: 1, ConversationID: "conv", ScopeType: "group", ScopeID: 7, AuthorizationEventID: 3},
		Jobs:     []domain.ExtractionJob{{ID: "job"}},
		Messages: []domain.ExtractionSourceMessage{{ID: 11, BotID: 1, ConversationID: "conv", ScopeType: "group", ScopeID: 7, SenderID: 42, AuthorizationEventID: 3}},
	})
	if len(repo.finalized) != 1 || len(repo.finalized[0]) != 0 || repo.failed != "" {
		t.Fatalf("finalized=%+v failed=%q, want empty successful finalize", repo.finalized, repo.failed)
	}
}

// TestExtractionWorkerFinalizesCandidates verifies the successful worker path
// materializes exactly one candidate for the source message and finalizes it.
// TestExtractionWorkerFinalizesCandidates 验证成功路径按来源消息生成一条候选并落库。
func TestExtractionWorkerFinalizesCandidates(t *testing.T) {
	repo := &fakeExtractionRepo{}
	extractor := &fakeExtractor{candidates: []domain.ExtractionCandidate{{
		Content: "用户A喜欢喝茶", Confidence: 0.8, SourceMessageID: 11, Subject: "sender",
	}}}
	worker := newTestExtractionWorker(t, repo, extractor, fakeMemoryAuthorizer{auth: scopecontracts.MemoryAuthorization{EventID: 3, Authorized: true}})
	worker.processBatch(context.Background(), domain.ExtractionBatch{
		Scope:    domain.ExtractionScope{BotID: 1, ConversationID: "conv", ScopeType: "group", ScopeID: 7, AuthorizationEventID: 3},
		Jobs:     []domain.ExtractionJob{{ID: "job"}},
		Messages: []domain.ExtractionSourceMessage{{ID: 11, BotID: 1, ConversationID: "conv", ScopeType: "group", ScopeID: 7, SenderID: 42, AuthorizationEventID: 3}},
	})
	if extractor.calls != 1 || len(repo.finalized) != 1 || len(repo.finalized[0]) != 1 {
		t.Fatalf("extractor calls=%d finalize=%+v", extractor.calls, repo.finalized)
	}
	got := repo.finalized[0][0]
	if got.SubjectType != domain.SubjectUser || got.SubjectID != "42" || got.SourceSenderID != 42 {
		t.Fatalf("candidate subject = %+v", got)
	}
}

// TestExtractionWorkerFailsExtractorError verifies extractor failures are
// recorded as retryable batch failures rather than swallowed.
// TestExtractionWorkerFailsExtractorError 验证抽取器错误会记录为可重试批次失败。
func TestExtractionWorkerFailsExtractorError(t *testing.T) {
	repo := &fakeExtractionRepo{}
	worker := newTestExtractionWorker(t, repo, &fakeExtractor{err: errors.New("timeout")}, fakeMemoryAuthorizer{auth: scopecontracts.MemoryAuthorization{EventID: 3, Authorized: true}})
	worker.processBatch(context.Background(), domain.ExtractionBatch{
		Scope: domain.ExtractionScope{BotID: 1, ConversationID: "conv", ScopeType: "group", ScopeID: 7, AuthorizationEventID: 3},
		Jobs:  []domain.ExtractionJob{{ID: "job"}}, Messages: []domain.ExtractionSourceMessage{{ID: 1}},
	})
	if repo.failed != "extractor_failed" {
		t.Fatalf("failed code = %q", repo.failed)
	}
}
