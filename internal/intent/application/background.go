package application

import (
	"context"
	"encoding/json"
	"slices"
	"strconv"

	"github.com/HappyLadySauceM/Nagi/internal/intent/domain"
)

func (s *PromptService) IntentRoleBackground(ctx context.Context, in domain.Input) (*domain.PromptIntentContext, []domain.BackgroundRoleLayer, bool) {
	task := in.Task
	if task.BotID <= 0 || task.Kind != domain.PromptScopeGroup {
		return nil, nil, true
	}
	p := s.ResolveFor(ctx, domain.PromptIDChatReply, domain.PromptKindChatReply, domain.PromptProviderOpenAI, task.BotID, task.Kind, task.TargetID, intentRoleVariables(in))
	switch p.FallbackReason {
	case "prompt_store_error", "scope_supplement_error", "invalid_local_prompt", "invalid_scope_supplement", "layered_prompt_store_unavailable":
		return nil, nil, false
	}
	if RoleBackgroundHash(p.IntentContext) == RoleBackgroundHash(nil) {
		return nil, nil, true
	}
	var refs []domain.BackgroundRoleLayer
	for _, layer := range p.Layers {
		if layer.VersionID == 0 {
			continue
		}
		scope := domain.PromptScopeAccount
		switch layer.Layer {
		case domain.PromptLayerAccountDefault:
			if p.FallbackReason == "legacy_global_prompt" {
				continue
			}
		case domain.PromptLayerScopeSupplement:
			if task.Kind != domain.PromptScopeGroup {
				continue
			}
			scope = domain.PromptScopeGroup
		default:
			continue
		}
		refs = append(refs, domain.BackgroundRoleLayer{VersionID: layer.VersionID, Version: layer.Version, Scope: scope})
	}
	return p.IntentContext, refs, true
}

func (s *PromptService) FrozenIntentRole(ctx context.Context, in domain.Input, refs []domain.BackgroundRoleLayer, hash string) (*domain.PromptIntentContext, bool) {
	if len(refs) == 0 {
		return nil, hash == RoleBackgroundHash(nil)
	}
	if len(refs) > 2 || s == nil || s.repo == nil {
		return nil, false
	}
	task := in.Task
	var role domain.PromptIntentContext
	seen := map[string]bool{}
	for _, ref := range refs {
		if seen[ref.Scope] || (ref.Scope != domain.PromptScopeAccount && (ref.Scope != domain.PromptScopeGroup || task.Kind != domain.PromptScopeGroup)) {
			return nil, false
		}
		seen[ref.Scope] = true
		row, err := s.repo.Get(ctx, ref.VersionID)
		if err != nil || row.BotID != task.BotID || row.ScopeType != ref.Scope || row.ScopeID != 0 || row.Version != ref.Version || row.PromptID != domain.PromptIDChatReply || row.Kind != domain.PromptKindChatReply || row.Provider != domain.PromptProviderOpenAI || (row.Status != domain.PromptStatusPublished && row.Status != domain.PromptStatusArchived) {
			return nil, false
		}
		var p domain.Prompt
		if ref.Scope == domain.PromptScopeAccount {
			p, err = domain.ParsePromptContentForWithVariables(row.Kind, row.Provider, row.Content, withContentVariables(s.customVariableNames(), row.Content))
		} else {
			p, err = decodePromptSupplement(row.Kind, row.Provider, row.Content, s.customVariableNames())
		}
		if err != nil || domain.ValidatePromptIntentContext(row.Kind, row.ScopeType, p.IntentContext) != nil {
			return nil, false
		}
		if p.IntentContext == nil {
			continue
		}
		if ref.Scope == domain.PromptScopeAccount {
			role.Identity = p.IntentContext.Identity
			role.Responsibilities = slices.Clone(p.IntentContext.Responsibilities)
		} else {
			role.GroupConventions = slices.Clone(p.IntentContext.GroupConventions)
		}
	}
	variables := fillRuntimePromptVariables(mergePromptVariables(s.customVariableMap(), intentRoleVariables(in)), task.BotID, task.Kind, task.TargetID)
	rendered := domain.RenderPromptVariables(domain.Prompt{IntentContext: &role}, variables).IntentContext
	return rendered, hash == RoleBackgroundHash(rendered)
}

func intentRoleVariables(in domain.Input) map[string]string {
	return map[string]string{
		"bot_name":  in.CallName(),
		"sender_id": strconv.FormatInt(in.Task.Message.SenderID, 10),
	}
}

func RoleBackgroundHash(role *domain.PromptIntentContext) string {
	if role == nil || (role.Identity == "" && len(role.Responsibilities) == 0 && len(role.GroupConventions) == 0) {
		return domain.PromptTextHash("")
	}
	content, _ := json.Marshal(role)
	return domain.PromptTextHash(string(content))
}
