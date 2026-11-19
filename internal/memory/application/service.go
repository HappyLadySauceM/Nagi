// Package application implements memory candidate, approval, search and management use cases.
// memory/application 实现记忆候选、审核、检索与管理用例。
package application

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"

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

// Memory mutation errors are stable API-facing causes for handler mapping.
// 记忆变更错误是供处理器映射的稳定接口原因。
var (
	ErrMemoryInvalid        = errors.New("memory: invalid memory fields")
	ErrMemoryStatusConflict = errors.New("memory: memory status conflict")
	ErrSubjectMismatch      = errors.New("memory: subject mismatch")
	ErrSubjectAmbiguous     = errors.New("memory: subject ambiguous")
	ErrIdempotencyConflict  = errors.New("memory: idempotency conflict")
	ErrReplacementInvalid   = errors.New("memory: replacement invalid")
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

// AtomicMemoryRepository applies status/content changes under the shared
// memory:mutate lock and row lock. Production repositories implement it.
// AtomicMemoryRepository 在共享 memory:mutate 锁与行锁下应用状态/内容变更；
// 生产仓储实现该接口。
type AtomicMemoryRepository interface {
	ApproveMemory(ctx context.Context, memoryID, actor string) error
	ApproveMemoryWithEdit(ctx context.Context, memoryID, actor, content string, visibility domain.Visibility, ownerKey *string) error
	UpdateMemoryLocked(ctx context.Context, m domain.Memory) error
	DeleteMemory(ctx context.Context, memoryID string) (bool, error)
	InsertCandidateLocked(ctx context.Context, m domain.Memory) (string, bool, error)
}

// ReplacementRepository provides the explicit supersede transaction and
// management replacement view.
// ReplacementRepository 提供显式替代事务与管理端替代关系视图。
type ReplacementRepository interface {
	Supersede(ctx context.Context, in domain.SupersedeInput) (domain.SupersedeResult, error)
	Management(ctx context.Context, memoryID string, botID int64) (domain.Memory, []domain.ReplacementView, bool, error)
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

// SummaryRepository is the optional aggregate read port used by the knowledge
// summary endpoint. It returns counts only and never memory or message bodies.
// SummaryRepository 是知识摘要接口使用的可选聚合读取端口；只返回计数，
// 绝不返回记忆或消息正文。
type SummaryRepository interface {
	MemorySummary(ctx context.Context, botID int64) (domain.MemorySummary, error)
	ExtractionSummary(ctx context.Context, botID int64) (domain.ExtractionSummary, error)
}

type RecallRewriter interface {
	Rewrite(ctx context.Context, botID int64, kind string, targetID int64, query string) ([]string, error)
}

// Service implements the candidate → dedupe → approve/merge → active workflow.
// Service 实现 候选 → 去重 → 审核/合并 → 激活 流程。
type Service struct {
	rewriter   RecallRewriter
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

// SetRecallRewriter injects the optional model used after an empty literal recall.
// SetRecallRewriter 注入原文召回为空时使用的改写模型。
func (s *Service) SetRecallRewriter(rewriter RecallRewriter) {
	s.rewriter = rewriter
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
	if writer, ok := s.repo.(AtomicMemoryRepository); ok {
		return writer.InsertCandidateLocked(ctx, m)
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
	if writer, ok := s.repo.(AtomicMemoryRepository); ok {
		return writer.ApproveMemory(ctx, memoryID, approvedBy)
	}
	m, found, err := s.repo.Get(ctx, memoryID)
	if err != nil {
		return err
	}
	if !found {
		return fmt.Errorf("%w: %s", ErrMemoryNotFound, memoryID)
	}
	if m.Status != domain.StatusCandidate {
		return ErrMemoryStatusConflict
	}
	m.Status = domain.StatusActive
	m.ApprovedBy = approvedBy
	return s.repo.Update(ctx, m)
}

// ApproveWithEdit validates and applies an edit together with candidate ->
// active in one repository transaction, so a failed approval cannot leave the
// edit committed.
// ApproveWithEdit 在同一仓储事务中校验并应用编辑与 candidate -> active，避免审批
// 失败时编辑已落库。
func (s *Service) ApproveWithEdit(ctx context.Context, memoryID, actor, content string, visibility domain.Visibility, ownerKey *string) error {
	if strings.TrimSpace(actor) == "" {
		return fmt.Errorf("memory: approver is required")
	}
	if writer, ok := s.repo.(AtomicMemoryRepository); ok {
		return writer.ApproveMemoryWithEdit(ctx, memoryID, actor, content, visibility, ownerKey)
	}
	m, found, err := s.repo.Get(ctx, memoryID)
	if err != nil {
		return err
	}
	if !found {
		return fmt.Errorf("%w: %s", ErrMemoryNotFound, memoryID)
	}
	if m.Status != domain.StatusCandidate {
		return ErrMemoryStatusConflict
	}
	if err := s.Revise(ctx, memoryID, content, visibility, ownerKey); err != nil {
		return err
	}
	return s.Approve(ctx, memoryID, actor)
}

// Revise updates content, visibility or owner of one memory after validation.
// Revise 在校验后更新一条记忆的内容、可见性或归属。
func (s *Service) Revise(ctx context.Context, memoryID string, content string, visibility domain.Visibility, ownerKey *string) error {
	m, found, err := s.repo.Get(ctx, memoryID)
	if err != nil {
		return err
	}
	if !found {
		return fmt.Errorf("%w: %s", ErrMemoryNotFound, memoryID)
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
		return fmt.Errorf("%w: %v", ErrMemoryInvalid, err)
	}
	if m.Status == domain.StatusSuperseded || m.Status == domain.StatusDeleted {
		return ErrMemoryStatusConflict
	}
	if writer, ok := s.repo.(AtomicMemoryRepository); ok {
		return writer.UpdateMemoryLocked(ctx, m)
	}
	return s.repo.Update(ctx, m)
}

// Delete permanently removes one memory row.
// Delete 永久删除一条记忆行。
func (s *Service) Delete(ctx context.Context, memoryID string) error {
	if writer, ok := s.repo.(AtomicMemoryRepository); ok {
		deleted, err := writer.DeleteMemory(ctx, memoryID)
		if err != nil {
			return err
		}
		if !deleted {
			return ErrMemoryNotFound
		}
		return nil
	}
	deleted, err := s.repo.Delete(ctx, memoryID)
	if err != nil {
		return err
	}
	if !deleted {
		return ErrMemoryNotFound
	}
	return nil
}

// Supersede explicitly replaces one active memory with a candidate in one
// transaction. The repository owns the shared memory:mutate lock and row locks.
// Supersede 在一个事务中显式用候选替代一条 active 记忆。仓储负责共享
// memory:mutate 锁与行锁。
func (s *Service) Supersede(ctx context.Context, in domain.SupersedeInput) (domain.SupersedeResult, error) {
	in.NewMemoryID = strings.TrimSpace(in.NewMemoryID)
	in.OldMemoryID = strings.TrimSpace(in.OldMemoryID)
	in.Actor = strings.TrimSpace(in.Actor)
	in.Reason = strings.TrimSpace(in.Reason)
	in.IdempotencyKey = strings.TrimSpace(in.IdempotencyKey)
	if _, err := uuid.Parse(in.NewMemoryID); err != nil {
		return domain.SupersedeResult{}, fmt.Errorf("%w: invalid new memory id", ErrReplacementInvalid)
	}
	if _, err := uuid.Parse(in.OldMemoryID); err != nil {
		return domain.SupersedeResult{}, fmt.Errorf("%w: invalid old memory id", ErrReplacementInvalid)
	}
	if in.Actor == "" {
		return domain.SupersedeResult{}, fmt.Errorf("%w: actor is required", ErrReplacementInvalid)
	}
	if len([]rune(in.IdempotencyKey)) < 1 || len([]rune(in.IdempotencyKey)) > 128 {
		return domain.SupersedeResult{}, fmt.Errorf("%w: idempotency key must be 1-128 characters", ErrReplacementInvalid)
	}
	repo, ok := s.repo.(ReplacementRepository)
	if !ok {
		return domain.SupersedeResult{}, fmt.Errorf("memory: replacement repository unavailable")
	}
	return repo.Supersede(ctx, in)
}

// Management returns one memory in any lifecycle state plus its replacement
// relations for the administrator detail endpoint.
// Management 返回任意生命周期状态的一条记忆及其替代关系，供管理员详情接口使用。
func (s *Service) Management(ctx context.Context, memoryID string, botID int64) (domain.Memory, []domain.ReplacementView, bool, error) {
	if _, err := uuid.Parse(strings.TrimSpace(memoryID)); err != nil {
		return domain.Memory{}, nil, false, fmt.Errorf("%w: %q", ErrInvalidID, memoryID)
	}
	if botID <= 0 {
		return domain.Memory{}, nil, false, fmt.Errorf("memory: bot id must be positive")
	}
	repo, ok := s.repo.(ReplacementRepository)
	if !ok {
		return domain.Memory{}, nil, false, fmt.Errorf("memory: replacement repository unavailable")
	}
	return repo.Management(ctx, strings.TrimSpace(memoryID), botID)
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

// Summary returns aggregate memory and extraction counts for one bot.
// Summary 返回单个机器人的记忆与抽取聚合计数。
func (s *Service) Summary(ctx context.Context, botID int64) (domain.KnowledgeSummary, error) {
	if botID <= 0 {
		return domain.KnowledgeSummary{}, fmt.Errorf("memory: bot id must be positive")
	}
	repo, ok := s.repo.(SummaryRepository)
	if !ok {
		return domain.KnowledgeSummary{}, fmt.Errorf("memory: summary repository unavailable")
	}
	memories, err := repo.MemorySummary(ctx, botID)
	if err != nil {
		return domain.KnowledgeSummary{}, err
	}
	extraction, err := repo.ExtractionSummary(ctx, botID)
	if err != nil {
		return domain.KnowledgeSummary{}, err
	}
	return domain.KnowledgeSummary{Memories: memories, Extraction: extraction}, nil
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

// Recall returns a bounded, visibility-scoped ranked read model for reply
// generation. Unlike management listing, it is intentionally limited to the
// few entries a reply prompt can safely carry.
// Recall 返回供回复生成使用的有界、按可见性过滤的排序读模型。
// 与后台列表不同，它刻意只返回回复提示可以安全承载的少量条目。
func (s *Service) Recall(ctx context.Context, in contracts.RecallRequest) (contracts.RecallResult, error) {
	limit := in.Limit
	if limit <= 0 {
		limit = 3
	}
	if limit > 5 {
		limit = 5
	}
	result := contracts.RecallResult{Items: []contracts.RecallItem{}, Limit: limit}
	if in.BotID <= 0 {
		return result, fmt.Errorf("memory: bot id must be positive")
	}
	if in.Kind != "group" && in.Kind != "private" {
		return result, fmt.Errorf("memory: invalid conversation kind %q", in.Kind)
	}
	if in.TargetID <= 0 {
		return result, fmt.Errorf("memory: target id must be positive")
	}
	speakerType, err := recallSubjectType(in.SpeakerType, false)
	if err != nil {
		return result, fmt.Errorf("memory: invalid speaker type %q", in.SpeakerType)
	}
	subjectType, err := recallSubjectType(in.SubjectType, true)
	if err != nil {
		return result, fmt.Errorf("memory: invalid subject type %q", in.SubjectType)
	}
	subjectID := strings.TrimSpace(in.SubjectID)
	if subjectType != domain.SubjectUnknown && subjectID == "" {
		return result, fmt.Errorf("memory: subject id is required for subject type %s", subjectType)
	}
	if subjectType == domain.SubjectGroup && in.Kind != "group" {
		return result, fmt.Errorf("memory: group subject requires a group conversation")
	}
	if in.SubjectAmbiguous {
		subjectType = domain.SubjectUnknown
		subjectID = ""
	}
	if speakerType != domain.SubjectUser && speakerType != domain.SubjectUnknown {
		return result, fmt.Errorf("memory: invalid speaker type %q", in.SpeakerType)
	}
	source := strings.TrimSpace(in.SubjectSource)
	if source == "" {
		source = "unknown"
	}
	if !validRecallSubjectSource(source) {
		return result, fmt.Errorf("memory: invalid subject source %q", source)
	}
	search := domain.SearchRequest{
		BotID: in.BotID, Kind: in.Kind, TargetID: in.TargetID, ConversationID: in.ConversationID, Query: in.Query,
	}
	query := domain.RecallQuery{
		Kind: in.Kind, TargetID: in.TargetID, Query: in.Query, SubjectType: subjectType, SubjectID: subjectID, Limit: limit,
	}
	lookup := func(ctx context.Context) ([]domain.RecallHit, bool, string, error) {
		items, err := s.repo.SearchVisible(ctx, search)
		if err != nil {
			return nil, false, "", err
		}
		hits, empty, reason := domain.RankRecall(items, query)
		return hits, empty, reason, nil
	}
	var hits []domain.RecallHit
	noKeyword, reason := true, "no_keyword"
	if domain.HasRecallKeyword(in.Query) {
		var err error
		hits, noKeyword, reason, err = lookup(ctx)
		if err != nil {
			return result, err
		}
	}
	if len(hits) > 0 {
		result.Source = "literal"
	} else if in.AllowRewrite && s.rewriter != nil && ctx.Err() == nil && recallCanRewrite(in.Query) {
		rewriteCtx, span := otel.Tracer("nagi.memory").Start(ctx, "nagi.memory.rewrite")
		defer span.End()
		bounded, cancel := context.WithTimeout(rewriteCtx, 2*time.Second)
		rewritten, rewriteErr := s.rewriter.Rewrite(bounded, in.BotID, in.Kind, in.TargetID, in.Query)
		rewriteDone := bounded.Err() == nil
		cancel()
		switch {
		case rewriteErr != nil || !rewriteDone:
			span.SetAttributes(attribute.String("nagi.memory.rewrite_status", "unavailable"))
		case len(rewritten) == 0:
			span.SetAttributes(attribute.String("nagi.memory.rewrite_status", "no_terms"))
		default:
			keywords := make([]domain.QueryTerm, 0, len(rewritten))
			for _, term := range rewritten {
				keywords = append(keywords, domain.QueryTerm{Term: term, Kind: "rewrite"})
			}
			search.Keywords, query.Keywords = keywords, keywords
			matches, _, _, err := lookup(rewriteCtx)
			switch {
			case err != nil:
				span.SetAttributes(attribute.String("nagi.memory.rewrite_status", "lookup_failed"))
			case len(matches) == 0:
				span.SetAttributes(attribute.String("nagi.memory.rewrite_status", "no_match"))
			default:
				hits, noKeyword, reason = matches, false, ""
				result.Source = "rewrite"
				span.SetAttributes(attribute.String("nagi.memory.rewrite_status", "matched"))
			}
		}
	}

	if noKeyword {
		result.NoKeyword = true
		result.Reason = reason
		return result, nil
	}
	result.Items = make([]contracts.RecallItem, 0, len(hits))
	for _, hit := range hits {
		result.Items = append(result.Items, contracts.RecallItem{
			MemoryID:        hit.Memory.ID,
			Content:         hit.Memory.Content,
			Visibility:      string(hit.Memory.Visibility),
			SubjectType:     string(hit.Memory.SubjectType),
			SubjectID:       hit.Memory.SubjectID,
			Source:          hit.Memory.Source,
			SourceMessageID: hit.Memory.SourceMessageID,
			SourceSenderID:  hit.Memory.SourceSenderID,
			Score:           hit.Score,
			HitKeywords:     hit.HitKeywords,
			SubjectMatch:    hit.SubjectMatch,
			SelectionReason: hit.SelectionReason,
			UpdatedAt:       hit.Memory.UpdatedAt,
			Memory:          hit.Memory,
		})
	}
	return result, nil
}

func recallCanRewrite(text string) bool {
	text = strings.ToLower(strings.TrimFunc(text, func(r rune) bool {
		return unicode.IsSpace(r) || unicode.IsPunct(r)
	}))
	switch text {
	case "", "hi", "hello", "你好", "大家好", "你们好", "嗨", "谢谢", "在吗", "有人吗", "有人嘛":
		return false
	}
	return strings.ContainsFunc(text, unicode.IsLetter)
}

// recallSubjectType normalizes an API subject type. Speaker values cannot be
// group; query subjects may be user, group or unknown.
// recallSubjectType 规范化 API 主体类型；当前发言者不能是 group，查询主体
// 可以是 user、group 或 unknown。
func recallSubjectType(raw string, allowGroup bool) (domain.SubjectType, error) {
	switch strings.TrimSpace(raw) {
	case "", "unknown":
		return domain.SubjectUnknown, nil
	case "user":
		return domain.SubjectUser, nil
	case "group":
		if allowGroup {
			return domain.SubjectGroup, nil
		}
	}
	return "", fmt.Errorf("invalid subject type")
}

// validRecallSubjectSource checks the deterministic source vocabulary.
// validRecallSubjectSource 校验确定性来源词表。
func validRecallSubjectSource(source string) bool {
	switch source {
	case "self", "mention", "qq_id", "reply_target", "explicit_group", "admin_explicit", "unknown":
		return true
	}
	return false
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
