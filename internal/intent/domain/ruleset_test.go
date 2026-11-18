// Tests for ruleset-level question validation.
// 规则集级别问题集校验的测试。
package domain

import "testing"

// TestRulesetValidateQuestions verifies a ruleset accepts a valid question set
// and reuses the SystemOne prompt rules to reject malformed sets.
// TestRulesetValidateQuestions 验证规则集接受合法问题集，并复用 SystemOne
// Prompt 规则拒绝格式错误的问题集。
func TestRulesetValidateQuestions(t *testing.T) {
	intent := validIntentQuestion("当前意图？")
	intent.Order, intent.Action, intent.Scope, intent.Threshold = 10, ActionSilent, QuestionScopeAll, 0.8
	valid := map[string]PromptQuestion{
		"directed_to_bot": {Type: "noul", Role: QuestionRoleDirectedToBot, Weight: 1, Instructions: "是否在对机器人说话？", Order: 50, Threshold: 0.8, Action: ActionReply, Scope: QuestionScopeGroup},
		"intent":          intent,
	}
	if err := (Ruleset{Questions: valid}).Validate(); err != nil {
		t.Fatalf("valid question set rejected: %v", err)
	}
	if err := (Ruleset{}).Validate(); err != nil {
		t.Fatalf("empty question set rejected: %v", err)
	}
	if err := (Ruleset{Questions: DefaultDecisionQuestions()}).Validate(); err != nil {
		t.Fatalf("default decision questions rejected: %v", err)
	}
	named := map[string]PromptQuestion{
		"custom_1": {Type: "noul", Role: QuestionRoleCustom, Weight: 1, Instructions: "是否紧急？", Title: "是否紧急", Order: 60, Threshold: 0.8, Action: ActionReply, Scope: QuestionScopeGroup},
	}
	if err := (Ruleset{Questions: named}).Validate(); err != nil {
		t.Fatalf("named custom question rejected: %v", err)
	}

	cases := map[string]map[string]PromptQuestion{
		"intent missing decision": {
			"intent": {Type: "noul", Role: QuestionRoleIntent, Instructions: "x"},
		},
		"intent missing closing": {
			"intent": {Type: "choice", Role: QuestionRoleIntent, Instructions: "x", Criteria: map[string]string{"chat": "闲聊"}},
		},
		"duplicate role": {
			"directed_to_bot": {Type: "noul", Role: QuestionRoleDirectedToBot, Instructions: "x"},
			"directed_again":  {Type: "noul", Role: QuestionRoleDirectedToBot, Instructions: "y"},
		},
		"unknown role": {
			"note": {Type: "noul", Role: "unknown_role", Instructions: "x"},
		},
		"missing decision": {
			"note": {Type: "noul", Role: QuestionRoleCustom, Weight: 1, Instructions: "是否紧急？"},
		},
		"custom missing title": {
			"note": {Type: "noul", Role: QuestionRoleCustom, Weight: 1, Instructions: "是否紧急？", Order: 60, Threshold: 0.8, Action: ActionReply, Scope: QuestionScopeGroup},
		},
	}
	for name, questions := range cases {
		if err := (Ruleset{Questions: questions}).Validate(); err == nil {
			t.Fatalf("%s: expected rejection", name)
		}
	}
}

func TestWithDecisionDefaultsFillsLegacyQuestions(t *testing.T) {
	legacy := map[string]PromptQuestion{
		"directed_to_bot": {Type: "noul", Role: QuestionRoleDirectedToBot, Instructions: "是否在对机器人说话？"},
		"urgency":         {Type: "noul", Role: QuestionRoleCustom, Instructions: "是否紧急？"},
		"intent":          {Type: "choice", Role: QuestionRoleIntent, Instructions: "当前意图？", Criteria: map[string]string{"closing": "告别"}, Order: 10, Action: ActionReply, Scope: QuestionScopeAll, Match: "chat"},
	}
	got := WithDecisionDefaults(legacy)
	directed := got["directed_to_bot"]
	if directed.Scope != QuestionScopeGroup || directed.Action != ActionReply || directed.Threshold != 0.8 || directed.Instructions != "是否在对机器人说话？" {
		t.Fatalf("legacy directed = %+v", directed)
	}
	urgency := got["urgency"]
	if urgency.Scope != QuestionScopeGroup || urgency.Action != ActionReply || urgency.Threshold != 0.8 {
		t.Fatalf("custom question = %+v", urgency)
	}
	intent := got["intent"]
	if intent.Type != "noul" || intent.Match != "" || intent.Threshold != 0.8 || intent.Action != ActionReply || intent.Scope != QuestionScopeAll || intent.Instructions != "当前意图？" {
		t.Fatalf("choice question was not converted: %+v", intent)
	}
	if len(WithDecisionDefaults(nil)) != 0 || len(WithDecisionDefaults(map[string]PromptQuestion{})) != 0 {
		t.Fatal("empty question lists were filled")
	}
}

func TestDecisionReasonUsesClosingOnlyForSilentClosing(t *testing.T) {
	closing := DefaultDecisionQuestions()["intent"]
	if got := DecisionReason("intent", closing); got != "closing" {
		t.Fatalf("default intent reason = %s", got)
	}
	changed := closing
	changed.Action = ActionReply
	if got := DecisionReason("intent", changed); got != "intent" {
		t.Fatalf("changed intent reason = %s", got)
	}
	custom := PromptQuestion{Role: QuestionRoleCustom, Title: " 是否紧急 "}
	if got := DecisionReason("custom_1", custom); got != "是否紧急" {
		t.Fatalf("custom reason = %s", got)
	}
	if got := DecisionReason("custom_1", PromptQuestion{Role: QuestionRoleCustom}); got != "custom_1" {
		t.Fatalf("untitled custom reason = %s", got)
	}
}
