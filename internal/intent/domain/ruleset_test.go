// Tests for ruleset-level question validation.
// 规则集级别问题集校验的测试。
package domain

import "testing"

// TestRulesetValidateQuestions verifies a ruleset accepts a valid question set
// and reuses the SystemOne prompt rules to reject malformed sets.
// TestRulesetValidateQuestions 验证规则集接受合法问题集，并复用 SystemOne
// Prompt 规则拒绝格式错误的问题集。
func TestRulesetValidateQuestions(t *testing.T) {
	valid := map[string]PromptQuestion{
		"directed_to_bot": {Type: "noul", Role: QuestionRoleDirectedToBot, Weight: 1, Instructions: "是否在对机器人说话？"},
		"intent":          validIntentQuestion("当前意图？"),
	}
	if err := (Ruleset{Questions: valid}).Validate(); err != nil {
		t.Fatalf("valid question set rejected: %v", err)
	}
	if err := (Ruleset{}).Validate(); err != nil {
		t.Fatalf("empty question set rejected: %v", err)
	}

	cases := map[string]map[string]PromptQuestion{
		"intent not choice": {
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
	}
	for name, questions := range cases {
		if err := (Ruleset{Questions: questions}).Validate(); err == nil {
			t.Fatalf("%s: expected rejection", name)
		}
	}
}
