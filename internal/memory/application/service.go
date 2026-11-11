// Package application implements memory candidate, approval, search and management use cases.
// memory/application 实现记忆候选、审核、检索与管理用例。
package application

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/HappyLadySauceM/Nagi/internal/memory/contracts"
	"github.com/HappyLadySauceM/Nagi/internal/memory/domain"
	scopecontracts "github.com/HappyLadySauceM/Nagi/internal/scope/contracts"
)

// ErrInvalidID reports a malformed memory identifier before it reaches storage.
// ErrInvalidID 表示记忆 ID 格式非法，避免把解析错误当成内部错误返回。
var ErrInvalidID = errors.New("memory: invalid memory id")

// ErrMemoryNotFound reports that a memory row does not exist.
// ErrMemoryNotFound 表示记忆行不存在。
var ErrMemoryNotFound = errors.New("memory: memory not found")

// Extraction management errors are stable, safe messages for the HTTP layer.
// 提取管理错误是提供给 HTTP 层的稳定安全消息。
var (
	ErrExtractionJobNotFound          = errors.New("memory: extraction job not found")
	ErrExtractionJobState             = errors.New("memory: extraction job is not retryable")
	ErrExtractionSourceMissing        = errors.New("memory: extraction source is unavailable")
	ErrExtractionAuthorizationChanged = errors.New("memory: extraction authorization changed")
	ErrExtractionRetryLimit           = errors.New("memory: extraction retry limit reached")
)

// DefaultManualRetryLimit is the maximum number of manual retries per job.
// DefaultManualRetryLimit 是每个任务的最大手动重试次数。
const DefaultManualRetryLimit = 1

// Service implements the read-only recall port used by reply generation.
// Service 实现回复生成使用的只读召回端口。
var _ contracts.Reader = (*Service)(nil)
var _ contracts.DetailReader = (*Service)(nil)

// Repository persists memories; implemented by infrastructure.
// Repository 持久化记忆；由 infrastructure 实现。
type Repository interface {
	Insert(ctx context.Context, m domain.Memory) (string, error)
	Get(ctx context.Context, memoryID string) (domain.Memory, bool, error)
	Update(ctx context.Context, m domain.Memory) error
	Delete(ctx context.Context, memoryID string) (bool, error)
	List(ctx context.Context, f domain.ListFilter) ([]domain.Memory, int64, error)
	FindSame(ctx context.Context, botID int64, visibility domain.Visibility, ownerKey, content string) (domain.Memory, bool, error)
	SearchVisible(ctx context.Context, in domain.SearchRequest) ([]domain.Memory, error)
}

// ExtractionJobRepository is an optional management read/retry port.
// ExtractionJobRepository 是可选的管理读取/重试端口。
type ExtractionJobRepository interface {
	ListExtractionJobs(ctx context.Context, f domain.ExtractionJobFilter) ([]domain.ExtractionJob, int64, error)
	GetExtractionJob(ctx context.Context, id string) (domain.ExtractionJob, bool, error)
	RetryExtractionJob(ctx context.Context, id string, maxManualRetries int) (bool, error)
}

// DedupeRepository is the optional complete-key lookup used for subject-aware
// candidate deduplication.
// DedupeRepository 是用于带主体候选去重的可选完整键查询端口。
type DedupeRepository interface {
	FindByDedupeKey(ctx context.Context, key string) (domain.Memory, bool, error)
}

// Service implements the candidate → dedupe → approve/merge → active workflow.
// Service 实现 候选 → 去重 → 审核/合并 → 激活 流程。
type Service struct {
	repo       Repository
	authorizer scopecontracts.MemoryAuthorizer
}

// NewService validates dependencies and builds the service.
// NewService 校验依赖并构建服务。
func NewService(repo Repository) (*Service, error) {
	if repo == nil {
		return nil, fmt.Errorf("memory: repository required")
	}
	return &Service{repo: repo}, nil
}

// SetMemoryAuthorizer binds the current scope authorization resolver.
// SetMemoryAuthorizer 绑定当前作用域授权解析器。
func (s *Service) SetMemoryAuthorizer(authorizer scopecontracts.MemoryAuthorizer) {
	s.authorizer = authorizer
}

// CandidateInput is one proposed memory with its provenance.
// CandidateInput 是一条带来源的记忆提议。
type CandidateInput struct {
	BotID           int64
	Visibility      domain.Visibility
	OwnerKey        string
	Content         string
	SourceMessageID int64
	Source          string
	SourceSenderID  int64
	Origin          domain.Origin
	SubjectType     domain.SubjectType
	SubjectID       string
	DedupeKey       string
	ReviewReason    string
	Confidence      float64
}

// Propose dedupes and stores one memory candidate. Known-subject candidates use
// the complete structured key; unknown extraction subjects derive provenance
// from their source message and sender; legacy manual entries without a subject
// keep exact content dedupe. Conflicts require explicit management merge, never
// automatic overwrite.
// Propose 对记忆候选去重并存储。已知主体使用完整结构化键；未知提取主体从来源
// 消息与发送者生成出处标识；无主体的旧人工条目保留精确内容去重。冲突必须经
// 管理端显式合并，绝不自动覆盖。
func (s *Service) Propose(ctx context.Context, in CandidateInput) (string, bool, error) {
	origin := in.Origin
	if origin == "" {
		origin = domain.OriginManual
	}
	content := strings.TrimSpace(in.Content)
	subjectType := in.SubjectType
	subjectID := strings.TrimSpace(in.SubjectID)
	dedupeKey := in.DedupeKey
	if origin == domain.OriginExtraction && (subjectType == "" || subjectType == domain.SubjectUnknown) {
		subjectType = domain.SubjectUnknown
		if in.SourceMessageID <= 0 || in.SourceSenderID <= 0 {
			return "", false, fmt.Errorf("memory: extraction unknown subject requires source message and sender")
		}
		subjectID = domain.ExtractionUnknownSubjectID(in.SourceMessageID, in.SourceSenderID)
		dedupeKey = domain.ComputeDedupeKey(in.BotID, in.Visibility, in.OwnerKey, subjectType, subjectID, content)
	} else if dedupeKey == "" && subjectType != "" && subjectID != "" {
		dedupeKey = domain.ComputeDedupeKey(in.BotID, in.Visibility, in.OwnerKey, subjectType, subjectID, content)
	}
	m := domain.Memory{
		BotID:           in.BotID,
		Visibility:      in.Visibility,
		OwnerKey:        in.OwnerKey,
		Content:         content,
		SourceMessageID: in.SourceMessageID,
		Source:          in.Source,
		SourceSenderID:  in.SourceSenderID,
		Origin:          origin,
		SubjectType:     subjectType,
		SubjectID:       subjectID,
		DedupeKey:       dedupeKey,
		ReviewReason:    in.ReviewReason,
		Confidence:      in.Confidence,
		Status:          domain.StatusCandidate,
	}
	if err := m.Validate(); err != nil {
		return "", false, err
	}
	var existing domain.Memory
	var found bool
	var err error
	if dedupeKey != "" {
		if dedupeRepo, ok := s.repo.(DedupeRepository); ok {
			existing, found, err = dedupeRepo.FindByDedupeKey(ctx, dedupeKey)
		} else {
			existing, found, err = s.repo.FindSame(ctx, in.BotID, in.Visibility, in.OwnerKey, m.Content)
		}
	} else {
		existing, found, err = s.repo.FindSame(ctx, in.BotID, in.Visibility, in.OwnerKey, m.Content)
	}
	if err != nil {
		return "", false, err
	}
	if found {
		return existing.ID, true, nil
	}
	id, err := s.repo.Insert(ctx, m)
	if err != nil {
		return "", false, err
	}
	return id, false, nil
}

// Approve activates a candidate; the approver identity is recorded for audit.
// Approve 激活候选记忆；审批人身份会记录用于审计。
func (s *Service) Approve(ctx context.Context, memoryID, approvedBy string) error {
	if strings.TrimSpace(approvedBy) == "" {
		return fmt.Errorf("memory: approver is required")
	}
	m, found, err := s.repo.Get(ctx, memoryID)
	if err != nil {
		return err
	}
	if !found {
		return fmt.Errorf("memory: memory %s not found", memoryID)
	}
	m.Status = domain.StatusActive
	m.ApprovedBy = approvedBy
	return s.repo.Update(ctx, m)
}

// Revise updates content, visibility or owner of one memory after validation.
// Revise 在校验后更新一条记忆的内容、可见性或归属。
func (s *Service) Revise(ctx context.Context, memoryID string, content string, visibility domain.Visibility, ownerKey *string) error {
	m, found, err := s.repo.Get(ctx, memoryID)
	if err != nil {
		return err
	}
	if !found {
		return fmt.Errorf("memory: memory %s not found", memoryID)
	}
	if strings.TrimSpace(content) != "" {
		m.Content = strings.TrimSpace(content)
	}
	if visibility != "" {
		m.Visibility = visibility
	}
	if ownerKey != nil {
		m.OwnerKey = *ownerKey
	}
	if m.SubjectType != "" && m.SubjectID != "" {
		m.DedupeKey = domain.ComputeDedupeKey(m.BotID, m.Visibility, m.OwnerKey, m.SubjectType, m.SubjectID, m.Content)
	}
	if err := m.Validate(); err != nil {
		return err
	}
	return s.repo.Update(ctx, m)
}

// Delete permanently removes one memory row.
// Delete 永久删除一条记忆行。
func (s *Service) Delete(ctx context.Context, memoryID string) error {
	deleted, err := s.repo.Delete(ctx, memoryID)
	if err != nil {
		return err
	}
	if !deleted {
		return ErrMemoryNotFound
	}
	return nil
}

// Get returns one memory when it exists.
// Get 返回已存在的记忆。
func (s *Service) Get(ctx context.Context, memoryID string) (domain.Memory, bool, error) {
	return s.repo.Get(ctx, memoryID)
}

// List returns one page of memories for management.
// List 返回管理用的一页记忆。
func (s *Service) List(ctx context.Context, f domain.ListFilter) ([]domain.Memory, int64, error) {
	return s.repo.List(ctx, f.Normalized())
}

// SearchVisible returns active, unexpired memories the conversation may read:
// bot-shared knowledge plus the conversation's own group/private/conversation scope.
// SearchVisible 返回会话可读取的激活且未过期的记忆：机器人共享知识，
// 加上该会话自身的群/私聊/会话范围。
func (s *Service) SearchVisible(ctx context.Context, in domain.SearchRequest) ([]domain.Memory, error) {
	if in.Limit <= 0 {
		in.Limit = 5
	}
	return s.repo.SearchVisible(ctx, in)
}

// Recall returns a bounded, visibility-scoped read model for reply generation.
// Unlike management listing, it is intentionally limited to the few entries a
// reply prompt can safely carry.
// Recall 返回供回复生成使用的有界、按可见性过滤的读模型。
// 与后台列表不同，它刻意只返回回复提示可以安全承载的少量条目。
func (s *Service) Recall(ctx context.Context, in contracts.RecallRequest) ([]contracts.Memory, error) {
	if in.BotID <= 0 {
		return nil, fmt.Errorf("memory: bot id must be positive")
	}
	if in.Kind != "group" && in.Kind != "private" {
		return nil, fmt.Errorf("memory: invalid conversation kind %q", in.Kind)
	}
	if in.TargetID <= 0 {
		return nil, fmt.Errorf("memory: target id must be positive")
	}
	limit := in.Limit
	if limit <= 0 || limit > 5 {
		limit = 5
	}
	items, err := s.repo.SearchVisible(ctx, domain.SearchRequest{
		BotID:          in.BotID,
		Kind:           in.Kind,
		TargetID:       in.TargetID,
		ConversationID: in.ConversationID,
		Query:          in.Query,
		Limit:          limit,
	})
	if err != nil {
		return nil, err
	}
	out := make([]contracts.Memory, 0, len(items))
	for _, item := range items {
		out = append(out, contracts.Memory{
			ID:              item.ID,
			Content:         item.Content,
			Visibility:      string(item.Visibility),
			Source:          item.Source,
			SourceMessageID: item.SourceMessageID,
			SourceSenderID:  item.SourceSenderID,
			Origin:          string(item.Origin),
			SubjectType:     string(item.SubjectType),
			SubjectID:       item.SubjectID,
			ReviewReason:    item.ReviewReason,
			UpdatedAt:       item.UpdatedAt,
		})
	}
	return out, nil
}

// RecallDetail returns one active, unexpired memory only when it is visible to
// the requested conversation. It never returns a candidate, deleted, expired or
// cross-scope entry.
// RecallDetail 仅在记忆对指定会话可见时返回该激活且未过期的条目；
// 候选、已删除、已过期及跨作用域条目一律不返回。
func (s *Service) RecallDetail(ctx context.Context, in contracts.DetailRequest) (contracts.Memory, bool, error) {
	if in.BotID <= 0 {
		return contracts.Memory{}, false, fmt.Errorf("memory: bot id must be positive")
	}
	if in.Kind != "group" && in.Kind != "private" {
		return contracts.Memory{}, false, fmt.Errorf("memory: invalid conversation kind %q", in.Kind)
	}
	if in.TargetID <= 0 || strings.TrimSpace(in.MemoryID) == "" {
		return contracts.Memory{}, false, fmt.Errorf("memory: target id and memory id are required")
	}
	if _, err := uuid.Parse(in.MemoryID); err != nil {
		return contracts.Memory{}, false, fmt.Errorf("%w: %q", ErrInvalidID, in.MemoryID)
	}
	item, found, err := s.repo.Get(ctx, in.MemoryID)
	if err != nil || !found {
		return contracts.Memory{}, false, err
	}
	if item.BotID != in.BotID || item.Status != domain.StatusActive || !item.ExpiresAt.IsZero() && !item.ExpiresAt.After(time.Now()) {
		return contracts.Memory{}, false, nil
	}
	if !item.VisibleTo(in.Kind, in.TargetID, in.ConversationID) {
		return contracts.Memory{}, false, nil
	}
	return contracts.Memory{
		ID:              item.ID,
		Content:         item.Content,
		Visibility:      string(item.Visibility),
		Source:          item.Source,
		SourceMessageID: item.SourceMessageID,
		SourceSenderID:  item.SourceSenderID,
		Origin:          string(item.Origin),
		SubjectType:     string(item.SubjectType),
		SubjectID:       item.SubjectID,
		ReviewReason:    item.ReviewReason,
		UpdatedAt:       item.UpdatedAt,
	}, true, nil
}

// ListExtractionJobs returns a management page of extraction jobs.
// ListExtractionJobs 返回一页管理用提取任务。
func (s *Service) ListExtractionJobs(ctx context.Context, f domain.ExtractionJobFilter) ([]domain.ExtractionJob, int64, error) {
	repo, ok := s.repo.(ExtractionJobRepository)
	if !ok {
		return nil, 0, fmt.Errorf("memory: extraction job repository unavailable")
	}
	return repo.ListExtractionJobs(ctx, f.Normalized())
}

// RetryExtractionJob restarts one failed job only while its source and
// authorization generation are still valid.
// RetryExtractionJob 仅在来源与授权代际仍然有效时重启一个失败任务。
func (s *Service) RetryExtractionJob(ctx context.Context, jobID string) error {
	if _, err := uuid.Parse(jobID); err != nil {
		return fmt.Errorf("%w: %q", ErrInvalidID, jobID)
	}
	repo, ok := s.repo.(ExtractionJobRepository)
	if !ok {
		return fmt.Errorf("memory: extraction job repository unavailable")
	}
	job, found, err := repo.GetExtractionJob(ctx, jobID)
	if err != nil {
		return err
	}
	if !found {
		return ErrExtractionJobNotFound
	}
	if job.Status != "failed" || job.ManualRetries >= DefaultManualRetryLimit {
		return ErrExtractionJobState
	}
	if job.MessageID == 0 || !job.SourceAvailable {
		return ErrExtractionSourceMissing
	}
	if job.MessageAuthorizationEventID == 0 || job.MessageAuthorizationEventID != job.AuthorizationEventID {
		return ErrExtractionAuthorizationChanged
	}
	// The repository performs the authoritative source/state/authorization
	// recheck under the shared memory-authorization lock in the same transaction
	// as the update. Do not resolve authorization outside that transaction here.
	// 仓储层在同一事务内、持有同一记忆授权锁时复核来源、状态与授权；服务层不再
	// 在事务外解析授权，避免检查与更新之间出现撤权窗口。
	retried, err := repo.RetryExtractionJob(ctx, jobID, DefaultManualRetryLimit)
	if err != nil {
		return err
	}
	if !retried {
		return ErrExtractionJobState
	}
	return nil
}

// NewID generates a memory identifier.
// NewID 生成记忆标识。
func NewID() string { return uuid.NewString() }
