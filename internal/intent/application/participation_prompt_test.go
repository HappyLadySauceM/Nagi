package application

import (
	"testing"

	"github.com/HappyLadySauceM/Nagi/internal/intent/domain"
)

func TestComposeRulesetPromptAppliesWordingWithoutChangingPolicy(t *testing.T) {
	ruleset := domain.Ruleset{ID: 11, Version: 4, Questions: map[string]domain.PromptQuestion{
		"addressed": {Type: "noul", Role: "custom", Scope: "group", Order: 3, Threshold: 0.9, Action: domain.ActionSilent, Instructions: "正在跟别人说话？"},
		"private":   {Type: "noul", Scope: "private", Order: 7, Threshold: 0.8, Action: domain.ActionReply, Instructions: "只在私聊生效？"},
	}}
	account := domain.Prompt{Provider: "jev", SystemOne: &domain.SystemOnePrompt{Questions: map[string]domain.PromptQuestion{
		"addressed": {Instructions: "是否在问另一位{{group_role}}？", Order: 50, Threshold: 0.1, Action: domain.ActionReply},
		"unlisted":  {Instructions: "这题不在规则集里"},
	}}}
	group := domain.Prompt{Provider: "jev", SystemOne: &domain.SystemOnePrompt{Questions: map[string]domain.PromptQuestion{
		"addressed": {Instructions: "检查是否明确指向 {{bot_name}}。"},
	}}}
	wording := domain.Prompt{
		Variables: map[string]string{"group_role": "群友", "bot_name": "旧名称"},
		Layers: []domain.PromptLayer{
			{Layer: domain.PromptLayerAccountDefault, Version: 8, VersionID: 81, Content: domain.PromptContentJSON(account)},
			{Layer: domain.PromptLayerScopeSupplement, Version: 2, VersionID: 82, Content: domain.PromptContentJSON(group)},
		},
	}
	result := ComposeRulesetPrompt(ruleset, wording, 100, "group", 700, map[string]string{"bot_name": "Nagi"})
	got := result.SystemOne.Questions["addressed"]
	if got.Instructions != "是否在问另一位群友？\n\n检查是否明确指向 Nagi。" {
		t.Fatalf("rendered wording = %q", got.Instructions)
	}
	if got.Action != domain.ActionSilent || got.Order != 3 || got.Threshold != 0.9 || len(result.SystemOne.Questions) != 1 {
		t.Fatalf("wording changed policy: %+v", result.SystemOne.Questions)
	}
	if result.Layers[0].VersionID != 11 || result.Layers[1].VersionID != 81 ||
		result.Layers[2].VersionID != 82 || result.RenderedHash != domain.PromptHash(result) {
		t.Fatalf("effective versions or hash mismatch: %+v", result)
	}
	unrelated := ComposeRulesetPrompt(ruleset, wording, 100, "private", 200, nil)
	if len(unrelated.Layers) != 1 || unrelated.SystemOne.Questions["private"].Instructions != "只在私聊生效？" {
		t.Fatalf("unrelated wording reached a different policy: %+v", unrelated)
	}
}

func TestComposeRulesetPromptKeepsLegacyClosingQuestionAValidScore(t *testing.T) {
	builtin := domain.BuiltinPromptFor(domain.PromptKindIntentParticipation, domain.PromptProviderJev)
	builtin.SystemOne.Questions["intent"] = domain.PromptQuestion{
		Type: "choice", Role: domain.QuestionRoleIntent, Instructions: "当前消息的主要聊天功能是什么？",
		Criteria: map[string]string{"closing": "结束对话"},
	}
	wording := domain.Prompt{Layers: []domain.PromptLayer{{
		Layer: domain.PromptLayerAccountDefault, VersionID: 12, Content: domain.PromptContentJSON(builtin),
	}}}
	rules := domain.Ruleset{ID: 7, Version: 3, Questions: domain.DefaultDecisionQuestions()}
	withPolicy := ComposeRulesetPrompt(rules, wording, 100, "group", 700, nil)
	withoutPolicy := ScoreParticipationPrompt(builtin)
	want := rules.Questions["intent"].Instructions
	for _, prompt := range []domain.Prompt{withPolicy, withoutPolicy} {
		q := prompt.SystemOne.Questions["intent"]
		if q.Type != "noul" || q.Criteria != nil || q.Instructions != want {
			t.Fatalf("legacy closing question was not normalized: %+v", q)
		}
		if prompt.RenderedHash != domain.PromptHash(prompt) {
			t.Fatal("hash must cover score question")
		}
	}
}
