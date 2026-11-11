// Package application implements memory candidate, approval, search and management use cases.
// memory/application 实现记忆候选、审核、检索与管理用例。
package application

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/HappyLadySauceM/Nagi/internal/memory/contracts"
	"github.com/HappyLadySauceM/Nagi/internal/memory/domain"
)

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
	SoftDelete(ctx context.Context, memoryID string) error
	List(ctx context.Context, f domain.ListFilter) ([]domain.Memory, int64, error)
	FindSame(ctx context.Context, botID int64, visibility domain.Visibility, ownerKey, content string) (domain.Memory, bool, error)
	SearchVisible(ctx context.Context, in domain.SearchRequest) ([]domain.Memory, error)
}

// Service implements the candidate → dedupe → approve/merge → active workflow.
// Service 实现 候选 → 去重 → 审核/合并 → 激活 流程。
type Service struct {
	repo Repository
}

// NewService validates dependencies and builds the service.
// NewService 校验依赖并构建服务。
func NewService(repo Repository) (*Service, error) {
	if repo == nil {
		return nil, fmt.Errorf("memory: repository required")
	}
	return &Service{repo: repo}, nil
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
	Confidence      float64
}

// Propose dedupes and stores one memory candidate. Exact duplicates return the
// existing memory; conflicting content must go through explicit merge via the
// management API, never automatic overwrite.
// Propose 对记忆候选去重并存储。完全重复时返回既有记忆；内容冲突必须
// 经管理端显式合并，绝不自动覆盖。
func (s *Service) Propose(ctx context.Context, in CandidateInput) (string, bool, error) {
	m := domain.Memory{
		BotID:           in.BotID,
		Visibility:      in.Visibility,
		OwnerKey:        in.OwnerKey,
		Content:         strings.TrimSpace(in.Content),
		SourceMessageID: in.SourceMessageID,
		Source:          in.Source,
		Confidence:      in.Confidence,
		Status:          domain.StatusCandidate,
	}
	if err := m.Validate(); err != nil {
		return "", false, err
	}
	existing, found, err := s.repo.FindSame(ctx, in.BotID, in.Visibility, in.OwnerKey, m.Content)
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
	if err := m.Validate(); err != nil {
		return err
	}
	return s.repo.Update(ctx, m)
}

// Delete removes a memory from retrieval by marking it deleted.
// Delete 将记忆标记为删除，使其从检索结果中消失。
func (s *Service) Delete(ctx context.Context, memoryID string) error {
	return s.repo.SoftDelete(ctx, memoryID)
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
		UpdatedAt:       item.UpdatedAt,
	}, true, nil
}

// NewID generates a memory identifier.
// NewID 生成记忆标识。
func NewID() string { return uuid.NewString() }
