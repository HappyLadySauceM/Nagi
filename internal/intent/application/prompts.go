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
	"strings"
	"sync"
	"time"

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

// RemotePromptClient fetches one prompt version from an optional remote source.
// A nil client keeps the prompt store fully local.
// RemotePromptClient 从可选远端来源获取一个 Prompt 版本。
// 为 nil 时 Prompt 存储完全本地运行。
type RemotePromptClient interface {
	Configured() bool
	Fetch(ctx context.Context, name string, version int, label string) (domain.RemotePrompt, error)
}

// PromptService resolves runtime prompts and manages local versions.
// PromptService 解析运行时 Prompt 并管理本地版本。
type PromptService struct {
	repo   PromptRepository
	remote RemotePromptClient
	logger *slog.Logger

	mu       sync.Mutex
	attempts map[string]promptSyncAttempt
}

// promptSyncAttempt is the last explicit sync outcome kept in memory for the
// status endpoint. Failure details are operational state, not prompt content.
// promptSyncAttempt 是状态接口使用的最近一次显式同步结果。
// 失败详情是运行状态，不是 Prompt 内容。
type promptSyncAttempt struct {
	status    string
	reason    string
	message   string
	at        time.Time
	succeeded bool
}

func NewPromptService(repo PromptRepository, logger *slog.Logger, remote ...RemotePromptClient) *PromptService {
	if logger == nil {
		logger = slog.Default()
	}
	svc := &PromptService{repo: repo, logger: logger, attempts: make(map[string]promptSyncAttempt)}
	if len(remote) > 0 {
		svc.remote = remote[0]
	}
	return svc
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

// SyncStatus reports the optional remote sync state without touching the
// active local prompt. It never contacts Langfuse.
// SyncStatus 报告可选远端同步状态，不改变当前本地 Prompt，也绝不联系 Langfuse。
func (s *PromptService) SyncStatus(ctx context.Context, promptID, provider string) (domain.PromptSyncStatus, error) {
	if promptID == "" {
		promptID = domain.BuiltinPromptID
	}
	if provider == "" {
		provider = domain.PromptProviderJev
	}
	configured := s != nil && s.remote != nil && s.remote.Configured()
	status := domain.PromptSyncStatus{
		PromptID: promptID, Provider: provider, Configured: configured, Status: domain.PromptSyncStatusIdle,
		Candidates: []domain.PromptRecord{},
	}
	if s == nil || s.repo == nil {
		return domain.PromptSyncStatus{}, fmt.Errorf("intent: prompt store unavailable")
	}
	if !configured {
		status.Status = domain.PromptSyncStatusNotConfigured
		status.ReasonCode = domain.PromptSyncReasonNotConfigured
		status.Message = "未配置 Langfuse，本地 Prompt 不受影响"
	} else if attempt, ok := s.lastAttempt(promptID, provider); ok {
		status.Status = attempt.status
		status.ReasonCode = attempt.reason
		status.Message = attempt.message
		status.LastAttemptAt = attempt.at.Format(time.RFC3339)
		if attempt.succeeded {
			status.LastSuccessAt = attempt.at.Format(time.RFC3339)
		}
	}
	active, found, err := s.repo.Active(ctx, promptID, provider)
	if err != nil {
		return domain.PromptSyncStatus{}, err
	}
	if found {
		status.Active = domain.PromptSyncActive{
			PromptID: active.PromptID, Name: active.Name, Version: active.Version,
			Status: active.Status, Source: active.Source, SourceRef: active.SourceRef, Hash: active.Hash,
		}
	} else {
		builtin := domain.BuiltinPrompt(provider)
		status.Active = domain.PromptSyncActive{
			PromptID: builtin.ID, Name: builtin.Name, Version: builtin.Version,
			Status: domain.PromptStatusPublished, Source: domain.PromptSourceBuiltin, Hash: builtin.Hash,
		}
	}
	records, err := s.repo.Versions(ctx, promptID)
	if err != nil {
		return domain.PromptSyncStatus{}, err
	}
	for _, rec := range records {
		if rec.Provider == provider && rec.Status == domain.PromptStatusDraft && rec.Source == domain.PromptSourceLangfuse {
			status.Candidates = append(status.Candidates, rec)
		}
	}
	return status, nil
}

// SyncRemote explicitly fetches one remote prompt version and stores it as an
// immutable local draft candidate. Activation stays a separate local publish.
// SyncRemote 显式获取一个远端 Prompt 版本，并保存为不可变本地草稿候选。
// 激活仍由独立的本地发布操作完成。
func (s *PromptService) SyncRemote(ctx context.Context, promptID, provider string, version int, label string) (domain.PromptSyncResult, error) {
	if promptID == "" {
		promptID = domain.BuiltinPromptID
	}
	if provider == "" {
		provider = domain.PromptProviderJev
	}
	if s == nil || s.repo == nil {
		return domain.PromptSyncResult{}, fmt.Errorf("intent: prompt store unavailable")
	}
	if provider != domain.PromptProviderJev && provider != domain.PromptProviderOpenAI {
		return domain.PromptSyncResult{}, fmt.Errorf("intent: unsupported prompt provider")
	}
	if s.remote == nil || !s.remote.Configured() {
		s.recordAttempt(promptID, provider, false, domain.PromptSyncReasonNotConfigured, "未配置 Langfuse，本地 Prompt 不受影响")
		return domain.PromptSyncResult{}, domain.ErrPromptRemoteNotConfigured
	}
	remote, err := s.remote.Fetch(ctx, promptID, version, label)
	if err != nil {
		code, message := promptSyncError(err)
		s.recordAttempt(promptID, provider, false, code, message)
		return domain.PromptSyncResult{}, err
	}
	if remote.Name != promptID {
		s.recordAttempt(promptID, provider, false, domain.PromptSyncReasonInvalid, "Langfuse Prompt 名称与本地 Prompt ID 不一致")
		return domain.PromptSyncResult{}, fmt.Errorf("%w: name mismatch", domain.ErrPromptRemoteInvalid)
	}
	content, err := decodeRemotePromptContent(provider, remote.Prompt)
	if err != nil {
		s.recordAttempt(promptID, provider, false, domain.PromptSyncReasonInvalid, "Langfuse Prompt 内容不合法")
		return domain.PromptSyncResult{}, fmt.Errorf("%w: %v", domain.ErrPromptRemoteInvalid, err)
	}
	if err := validatePromptForProvider(provider, content); err != nil {
		s.recordAttempt(promptID, provider, false, domain.PromptSyncReasonInvalid, "Langfuse Prompt 内容不合法")
		return domain.PromptSyncResult{}, fmt.Errorf("%w: %v", domain.ErrPromptRemoteInvalid, err)
	}
	hash := domain.PromptHash(content)
	sourceRef := fmt.Sprintf("langfuse:%s@%d", remote.Name, remote.Version)
	records, err := s.repo.Versions(ctx, promptID)
	if err != nil {
		return domain.PromptSyncResult{}, err
	}
	for _, rec := range records {
		if rec.SourceRef != sourceRef {
			continue
		}
		if rec.Hash != hash {
			s.recordAttempt(promptID, provider, false, domain.PromptSyncReasonVersionConflict, "同一 Langfuse 版本的内容哈希发生变化")
			return domain.PromptSyncResult{}, domain.ErrPromptRemoteVersionConflict
		}
		s.recordAttempt(promptID, provider, true, "", "该 Langfuse 版本已同步")
		return domain.PromptSyncResult{
			PromptID: promptID, Provider: provider, RemoteName: remote.Name, RemoteVersion: remote.Version,
			Status: "already_synced", Candidate: rec,
		}, nil
	}
	note := "Langfuse " + sourceRef
	rec, err := s.repo.CreateDraft(ctx, promptID, remote.Name, provider, content, note, domain.PromptSourceLangfuse, sourceRef)
	if err != nil {
		return domain.PromptSyncResult{}, err
	}
	s.recordAttempt(promptID, provider, true, "", "Langfuse Prompt 已写入本地候选，等待本地验证后发布")
	return domain.PromptSyncResult{
		PromptID: promptID, Provider: provider, RemoteName: remote.Name, RemoteVersion: remote.Version,
		Status: "candidate_created", Candidate: rec,
	}, nil
}

func (s *PromptService) recordAttempt(promptID, provider string, succeeded bool, reason, message string) {
	status := domain.PromptSyncStatusFailed
	if succeeded {
		status = domain.PromptSyncStatusOK
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.attempts == nil {
		s.attempts = make(map[string]promptSyncAttempt)
	}
	s.attempts[promptID+"|"+provider] = promptSyncAttempt{
		status: status, reason: reason, message: message, at: time.Now(), succeeded: succeeded,
	}
}

func (s *PromptService) lastAttempt(promptID, provider string) (promptSyncAttempt, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	attempt, ok := s.attempts[promptID+"|"+provider]
	return attempt, ok
}

// decodeRemotePromptContent accepts either the local JSON content object or a
// Langfuse text prompt containing that JSON object. Plain text is rejected:
// prompt content is typed data, not free-form instruction text.
// decodeRemotePromptContent 接受本地 JSON 内容对象，或包含该 JSON 对象的 Langfuse 文本 Prompt。
// 纯文本会被拒绝：Prompt 内容是类型化数据，而不是自由指令文本。
func decodeRemotePromptContent(provider string, raw json.RawMessage) (domain.Prompt, error) {
	try := func(data []byte) (domain.Prompt, error) {
		return domain.ParsePromptContent(provider, data)
	}
	if parsed, err := try(raw); err == nil {
		return parsed, nil
	}
	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		if parsed, err := try([]byte(text)); err == nil {
			return parsed, nil
		}
	}
	var wrapped struct {
		Content json.RawMessage `json:"content"`
		Text    string          `json:"text"`
	}
	if err := json.Unmarshal(raw, &wrapped); err == nil {
		if len(wrapped.Content) > 0 {
			if parsed, err := try(wrapped.Content); err == nil {
				return parsed, nil
			}
		}
		if strings.TrimSpace(wrapped.Text) != "" {
			if parsed, err := try([]byte(wrapped.Text)); err == nil {
				return parsed, nil
			}
		}
	}
	return domain.Prompt{}, fmt.Errorf("unsupported prompt content shape")
}

// PromptSyncError returns the stable reason code and safe message for one sync error.
// PromptSyncError 返回一次同步错误对应的稳定原因码与安全信息。
func PromptSyncError(err error) (string, string) {
	return promptSyncError(err)
}

func promptSyncError(err error) (string, string) {
	switch {
	case errors.Is(err, domain.ErrPromptRemoteNotConfigured):
		return domain.PromptSyncReasonNotConfigured, "未配置 Langfuse，本地 Prompt 不受影响"
	case errors.Is(err, domain.ErrPromptRemoteNotFound):
		return domain.PromptSyncReasonNotFound, "Langfuse 中不存在指定 Prompt 或版本"
	case errors.Is(err, domain.ErrPromptRemoteInvalid):
		return domain.PromptSyncReasonInvalid, "Langfuse Prompt 内容不合法"
	case errors.Is(err, domain.ErrPromptRemoteTimeout):
		return domain.PromptSyncReasonTimeout, "Langfuse 请求超时，已保留本地已发布版本"
	case errors.Is(err, domain.ErrPromptRemoteAuth):
		return domain.PromptSyncReasonAuth, "Langfuse 凭据无效"
	case errors.Is(err, domain.ErrPromptRemoteVersionConflict):
		return domain.PromptSyncReasonVersionConflict, "同一 Langfuse 版本的内容哈希发生变化"
	case errors.Is(err, domain.ErrPromptRemoteUnreachable):
		return domain.PromptSyncReasonUnreachable, "Langfuse 不可达，已保留本地已发布版本"
	default:
		return "", ""
	}
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
