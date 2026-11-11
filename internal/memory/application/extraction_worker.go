package application

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/HappyLadySauceM/Nagi/internal/memory/domain"
	scopecontracts "github.com/HappyLadySauceM/Nagi/internal/scope/contracts"
	scopedomain "github.com/HappyLadySauceM/Nagi/internal/scope/domain"
)

// ErrExtractionLeaseLost reports that another worker owns or recovered the
// claimed batch, so the late worker must not write candidates.
// ErrExtractionLeaseLost 表示批次已被其他工作器持有或恢复，晚到的工作器不得写入候选。
var ErrExtractionLeaseLost = errors.New("memory: extraction lease lost")

// ExtractionRepository persists extraction jobs and atomically finalizes
// candidates. Implementations must enforce the authorization lock order.
// ExtractionRepository 持久化提取任务并原子落定候选；实现必须遵守授权锁顺序。
type ExtractionRepository interface {
	ListAuthorizedScopes(ctx context.Context, limit int) ([]domain.ExtractionScope, error)
	ListUnregisteredMessages(ctx context.Context, botID int64, conversationID string, authorizationEventID int64, limit int) ([]domain.ExtractionSourceMessage, error)
	CreateExtractionJobs(ctx context.Context, scope domain.ExtractionScope, messages []domain.ExtractionSourceMessage, maxWait time.Duration) (int, error)
	ClaimReadyExtractionBatch(ctx context.Context, options domain.ExtractionClaimOptions) (domain.ExtractionBatch, bool, error)
	FinalizeExtractionBatch(ctx context.Context, batch domain.ExtractionBatch, candidates []domain.Memory) (domain.FinalizeResult, error)
	FailExtractionBatch(ctx context.Context, batch domain.ExtractionBatch, code string, retryDelays []time.Duration, maxAttempts int) error
	CancelExtractionBatch(ctx context.Context, batch domain.ExtractionBatch, code string) error
	RecoverStaleExtractionJobs(ctx context.Context, maxAttempts int, retryDelays []time.Duration) error
}

// Extractor turns one bounded batch into candidate proposals.
// Extractor 把一个有界批次转换为候选提议。
type Extractor interface {
	Extract(ctx context.Context, request domain.ExtractionRequest) ([]domain.ExtractionCandidate, error)
}

// ExtractionWorkerOptions bounds scanning, coalescing and retry behavior.
// ExtractionWorkerOptions 限定扫描、合批与重试行为。
type ExtractionWorkerOptions struct {
	// Enabled is checked before scanning, before the model call and before
	// finalization; nil means the worker's construction-time gate is enough.
	// Enabled 在扫描前、模型调用前和终态提交前复核；nil 表示仅依赖构造时的门控。
	Enabled          func() bool
	Concurrency      int
	BatchMaxMessages int
	BatchMaxRunes    int
	MaxWait          time.Duration
	PollInterval     time.Duration
	MaxAttempts      int
	RetryBackoff     time.Duration
	RetryDelays      []time.Duration
	Lease            time.Duration
}

// ExtractionWorker incrementally scans authorized sessions and processes
// bounded per-conversation extraction batches.
// ExtractionWorker 增量扫描授权会话，并按会话处理有界提取批次。
type ExtractionWorker struct {
	repo       ExtractionRepository
	extractor  Extractor
	authorizer scopecontracts.MemoryAuthorizer
	opts       ExtractionWorkerOptions
	logger     *slog.Logger
}

// NewExtractionWorker validates dependencies and builds the worker.
// NewExtractionWorker 校验依赖并构建工作器。
func NewExtractionWorker(repo ExtractionRepository, extractor Extractor, authorizer scopecontracts.MemoryAuthorizer, opts ExtractionWorkerOptions, logger *slog.Logger) (*ExtractionWorker, error) {
	if repo == nil || extractor == nil || authorizer == nil || logger == nil {
		return nil, fmt.Errorf("memory: extraction worker dependencies required")
	}
	if opts.Enabled == nil {
		opts.Enabled = func() bool { return false }
	}
	if opts.Concurrency <= 0 {
		opts.Concurrency = 1
	}
	if opts.BatchMaxMessages <= 0 {
		opts.BatchMaxMessages = 10
	}
	if opts.BatchMaxRunes <= 0 {
		opts.BatchMaxRunes = 4000
	}
	if opts.MaxWait < 0 {
		opts.MaxWait = 0
	}
	if opts.PollInterval <= 0 {
		opts.PollInterval = time.Second
	}
	if opts.MaxAttempts <= 0 {
		opts.MaxAttempts = 3
	}
	if len(opts.RetryDelays) == 0 {
		if opts.RetryBackoff > 0 {
			opts.RetryDelays = []time.Duration{opts.RetryBackoff}
		} else {
			opts.RetryDelays = []time.Duration{30 * time.Second, 2 * time.Minute}
		}
	}
	if opts.Lease <= 0 {
		opts.Lease = 2 * time.Minute
	}
	return &ExtractionWorker{repo: repo, extractor: extractor, authorizer: authorizer, opts: opts, logger: logger}, nil
}

// Run scans and processes until the context is cancelled, then waits for
// in-flight batches to settle their terminal state.
// Run 持续扫描与处理直到上下文取消，然后等待在途批次完成终态写入。
func (w *ExtractionWorker) Run(ctx context.Context) error {
	if !w.enabled() {
		return nil
	}
	if err := w.repo.RecoverStaleExtractionJobs(ctx, w.opts.MaxAttempts, w.opts.RetryDelays); err != nil {
		w.logger.Warn("recover stale memory extraction jobs failed", "error", err)
	}
	ticker := time.NewTicker(w.opts.PollInterval)
	defer ticker.Stop()
	sem := make(chan struct{}, w.opts.Concurrency)
	var wg sync.WaitGroup
	defer wg.Wait()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			if err := w.repo.RecoverStaleExtractionJobs(ctx, w.opts.MaxAttempts, w.opts.RetryDelays); err != nil {
				w.logger.Warn("recover stale memory extraction jobs failed", "error", err)
			}
			if err := w.Sweep(ctx); err != nil {
				w.logger.Warn("memory extraction sweep failed", "error", err)
			}
		}
	claimLoop:
		for {
			select {
			case sem <- struct{}{}:
			default:
				break claimLoop
			}
			batch, found, err := w.repo.ClaimReadyExtractionBatch(ctx, domain.ExtractionClaimOptions{
				MaxMessages: w.opts.BatchMaxMessages,
				MaxRunes:    w.opts.BatchMaxRunes,
				Lease:       w.opts.Lease,
			})
			if err != nil {
				<-sem
				w.logger.Warn("claim memory extraction batch failed", "error", err)
				break claimLoop
			}
			if !found {
				<-sem
				break claimLoop
			}
			wg.Add(1)
			go func(batch domain.ExtractionBatch) {
				defer wg.Done()
				defer func() { <-sem }()
				defer func() {
					if recovered := recover(); recovered != nil {
						w.logger.Error("memory extraction worker panic", "panic", recovered)
					}
				}()
				w.processBatch(ctx, batch)
			}(batch)
		}
	}
}

// Sweep creates jobs for currently authorized, not-yet-registered messages.
// Sweep 为当前已授权且尚未登记任务的入站消息创建提取任务。
func (w *ExtractionWorker) Sweep(ctx context.Context) error {
	if !w.enabled() {
		return nil
	}
	scopes, err := w.repo.ListAuthorizedScopes(ctx, 100)
	if err != nil {
		return err
	}
	for _, scope := range scopes {
		messages, err := w.repo.ListUnregisteredMessages(ctx, scope.BotID, scope.ConversationID, scope.AuthorizationEventID, w.opts.BatchMaxMessages)
		if err != nil {
			return err
		}
		if len(messages) == 0 {
			continue
		}
		created, err := w.repo.CreateExtractionJobs(ctx, scope, messages, w.opts.MaxWait)
		if err != nil {
			return err
		}
		if created > 0 {
			w.logger.Info("memory extraction jobs created", "bot_id", scope.BotID, "conversation_id", scope.ConversationID, "count", created)
		}
	}
	return nil
}

func (w *ExtractionWorker) processBatch(ctx context.Context, batch domain.ExtractionBatch) {
	if !w.enabled() {
		_ = w.repo.CancelExtractionBatch(ctx, batch, "disabled")
		return
	}
	if len(batch.Jobs) == 0 || len(batch.Messages) == 0 {
		_ = w.repo.CancelExtractionBatch(ctx, batch, "empty_batch")
		return
	}
	current, err := w.authorizer.ResolveMemoryAuthorization(ctx, batch.Scope.BotID, scopedomain.ScopeType(batch.Scope.ScopeType), batch.Scope.ScopeID)
	if err != nil {
		w.failBatch(ctx, batch, "authorization_lookup_failed", err)
		return
	}
	if !current.Authorized || current.EventID != batch.Scope.AuthorizationEventID {
		_ = w.repo.CancelExtractionBatch(ctx, batch, "authorization_changed")
		return
	}
	if !w.enabled() {
		_ = w.repo.CancelExtractionBatch(ctx, batch, "disabled")
		return
	}
	candidates, err := w.extractor.Extract(ctx, domain.ExtractionRequest{Scope: batch.Scope, Messages: batch.Messages, MaxRunes: w.opts.BatchMaxRunes})
	if err != nil {
		w.failBatch(ctx, batch, "extractor_failed", err)
		return
	}
	if !w.enabled() {
		_ = w.repo.CancelExtractionBatch(ctx, batch, "disabled")
		return
	}
	memories, err := w.materializeCandidates(batch, candidates)
	if err != nil {
		w.failBatch(ctx, batch, "invalid_candidate", err)
		return
	}
	result, err := w.repo.FinalizeExtractionBatch(ctx, batch, memories)
	if err != nil {
		switch {
		case errors.Is(err, ErrExtractionAuthorizationChanged):
			_ = w.repo.CancelExtractionBatch(ctx, batch, "authorization_changed")
		case errors.Is(err, ErrExtractionSourceMissing):
			_ = w.repo.CancelExtractionBatch(ctx, batch, "source_missing")
		case errors.Is(err, ErrExtractionLeaseLost):
			w.logger.Warn("memory extraction finalize lost lease", "conversation_id", batch.Scope.ConversationID)
		default:
			w.failBatch(ctx, batch, "finalize_failed", err)
		}
		return
	}
	w.logger.Info("memory extraction batch completed",
		"bot_id", batch.Scope.BotID,
		"conversation_id", batch.Scope.ConversationID,
		"jobs", len(batch.Jobs),
		"created", result.Created,
		"duplicates", result.Duplicates,
		"suppressed", result.Suppressed)
}

func (w *ExtractionWorker) failBatch(ctx context.Context, batch domain.ExtractionBatch, code string, cause error) {
	w.logger.Warn("memory extraction batch failed", "conversation_id", batch.Scope.ConversationID, "code", code, "error", cause)
	if err := w.repo.FailExtractionBatch(ctx, batch, code, w.opts.RetryDelays, w.opts.MaxAttempts); err != nil {
		w.logger.Warn("record memory extraction failure failed", "conversation_id", batch.Scope.ConversationID, "error", err)
	}
}

// materializeCandidates validates the model-supplied subject and source before
// building candidate memories. Any candidate that cannot be bound to one exact
// batch source rejects the whole batch instead of being silently dropped.
// materializeCandidates 在校验模型给出的主体与来源后才构造候选记忆；任一候选
// 无法绑定到本批确切来源时整批拒绝，不会静默丢弃。
func (w *ExtractionWorker) materializeCandidates(batch domain.ExtractionBatch, candidates []domain.ExtractionCandidate) ([]domain.Memory, error) {
	sources := make(map[int64]domain.ExtractionSourceMessage, len(batch.Messages))
	for _, message := range batch.Messages {
		sources[message.ID] = message
	}
	visibility := domain.VisibilityGroup
	if batch.Scope.ScopeType == "private" {
		visibility = domain.VisibilityPrivateUser
	}
	ownerKey := strconv.FormatInt(batch.Scope.ScopeID, 10)
	out := make([]domain.Memory, 0, len(candidates))
	for index, candidate := range candidates {
		source, ok := sources[candidate.SourceMessageID]
		if !ok {
			return nil, fmt.Errorf("candidate %d source_message_id %d is outside the batch", index, candidate.SourceMessageID)
		}
		if source.BotID != batch.Scope.BotID || source.ConversationID != batch.Scope.ConversationID || source.AuthorizationEventID != batch.Scope.AuthorizationEventID {
			return nil, fmt.Errorf("candidate %d source provenance does not match the batch", index)
		}
		content := strings.TrimSpace(candidate.Content)
		if content == "" {
			return nil, fmt.Errorf("candidate %d content is empty", index)
		}
		if candidate.Confidence < 0 || candidate.Confidence > 1 {
			return nil, fmt.Errorf("candidate %d confidence is outside [0,1]", index)
		}
		subjectType := domain.SubjectUnknown
		subjectID := ""
		reviewReason := normalizeReviewReason(candidate.ReviewReason)
		switch strings.ToLower(strings.TrimSpace(candidate.Subject)) {
		case "sender", "user":
			subjectType = domain.SubjectUser
			subjectID = strconv.FormatInt(source.SenderID, 10)
		case "group":
			if batch.Scope.ScopeType != "group" {
				return nil, fmt.Errorf("candidate %d group subject is invalid in a private scope", index)
			}
			subjectType = domain.SubjectGroup
			subjectID = strconv.FormatInt(batch.Scope.ScopeID, 10)
		case "unknown", "":
			subjectType = domain.SubjectUnknown
			subjectID = domain.ExtractionUnknownSubjectID(source.ID, source.SenderID)
			if reviewReason == "" {
				reviewReason = "subject_unknown"
			}
		default:
			return nil, fmt.Errorf("candidate %d has an invalid subject", index)
		}
		if subjectType == domain.SubjectUnknown && reviewReason == "" {
			reviewReason = "subject_unknown"
		}
		out = append(out, domain.Memory{
			BotID:           batch.Scope.BotID,
			Visibility:      visibility,
			OwnerKey:        ownerKey,
			Content:         content,
			SourceMessageID: source.ID,
			SourceSenderID:  source.SenderID,
			Source:          fmt.Sprintf("message:%d", source.ID),
			Origin:          domain.OriginExtraction,
			SubjectType:     subjectType,
			SubjectID:       subjectID,
			DedupeKey:       domain.ComputeDedupeKey(batch.Scope.BotID, visibility, ownerKey, subjectType, subjectID, content),
			ReviewReason:    reviewReason,
			Confidence:      candidate.Confidence,
			Status:          domain.StatusCandidate,
		})
	}
	return out, nil
}

func (w *ExtractionWorker) enabled() bool {
	return w.opts.Enabled == nil || w.opts.Enabled()
}

func normalizeReviewReason(value string) string {
	switch strings.TrimSpace(value) {
	case "subject_unknown", "subject_unresolved", "evidence_insufficient", "low_confidence":
		return strings.TrimSpace(value)
	default:
		return ""
	}
}
