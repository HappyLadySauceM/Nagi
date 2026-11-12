// Prompt application service: local published versions first, built-in fallback
// always available. Langfuse is never consulted on the decision path.
// Prompt 应用服务：优先本地已发布版本，始终保留内置兜底。
// 判定路径绝不等待 Langfuse。
package application

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"

	"github.com/HappyLadySauceM/Nagi/internal/intent/domain"
)

// PromptRepository is the persistence port for locally published prompts.
// PromptRepository 是本地已发布 Prompt 的持久化端口。
type PromptRepository interface {
	Active(ctx context.Context, promptID, provider string) (domain.PromptRecord, bool, error)
	List(ctx context.Context) ([]domain.PromptRecord, error)
	Versions(ctx context.Context, promptID string) ([]domain.PromptRecord, error)
	Get(ctx context.Context, id int64) (domain.PromptRecord, error)
	CreateDraft(ctx context.Context, promptID, name, provider string, content domain.Prompt, note, source, sourceRef string) (domain.PromptRecord, error)
	UpdateDraft(ctx context.Context, id int64, note string, content domain.Prompt) (domain.PromptRecord, error)
	Publish(ctx context.Context, id int64) error
	Rollback(ctx context.Context, promptID string, version int) error
}

// PromptService resolves runtime prompts and manages local versions.
// PromptService 解析运行时 Prompt 并管理本地版本。
type PromptService struct {
	repo   PromptRepository
	logger *slog.Logger
}

func NewPromptService(repo PromptRepository, logger *slog.Logger) *PromptService {
	if logger == nil {
		logger = slog.Default()
	}
	return &PromptService{repo: repo, logger: logger}
}

// Resolve returns a runtime prompt. Any local failure falls back to the
// built-in verified prompt and is recorded in the returned snapshot; it never
// blocks an intent decision.
// Resolve 返回运行时 Prompt。任何本地失败都会回退到内置已验证 Prompt，
// 并记录在返回的快照中；绝不阻塞意图判定。
func (s *PromptService) Resolve(ctx context.Context, provider string) domain.Prompt {
	builtin := domain.BuiltinPrompt(provider)
	if s == nil || s.repo == nil {
		builtin.FallbackReason = "prompt_store_unavailable"
		return builtin
	}
	rec, ok, err := s.repo.Active(ctx, domain.BuiltinPromptID, provider)
	if err != nil {
		s.logger.Warn("load published prompt failed; using builtin prompt", "error", err)
		builtin.FallbackReason = "prompt_store_error"
		return builtin
	}
	if !ok {
		builtin.FallbackReason = "no_local_published_prompt"
		return builtin
	}
	parsed, err := domain.ParsePromptContent(provider, rec.Content)
	if err != nil {
		s.logger.Warn("invalid published prompt; using builtin prompt", "error", err, "prompt_id", rec.PromptID, "version", rec.Version)
		builtin.FallbackReason = "invalid_local_prompt"
		return builtin
	}
	parsed.ID = rec.PromptID
	parsed.Name = rec.Name
	parsed.Version = rec.Version
	parsed.Provider = provider
	parsed.Source = rec.Source
	parsed.SourceRef = rec.SourceRef
	parsed.Hash = rec.Hash
	return parsed
}

func (s *PromptService) List(ctx context.Context) ([]domain.PromptRecord, error) {
	if s == nil || s.repo == nil {
		return nil, fmt.Errorf("intent: prompt store unavailable")
	}
	return s.repo.List(ctx)
}

func (s *PromptService) Versions(ctx context.Context, promptID string) ([]domain.PromptRecord, error) {
	if s == nil || s.repo == nil {
		return nil, fmt.Errorf("intent: prompt store unavailable")
	}
	return s.repo.Versions(ctx, promptID)
}

func (s *PromptService) Get(ctx context.Context, id int64) (domain.PromptRecord, error) {
	if s == nil || s.repo == nil {
		return domain.PromptRecord{}, fmt.Errorf("intent: prompt store unavailable")
	}
	return s.repo.Get(ctx, id)
}

// CreateDraft validates provider-specific content before storage.
// CreateDraft 在存储前校验提供方专属内容。
func (s *PromptService) CreateDraft(ctx context.Context, promptID, name, provider string, content domain.Prompt, note, source, sourceRef string) (domain.PromptRecord, error) {
	if err := validatePromptForProvider(provider, content); err != nil {
		return domain.PromptRecord{}, err
	}
	if source == "" {
		source = domain.PromptSourceLocal
	}
	return s.repo.CreateDraft(ctx, promptID, name, provider, content, note, source, sourceRef)
}

func (s *PromptService) UpdateDraft(ctx context.Context, id int64, note string, content domain.Prompt) (domain.PromptRecord, error) {
	rec, err := s.Get(ctx, id)
	if err != nil {
		return domain.PromptRecord{}, err
	}
	if err := validatePromptForProvider(rec.Provider, content); err != nil {
		return domain.PromptRecord{}, err
	}
	return s.repo.UpdateDraft(ctx, id, note, content)
}

func (s *PromptService) Publish(ctx context.Context, id int64) error {
	return s.repo.Publish(ctx, id)
}

func (s *PromptService) Rollback(ctx context.Context, promptID string, version int) error {
	return s.repo.Rollback(ctx, promptID, version)
}

func validatePromptForProvider(provider string, content domain.Prompt) error {
	switch provider {
	case domain.PromptProviderOpenAI:
		return domain.ValidateOpenAIPrompt(content.OpenAI)
	case domain.PromptProviderJev:
		return domain.ValidateSystemOnePrompt(content.SystemOne)
	default:
		return fmt.Errorf("intent: unsupported prompt provider")
	}
}

// DecodePromptRequest parses a provider-specific prompt from admin JSON.
// DecodePromptRequest 从管理 JSON 解析提供方专属 Prompt。
func DecodePromptRequest(provider string, raw json.RawMessage) (domain.Prompt, error) {
	p, err := domain.ParsePromptContent(provider, raw)
	if err != nil {
		return domain.Prompt{}, err
	}
	return p, nil
}

var ErrPromptStoreUnavailable = errors.New("intent: prompt store unavailable")
