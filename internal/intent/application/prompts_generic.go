package application

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/HappyLadySauceM/Nagi/internal/intent/domain"
)

// LayeredPromptRepository is the scoped prompt store used by the unified
// three-kind prompt service. The legacy PromptRepository remains for the old
// intent-only compatibility endpoint.
type LayeredPromptRepository interface {
	PromptRepository
	ActiveLayer(ctx context.Context, key domain.PromptKey) (domain.PromptRecord, bool, error)
	HasLayer(ctx context.Context, key domain.PromptKey) (bool, error)
	ListAll(ctx context.Context) ([]domain.PromptRecord, error)
	VersionsLayer(ctx context.Context, key domain.PromptKey) ([]domain.PromptRecord, error)
	CreateDraftVersion(ctx context.Context, input domain.PromptDraftInput) (domain.PromptRecord, error)
	PublishVersion(ctx context.Context, id int64) (domain.PromptRecord, int64, error)
	RollbackVersion(ctx context.Context, key domain.PromptKey, versionID int64) (domain.PromptRecord, int64, error)
	RestoreBuiltin(ctx context.Context, key domain.PromptKey) (int64, error)
}

// LayeredPromptProvider resolves one prompt for a concrete runtime target.
type LayeredPromptProvider interface {
	ResolveFor(ctx context.Context, promptID, kind, provider string, botID int64, targetType string, targetID int64, variables map[string]string) domain.Prompt
}

func (s *PromptService) layered() (LayeredPromptRepository, bool) {
	if s == nil || s.repo == nil {
		return nil, false
	}
	repo, ok := s.repo.(LayeredPromptRepository)
	return repo, ok
}

// ResolveFor resolves account default + group/private type supplement + server
// constraints. It never consults Langfuse and always returns a usable prompt.
func (s *PromptService) ResolveFor(ctx context.Context, promptID, kind, provider string, botID int64, targetType string, targetID int64, variables map[string]string) domain.Prompt {
	if kind == "" {
		kind, _ = domain.PromptKindForID(promptID)
	}
	if promptID == "" {
		promptID = domain.PromptIDForKindOrEmpty(kind)
	}
	builtin := domain.BuiltinPromptFor(kind, provider)
	builtin.BotID = botID
	builtin.TargetType = targetType
	builtin.TargetID = targetID
	builtin.Variables = copyPromptVariables(variables)
	repo, ok := s.layered()
	if !ok {
		builtin.FallbackReason = "layered_prompt_store_unavailable"
		return finalizePrompt(builtin, variables)
	}

	accountKey := domain.PromptKey{PromptID: promptID, Kind: kind, Provider: provider, BotID: botID, ScopeType: domain.PromptScopeAccount}
	account, found, err := repo.ActiveLayer(ctx, accountKey)
	if err != nil {
		s.logger.Warn("load account prompt failed; using builtin", "error", err, "prompt_id", promptID)
		builtin.FallbackReason = "prompt_store_error"
		return finalizePrompt(builtin, variables)
	}
	legacyFallback := false
	if !found && botID != 0 {
		if hasLayer, layerErr := repo.HasLayer(ctx, accountKey); layerErr != nil {
			s.logger.Warn("check account prompt failed; using builtin", "error", layerErr, "prompt_id", promptID)
		} else if !hasLayer {
			account, found, err = repo.ActiveLayer(ctx, domain.PromptKey{PromptID: promptID, Kind: kind, Provider: provider, BotID: 0, ScopeType: domain.PromptScopeAccount})
			if err != nil {
				s.logger.Warn("load legacy prompt failed; using builtin", "error", err, "prompt_id", promptID)
			} else if found {
				legacyFallback = true
			}
		}
	}
	final := builtin
	layers := []domain.PromptLayer{{Layer: domain.PromptLayerAccountDefault, VersionID: 0, Version: 1, Source: domain.PromptSourceBuiltin, Hash: builtin.Hash, Editable: false, Content: domain.PromptContentJSON(builtin)}}
	if found {
		parsed, parseErr := domain.ParsePromptContentFor(kind, provider, account.Content)
		if parseErr != nil {
			s.logger.Warn("invalid account prompt; using builtin", "error", parseErr, "prompt_id", promptID, "version", account.Version)
			builtin.FallbackReason = "invalid_local_prompt"
			return finalizePrompt(builtin, variables)
		}
		parsed.ID = account.PromptID
		parsed.Kind = kind
		parsed.Name = account.Name
		parsed.Version = account.Version
		parsed.Provider = provider
		parsed.Source = account.Source
		parsed.SourceRef = account.SourceRef
		parsed.Hash = account.Hash
		parsed.BotID = botID
		parsed.TargetType = targetType
		parsed.TargetID = targetID
		parsed.Variables = copyPromptVariables(variables)
		parsed.Layers = []domain.PromptLayer{{Layer: domain.PromptLayerAccountDefault, VersionID: account.ID, Version: account.Version, Source: account.Source, Hash: account.Hash, Editable: true, Content: account.Content}}
		if legacyFallback {
			parsed.FallbackReason = "legacy_global_prompt"
		}
		final = parsed
		layers = parsed.Layers
	}
	if targetType == domain.PromptScopeGroup || targetType == domain.PromptScopePrivate {
		supplement, found, err := repo.ActiveLayer(ctx, domain.PromptKey{PromptID: promptID, Kind: kind, Provider: provider, BotID: botID, ScopeType: targetType})
		if err != nil {
			s.logger.Warn("load scope supplement failed; continuing without supplement", "error", err, "prompt_id", promptID, "scope_type", targetType)
			builtin.FallbackReason = "scope_supplement_error"
		} else if found {
			supplementPrompt, parseErr := domain.ParsePromptContentFor(kind, provider, supplement.Content)
			// ParsePromptContentFor validates full content, so supplements are
			// parsed leniently through their own schema first.
			if parseErr == nil {
				if merged, mergeErr := domain.MergePromptContent(kind, provider, final, supplementPrompt); mergeErr == nil {
					final = merged
				} else {
					s.logger.Warn("invalid scope supplement; ignoring", "error", mergeErr, "prompt_id", promptID)
				}
			} else {
				lenient, lenientErr := decodePromptSupplement(kind, provider, supplement.Content)
				if lenientErr != nil {
					s.logger.Warn("invalid scope supplement; ignoring", "error", lenientErr, "prompt_id", promptID)
				} else if merged, mergeErr := domain.MergePromptContent(kind, provider, final, lenient); mergeErr == nil {
					final = merged
				} else {
					s.logger.Warn("invalid scope supplement; ignoring", "error", mergeErr, "prompt_id", promptID)
				}
			}
			layers = append(layers, domain.PromptLayer{Layer: domain.PromptLayerScopeSupplement, VersionID: supplement.ID, Version: supplement.Version, Source: supplement.Source, Hash: supplement.Hash, Editable: true, Content: supplement.Content})
		}
	}
	final.BotID = botID
	final.TargetType = targetType
	final.TargetID = targetID
	final.Variables = copyPromptVariables(variables)
	final.Layers = append(layers, domain.PromptLayer{
		Layer: domain.PromptLayerServerConstraints, Version: 1, Source: domain.PromptServerConstraintsVersion,
		Hash: domain.PromptTextHash(domain.PromptServerConstraintsText), Editable: false,
		Summary: domain.PromptServerConstraintsText,
	})
	final.Hash = domain.PromptHash(final)
	return finalizePrompt(final, variables)
}

func finalizePrompt(p domain.Prompt, variables map[string]string) domain.Prompt {
	if len(p.Layers) == 0 {
		p.Layers = []domain.PromptLayer{
			{Layer: domain.PromptLayerAccountDefault, VersionID: 0, Version: 1, Source: domain.PromptSourceBuiltin, Hash: p.Hash, Editable: false, Content: domain.PromptContentJSON(p)},
			{Layer: domain.PromptLayerServerConstraints, Version: 1, Source: domain.PromptServerConstraintsVersion, Hash: domain.PromptTextHash(domain.PromptServerConstraintsText), Editable: false, Summary: domain.PromptServerConstraintsText},
		}
	}
	p.FinalHash = domain.PromptFinalHash(p)
	p = domain.RenderPromptVariables(p, variables)
	p.RenderedHash = domain.PromptHash(p)
	return p
}

func copyPromptVariables(variables map[string]string) map[string]string {
	if len(variables) == 0 {
		return nil
	}
	out := make(map[string]string, len(variables))
	for key, value := range variables {
		out[key] = value
	}
	return out
}

func decodePromptSupplement(kind, provider string, content json.RawMessage) (domain.Prompt, error) {
	var raw struct {
		Provider  string
		SystemOne *domain.SystemOnePrompt
		OpenAI    *domain.OpenAIPrompt
	}
	if err := json.Unmarshal(content, &raw); err != nil {
		return domain.Prompt{}, fmt.Errorf("invalid supplement content: %w", err)
	}
	p := domain.Prompt{Kind: kind, Provider: provider, SystemOne: raw.SystemOne, OpenAI: raw.OpenAI}
	if err := domain.ValidatePromptSupplement(kind, provider, &p); err != nil {
		return domain.Prompt{}, err
	}
	return p, nil
}

// ListAll returns every stored prompt version for the unified admin API.
func (s *PromptService) ListAll(ctx context.Context) ([]domain.PromptRecord, error) {
	repo, ok := s.layered()
	if !ok {
		return nil, ErrPromptStoreUnavailable
	}
	return repo.ListAll(ctx)
}

// VersionsFor returns versions of one editable layer.
func (s *PromptService) VersionsFor(ctx context.Context, promptID, kind, provider string, botID int64, scopeType string) ([]domain.PromptRecord, error) {
	repo, ok := s.layered()
	if !ok {
		return nil, ErrPromptStoreUnavailable
	}
	return repo.VersionsLayer(ctx, domain.PromptKey{PromptID: promptID, Kind: kind, Provider: provider, BotID: botID, ScopeType: scopeType})
}

// ActiveRecord returns the current published version of one editable layer.
func (s *PromptService) ActiveRecord(ctx context.Context, promptID, kind, provider string, botID int64, scopeType string) (domain.PromptRecord, bool, error) {
	repo, ok := s.layered()
	if !ok {
		return domain.PromptRecord{}, false, ErrPromptStoreUnavailable
	}
	return repo.ActiveLayer(ctx, domain.PromptKey{PromptID: promptID, Kind: kind, Provider: provider, BotID: botID, ScopeType: scopeType})
}

// CreateDraftFor creates a draft from content or a named base.
func (s *PromptService) CreateDraftFor(ctx context.Context, promptID, kind, provider string, botID int64, scopeType string, content *domain.Prompt, base string, baseVersionID int64, name, note, source, sourceRef string) (domain.PromptRecord, error) {
	repo, ok := s.layered()
	if !ok {
		return domain.PromptRecord{}, ErrPromptStoreUnavailable
	}
	if promptID == "" {
		promptID = domain.PromptIDForKindOrEmpty(kind)
	}
	if kind == "" {
		kind, _ = domain.PromptKindForID(promptID)
	}
	if scopeType == "" {
		scopeType = domain.PromptScopeAccount
	}
	if scopeType != domain.PromptScopeAccount && scopeType != domain.PromptScopeGroup && scopeType != domain.PromptScopePrivate {
		return domain.PromptRecord{}, fmt.Errorf("intent: invalid prompt scope")
	}
	if name == "" {
		name = domain.BuiltinPromptFor(kind, provider).Name
	}
	var final domain.Prompt
	if content != nil {
		final = *content
	} else {
		var err error
		final, err = s.resolveDraftBase(ctx, promptID, kind, provider, botID, scopeType, base, baseVersionID)
		if err != nil {
			return domain.PromptRecord{}, err
		}
	}
	if scopeType == domain.PromptScopeAccount {
		if err := domain.ValidatePromptForKind(kind, provider, &final); err != nil {
			return domain.PromptRecord{}, err
		}
	} else {
		if err := domain.ValidatePromptSupplement(kind, provider, &final); err != nil {
			return domain.PromptRecord{}, err
		}
		if err := s.validateSupplementAgainstAccount(ctx, promptID, kind, provider, botID, final); err != nil {
			return domain.PromptRecord{}, err
		}
	}
	if source == "" {
		source = domain.PromptSourceLocal
	}
	return repo.CreateDraftVersion(ctx, domain.PromptDraftInput{PromptID: promptID, Kind: kind, Name: name, Provider: provider, BotID: botID, ScopeType: scopeType, Content: final, Note: note, Source: source, SourceRef: sourceRef})
}

// UpdateDraftFor validates one draft according to its editable layer.
// UpdateDraftFor 按可编辑层校验并保存一个草稿。
func (s *PromptService) UpdateDraftFor(ctx context.Context, id int64, note string, content domain.Prompt) (domain.PromptRecord, error) {
	rec, err := s.Get(ctx, id)
	if err != nil {
		return domain.PromptRecord{}, err
	}
	if rec.Status != domain.PromptStatusDraft {
		return domain.PromptRecord{}, ErrPromptImmutable
	}
	if content.Kind != "" && content.Kind != rec.Kind {
		return domain.PromptRecord{}, fmt.Errorf("intent: invalid prompt kind")
	}
	if content.Provider != "" && content.Provider != rec.Provider {
		return domain.PromptRecord{}, fmt.Errorf("intent: invalid prompt provider")
	}
	if rec.ScopeType == domain.PromptScopeAccount {
		if err := domain.ValidatePromptForKind(rec.Kind, rec.Provider, &content); err != nil {
			return domain.PromptRecord{}, err
		}
	} else {
		if err := domain.ValidatePromptSupplement(rec.Kind, rec.Provider, &content); err != nil {
			return domain.PromptRecord{}, err
		}
		if err := s.validateSupplementAgainstAccount(ctx, rec.PromptID, rec.Kind, rec.Provider, rec.BotID, content); err != nil {
			return domain.PromptRecord{}, err
		}
	}
	return s.repo.UpdateDraft(ctx, id, note, content)
}

func (s *PromptService) validateSupplementAgainstAccount(ctx context.Context, promptID, kind, provider string, botID int64, supplement domain.Prompt) error {
	repo, ok := s.layered()
	if !ok {
		return ErrPromptStoreUnavailable
	}
	base := domain.BuiltinPromptFor(kind, provider)
	if rec, found, err := repo.ActiveLayer(ctx, domain.PromptKey{PromptID: promptID, Kind: kind, Provider: provider, BotID: botID, ScopeType: domain.PromptScopeAccount}); err != nil {
		return err
	} else if found {
		parsed, parseErr := parseStoredPrompt(kind, provider, rec.Content)
		if parseErr != nil {
			return parseErr
		}
		base = parsed
	}
	_, err := domain.MergePromptContent(kind, provider, base, supplement)
	return err
}

func (s *PromptService) resolveDraftBase(ctx context.Context, promptID, kind, provider string, botID int64, scopeType string, base string, baseVersionID int64) (domain.Prompt, error) {
	repo, ok := s.layered()
	if !ok {
		return domain.Prompt{}, ErrPromptStoreUnavailable
	}
	if base == "" {
		base = "auto"
	}
	key := domain.PromptKey{PromptID: promptID, Kind: kind, Provider: provider, BotID: botID, ScopeType: scopeType}
	switch base {
	case "auto":
		if rec, found, err := repo.ActiveLayer(ctx, key); err != nil {
			return domain.Prompt{}, err
		} else if found {
			return parseStoredPrompt(kind, provider, rec.Content)
		}
		if scopeType == domain.PromptScopeAccount {
			return domain.BuiltinPromptFor(kind, provider), nil
		}
		return emptySupplement(kind, provider), nil
	case "builtin":
		if scopeType != domain.PromptScopeAccount {
			return domain.Prompt{}, fmt.Errorf("intent: builtin base is only available for account scope")
		}
		return domain.BuiltinPromptFor(kind, provider), nil
	case "active":
		rec, found, err := repo.ActiveLayer(ctx, key)
		if err != nil {
			return domain.Prompt{}, err
		}
		if !found {
			return domain.Prompt{}, fmt.Errorf("intent: active prompt version not found")
		}
		return parseStoredPrompt(kind, provider, rec.Content)
	case "empty":
		if scopeType == domain.PromptScopeAccount {
			return domain.Prompt{}, fmt.Errorf("intent: empty base is only available for scope supplements")
		}
		return emptySupplement(kind, provider), nil
	case "version":
		if baseVersionID <= 0 {
			return domain.Prompt{}, fmt.Errorf("intent: base_version_id is required")
		}
		rec, err := repo.Get(ctx, baseVersionID)
		if err != nil {
			return domain.Prompt{}, err
		}
		if rec.PromptID != promptID || rec.Kind != kind || rec.Provider != provider || rec.BotID != botID || rec.ScopeType != scopeType {
			return domain.Prompt{}, fmt.Errorf("intent: base version belongs to another prompt layer")
		}
		return parseStoredPrompt(kind, provider, rec.Content)
	default:
		return domain.Prompt{}, fmt.Errorf("intent: unsupported draft base")
	}
}

func parseStoredPrompt(kind, provider string, content json.RawMessage) (domain.Prompt, error) {
	parsed, err := domain.ParsePromptContentFor(kind, provider, content)
	if err == nil {
		return parsed, nil
	}
	return decodePromptSupplement(kind, provider, content)
}

func emptySupplement(kind, provider string) domain.Prompt {
	p := domain.Prompt{Provider: provider, Kind: kind}
	switch kind {
	case domain.PromptKindChatReply, domain.PromptKindMemoryExtraction:
		p.OpenAI = &domain.OpenAIPrompt{SystemPrompt: ""}
	case domain.PromptKindIntentParticipation:
		if provider == domain.PromptProviderJev {
			p.SystemOne = &domain.SystemOnePrompt{Questions: map[string]domain.PromptQuestion{}}
		} else {
			p.OpenAI = &domain.OpenAIPrompt{SystemPrompt: ""}
		}
	}
	return p
}

// PublishVersion publishes one draft through the shared repository lock.
// PublishVersion 通过共享仓储锁发布一个草稿。
func (s *PromptService) PublishVersion(ctx context.Context, id int64) (domain.PromptRecord, int64, error) {
	repo, ok := s.layered()
	if !ok {
		return domain.PromptRecord{}, 0, ErrPromptStoreUnavailable
	}
	return repo.PublishVersion(ctx, id)
}

// RollbackVersion rolls back one archived version in the exact editable layer.
// RollbackVersion 在精确可编辑层内回滚一个归档版本。
func (s *PromptService) RollbackVersion(ctx context.Context, key domain.PromptKey, versionID int64) (domain.PromptRecord, int64, error) {
	repo, ok := s.layered()
	if !ok {
		return domain.PromptRecord{}, 0, ErrPromptStoreUnavailable
	}
	return repo.RollbackVersion(ctx, key, versionID)
}

// RestoreBuiltin removes the local published binding for one layer and returns
// the archived version id, or zero when there was no local published version.
func (s *PromptService) RestoreBuiltin(ctx context.Context, promptID, kind, provider string, botID int64, scopeType string) (int64, error) {
	repo, ok := s.layered()
	if !ok {
		return 0, ErrPromptStoreUnavailable
	}
	return repo.RestoreBuiltin(ctx, domain.PromptKey{PromptID: promptID, Kind: kind, Provider: provider, BotID: botID, ScopeType: scopeType})
}

// Effective is a thin API-facing alias for ResolveFor.
func (s *PromptService) Effective(ctx context.Context, promptID, kind, provider string, botID int64, targetType string, targetID int64) (domain.Prompt, error) {
	return s.ResolveFor(ctx, promptID, kind, provider, botID, targetType, targetID, nil), nil
}

// VersionPrompt returns parsed content for one version, preserving the exact
// stored template used by a run. Soft-deleted versions remain readable so
// historical runs can rebuild their prompt snapshot by version ID.
// VersionPrompt 返回一个版本解析后的内容，保留运行使用的原始模板；
// 软删除版本仍可读取，以支持历史运行按版本 ID 重建 Prompt 快照。
func (s *PromptService) VersionPrompt(ctx context.Context, id int64, kind, provider string) (domain.Prompt, error) {
	if s == nil || s.repo == nil {
		return domain.Prompt{}, ErrPromptStoreUnavailable
	}
	rec, err := s.repo.GetIncludingDeleted(ctx, id)
	if err != nil {
		return domain.Prompt{}, err
	}
	p, parseErr := domain.ParsePromptContentFor(kind, provider, rec.Content)
	if parseErr != nil {
		// Supplements are validated leniently.
		p, err = decodePromptSupplement(kind, provider, rec.Content)
		if err != nil {
			return domain.Prompt{}, err
		}
	}
	p.ID = rec.PromptID
	p.Kind = kind
	p.Name = rec.Name
	p.Version = rec.Version
	p.Provider = provider
	p.Source = rec.Source
	p.SourceRef = rec.SourceRef
	p.Hash = rec.Hash
	p.BotID = rec.BotID
	p.ScopeType = rec.ScopeType
	p.ScopeID = rec.ScopeID
	layer := domain.PromptLayerAccountDefault
	if rec.ScopeType == domain.PromptScopeGroup || rec.ScopeType == domain.PromptScopePrivate {
		layer = domain.PromptLayerScopeSupplement
	}
	p.Layers = []domain.PromptLayer{{Layer: layer, VersionID: rec.ID, Version: rec.Version, Source: rec.Source, Hash: rec.Hash, Editable: true, Content: rec.Content}}
	p.FinalHash = domain.PromptFinalHash(p)
	return p, nil
}
