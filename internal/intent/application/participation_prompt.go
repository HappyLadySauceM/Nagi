package application

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/HappyLadySauceM/Nagi/internal/intent/domain"
)

// activeQuestionRuleset finds the specific or account-wide SystemOne ruleset.
// activeQuestionRuleset 按具体会话、账号全局的顺序读取生效问题规则集。
func activeQuestionRuleset(ctx context.Context, rules RulesProvider, botID int64, kind string, targetID int64) (*domain.Ruleset, error) {
	if rules == nil {
		return nil, nil
	}
	set, found, err := rules.Active(ctx, botID, kind, targetID)
	if err != nil {
		return nil, err
	}
	if !found && (kind != domain.PromptScopeGroup || targetID != 0) {
		set, found, err = rules.Active(ctx, botID, domain.PromptScopeGroup, 0)
		if err != nil {
			return nil, err
		}
	}
	if !found {
		return nil, nil
	}
	return &set, nil
}

// ComposeRulesetPrompt takes question membership and decisions from the ruleset.
// Published prompt layers may supply wording for the same question keys.
// ComposeRulesetPrompt 由规则集决定问题范围和动作，提示词发布层可修改同键问题的措辞。
func ComposeRulesetPrompt(set domain.Ruleset, wording domain.Prompt, botID int64, kind string, targetID int64, variables map[string]string) domain.Prompt {
	questions := domain.ApplicableQuestions(domain.WithDecisionDefaults(set.Questions), kind)
	base := domain.BuiltinPromptFor(domain.PromptKindIntentParticipation, domain.PromptProviderJev)
	base.SystemOne = &domain.SystemOnePrompt{Questions: questions}
	base.Version = set.Version
	base.Source = domain.PromptSourceLocal
	base.BotID, base.TargetType, base.TargetID = botID, kind, targetID
	base.Layers = []domain.PromptLayer{{
		Layer: "participation_ruleset", VersionID: set.ID, Version: set.Version,
		Source: domain.PromptSourceLocal, Hash: domain.PromptHash(base),
	}}
	for _, layer := range wording.Layers {
		if layer.VersionID <= 0 || (layer.Layer != domain.PromptLayerAccountDefault && layer.Layer != domain.PromptLayerScopeSupplement) {
			continue
		}
		var content struct{ SystemOne *domain.SystemOnePrompt }
		if json.Unmarshal(layer.Content, &content) != nil || content.SystemOne == nil {
			continue
		}
		isSupplement := layer.Layer == domain.PromptLayerScopeSupplement
		clauses := content.SystemOne.Questions
		if !isSupplement {
			clauses = domain.WithDecisionDefaults(clauses)
		}
		if applyQuestionWording(questions, clauses, isSupplement) {
			base.Layers = append(base.Layers, layer)
		}
	}
	variables = fillRuntimePromptVariables(mergePromptVariables(wording.Variables, variables), botID, kind, targetID)
	base.Variables = variables
	base.Hash = domain.PromptHash(base)
	base.FinalHash = domain.PromptFinalHash(base)
	base = domain.RenderPromptVariables(base, variables)
	base.RenderedHash = domain.PromptHash(base)
	return base
}

func applyQuestionWording(questions, wording map[string]domain.PromptQuestion, supplement bool) bool {
	applied := false
	for key, clause := range wording {
		question, exists := questions[key]
		instructions := strings.TrimSpace(clause.Instructions)
		if !exists || instructions == "" {
			continue
		}
		if supplement && strings.TrimSpace(question.Instructions) != "" {
			instructions = strings.TrimSpace(question.Instructions) + "\n\n" + instructions
		}
		question.Instructions = instructions
		questions[key] = question
		applied = true
	}
	return applied
}

// ScoreParticipationPrompt normalizes legacy category questions to the score
// contract and keeps the hash in sync with the questions used in runtime.
// ScoreParticipationPrompt 将旧分类题转为运行中的分数题，并同步渲染哈希。
func ScoreParticipationPrompt(prompt domain.Prompt) domain.Prompt {
	if prompt.SystemOne == nil || len(prompt.SystemOne.Questions) == 0 {
		return prompt
	}
	prompt.SystemOne = &domain.SystemOnePrompt{Questions: domain.WithDecisionDefaults(prompt.SystemOne.Questions)}
	prompt.RenderedHash = domain.PromptHash(prompt)
	return prompt
}

// ApplyActiveQuestionRuleset composes the actual SystemOne prompt for a preview target.
// ApplyActiveQuestionRuleset 组合预览目标实际使用的 SystemOne 提示词。
func ApplyActiveQuestionRuleset(ctx context.Context, rules RulesProvider, prompt domain.Prompt) (domain.Prompt, error) {
	if prompt.Kind != domain.PromptKindIntentParticipation || prompt.Provider != domain.PromptProviderJev {
		return prompt, nil
	}
	ruleset, err := activeQuestionRuleset(ctx, rules, prompt.BotID, prompt.TargetType, prompt.TargetID)
	if err != nil || ruleset == nil {
		return prompt, err
	}
	return ComposeRulesetPrompt(*ruleset, prompt, prompt.BotID, prompt.TargetType, prompt.TargetID, prompt.Variables), nil
}
