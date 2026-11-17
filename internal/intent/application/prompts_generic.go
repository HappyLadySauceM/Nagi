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
	// Custom variables are the baseline; dynamic caller values win on conflicts.
	// 自定义变量作为基线；同名时调用方传入的动态值优先。
	variables = mergePromptVariables(s.customVariableMap(), variables)
	variables = fillRuntimePromptVariables(variables, botID, targetType, targetID)
	extra := s.customVariableNames()
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
		parsed, parseErr := domain.ParsePromptContentForWithVariables(kind, provider, account.Content, withContentVariables(extra, account.Content))
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
			supplementPrompt, parseErr := domain.ParsePromptContentForWithVariables(kind, provider, supplement.Content, withContentVariables(extra, supplement.Content))
			// Merge only needs to tolerate variables already present in stored
			// content; save-time validation stays strict.
			// 合并只需容忍已存储内容里既有的变量；保存时校验仍严格。
			mergeExtra := withContentVariables(withContentVariables(extra, account.Content), supplement.Content)
			// ParsePromptContentFor validates full content, so supplements are
			// parsed leniently through their own schema first.
			if parseErr == nil {
				if merged, mergeErr := domain.MergePromptContentWithVariables(kind, provider, final, supplementPrompt, mergeExtra); mergeErr == nil {
					final = merged
				} else {
					s.logger.Warn("invalid scope supplement; ignoring", "error", mergeErr, "prompt_id", promptID)
				}
			} else {
				lenient, lenientErr := decodePromptSupplement(kind, provider, supplement.Content, extra)
				if lenientErr != nil {
					s.logger.Warn("invalid scope supplement; ignoring", "error", lenientErr, "prompt_id", promptID)
				} else if merged, mergeErr := domain.MergePromptContentWithVariables(kind, provider, final, lenient, mergeExtra); mergeErr == nil {
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

// withContentVariables lets stored templates keep resolving after a custom
// variable was retired; save-time validation stays strict.
// withContentVariables 合并 extra 与已存储内容中出现的变量名，
// 使已发布模板在自定义变量被删除后仍可解析；保存时校验仍保持严格。
func withContentVariables(extra []string, content json.RawMessage) []string {
	if len(content) == 0 {
		return extra
	}
	refs := promptContentVariableRefs(content)
	if len(refs) == 0 {
		return extra
	}
	seen := make(map[string]struct{}, len(extra)+len(refs))
	merged := make([]string, 0, len(extra)+len(refs))
	for _, name := range append(append([]string(nil), extra...), refs...) {
		if name == "" {
			continue
		}
		if _, ok := seen[name]; ok {
			continue
		}
		seen[name] = struct{}{}
		merged = append(merged, name)
	}
	return merged
}

// mergePromptVariables overlays override on base; override wins on duplicates.
// mergePromptVariables 把 override 叠加到 base 上；同名时 override 优先。
func mergePromptVariables(base, override map[string]string) map[string]string {
	if len(base) == 0 && len(override) == 0 {
		return nil
	}
	out := make(map[string]string, len(base)+len(override))
	for key, value := range base {
		out[key] = value
	}
	for key, value := range override {
		out[key] = value
	}
	return out
}

// fillRuntimePromptVariables adds runtime values derived from ResolveFor
// arguments without overriding keys already present in variables.
// fillRuntimePromptVariables 补齐由 ResolveFor 参数得到的运行时变量，
// 不覆盖 variables 中已存在的键。
func fillRuntimePromptVariables(variables map[string]string, botID int64, targetType string, targetID int64) map[string]string {
	set := func(key, value string) {
		if value == "" {
			return
		}
		if _, ok := variables[key]; ok {
			return
		}
		if variables == nil {
			variables = make(map[string]string)
		}
		variables[key] = value
	}
	if botID > 0 {
		set("bot_id", fmt.Sprintf("%d", botID))
	}
	if targetID > 0 {
		set("target_id", fmt.Sprintf("%d", targetID))
	}
	if targetType != "" {
		set("scope_type", targetType)
	}
	return variables
}

func decodePromptSupplement(kind, provider string, content json.RawMessage, extra []string) (domain.Prompt, error) {
	var raw struct {
		Provider  string
		SystemOne *domain.SystemOnePrompt
		OpenAI    *domain.OpenAIPrompt
	}
	if err := json.Unmarshal(content, &raw); err != nil {
		return domain.Prompt{}, fmt.Errorf("invalid supplement content: %w", err)
	}
	p := domain.Prompt{Kind: kind, Provider: provider, SystemOne: raw.SystemOne, OpenAI: raw.OpenAI}
	if err := domain.ValidatePromptSupplementWithVariables(kind, provider, &p, withContentVariables(extra, content)); err != nil {
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
	extra := s.customVariableNames()
	if scopeType == domain.PromptScopeAccount {
		if err := domain.ValidatePromptForKindWithVariables(kind, provider, &final, extra); err != nil {
			return domain.PromptRecord{}, err
		}
	} else {
		if err := domain.ValidatePromptSupplementWithVariables(kind, provider, &final, extra); err != nil {
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
	extra := s.customVariableNames()
	if rec.ScopeType == domain.PromptScopeAccount {
		if err := domain.ValidatePromptForKindWithVariables(rec.Kind, rec.Provider, &content, extra); err != nil {
			return domain.PromptRecord{}, err
		}
	} else {
		if err := domain.ValidatePromptSupplementWithVariables(rec.Kind, rec.Provider, &content, extra); err != nil {
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
	extra := s.customVariableNames()
	base := domain.BuiltinPromptFor(kind, provider)
	if rec, found, err := repo.ActiveLayer(ctx, domain.PromptKey{PromptID: promptID, Kind: kind, Provider: provider, BotID: botID, ScopeType: domain.PromptScopeAccount}); err != nil {
		return err
	} else if found {
		parsed, parseErr := parseStoredPrompt(kind, provider, rec.Content, extra)
		if parseErr != nil {
			return parseErr
		}
		base = parsed
	}
	_, err := domain.MergePromptContentWithVariables(kind, provider, base, supplement, extra)
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
	extra := s.customVariableNames()
	key := domain.PromptKey{PromptID: promptID, Kind: kind, Provider: provider, BotID: botID, ScopeType: scopeType}
	switch base {
	case "auto":
		if rec, found, err := repo.ActiveLayer(ctx, key); err != nil {
			return domain.Prompt{}, err
		} else if found {
			return parseStoredPrompt(kind, provider, rec.Content, extra)
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
		return parseStoredPrompt(kind, provider, rec.Content, extra)
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
		return parseStoredPrompt(kind, provider, rec.Content, extra)
	default:
		return domain.Prompt{}, fmt.Errorf("intent: unsupported draft base")
	}
}

func parseStoredPrompt(kind, provider string, content json.RawMessage, extra []string) (domain.Prompt, error) {
	parsed, err := domain.ParsePromptContentForWithVariables(kind, provider, content, withContentVariables(extra, content))
	if err == nil {
		return parsed, nil
	}
	return decodePromptSupplement(kind, provider, content, extra)
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
	extra := s.customVariableNames()
	p, parseErr := domain.ParsePromptContentForWithVariables(kind, provider, rec.Content, withContentVariables(extra, rec.Content))
	if parseErr != nil {
		// Supplements are validated leniently.
		p, err = decodePromptSupplement(kind, provider, rec.Content, extra)
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
