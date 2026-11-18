// Tests for the versioned ruleset overlay.
// 版本化规则叠加的测试。
package application

import (
	"testing"

	obdomain "github.com/HappyLadySauceM/Nagi/internal/onebot/domain"
	scopedomain "github.com/HappyLadySauceM/Nagi/internal/scope/domain"

	"github.com/HappyLadySauceM/Nagi/internal/intent/domain"
)

func TestApplyRulesetReplySilentAndSemantic(t *testing.T) {
	entries := []domain.RuleEntry{
		{Priority: 0, Match: domain.RuleMatch{TextContains: "闭嘴"}, Action: domain.RuleAction{Type: domain.ActionSilent, ReasonCode: "muted_phrase"}},
		{Priority: 1, Match: domain.RuleMatch{Explicit: boolPtr(true)}, Action: domain.RuleAction{Type: domain.ActionReply, ReasonCode: "rule_explicit"}},
		{Priority: 2, Match: domain.RuleMatch{Kind: "group"}, Action: domain.RuleAction{Type: domain.ActionRequireSemantic, ReasonCode: "rule_semantic"}},
	}

	if d := ApplyRuleset(domain.RuleResult{}, baseInput(scopedomain.ModeIntentBased), entries); !d.NeedSemantic {
		t.Fatalf("require_semantic rule should set NeedSemantic: %+v", d)
	}
	base := EvaluateRules(baseInput(scopedomain.ModeIntentBased))

	in := baseInput(scopedomain.ModeIntentBased)
	in.Task.Message.Text = "请闭嘴"
	if d := ApplyRuleset(base, in, entries); !d.Blocked || d.ReasonCode != "muted_phrase" {
		t.Fatalf("silent rule = %+v", d)
	}

	addressed := baseInput(scopedomain.ModeIntentBased)
	addressed.Task.Segments = []obdomain.Segment{domainSegmentAt("100")}
	if d := ApplyRuleset(base, addressed, entries); !d.ForceReply || d.ForceReason != "rule_explicit" {
		t.Fatalf("reply rule = %+v", d)
	}
}

func TestApplyRulesetKeepsHardVeto(t *testing.T) {
	entries := []domain.RuleEntry{{Priority: 0, Action: domain.RuleAction{Type: domain.ActionReply, ReasonCode: "always"}}}
	blocked := domain.RuleResult{Blocked: true, ReasonCode: "observe_only"}
	if got := ApplyRuleset(blocked, baseInput(scopedomain.ModeObserveOnly), entries); !got.Blocked || got.ReasonCode != "observe_only" {
		t.Fatalf("hard veto lost: %+v", got)
	}
}

func TestRulesetValidation(t *testing.T) {
	bad := []domain.RuleEntry{
		{Priority: 0, Action: domain.RuleAction{Type: "shout", ReasonCode: "x"}},
		{Priority: 0, Action: domain.RuleAction{Type: domain.ActionReply}},
		{Priority: 0, Match: domain.RuleMatch{Kind: "channel"}, Action: domain.RuleAction{Type: domain.ActionReply, ReasonCode: "x"}},
	}
	for _, entry := range bad {
		if err := (domain.Ruleset{Entries: []domain.RuleEntry{entry}}).Validate(); err == nil {
			t.Fatalf("entry %+v should be rejected", entry)
		}
	}
	dup := domain.Ruleset{Entries: []domain.RuleEntry{
		{Priority: 1, Action: domain.RuleAction{Type: domain.ActionReply, ReasonCode: "a"}},
		{Priority: 1, Action: domain.RuleAction{Type: domain.ActionSilent, ReasonCode: "b"}},
	}}
	if err := dup.Validate(); err == nil {
		t.Fatal("duplicate priority should be rejected")
	}
}

func boolPtr(v bool) *bool { return &v }

func TestDisabledEntriesAndSemanticOverride(t *testing.T) {
	off := false
	in := baseInput(scopedomain.ModeAlwaysReply)
	entries := []domain.RuleEntry{
		{Priority: 0, Enabled: &off, Action: domain.RuleAction{Type: domain.ActionSilent, ReasonCode: "disabled"}},
		{Priority: 1, Action: domain.RuleAction{Type: domain.ActionRequireSemantic, ReasonCode: "semantic"}},
	}
	rule := ApplyRuleset(EvaluateRules(in), in, entries)
	if !rule.Matched || !rule.NeedSemantic || rule.Blocked || rule.AlwaysReply || rule.Explicit {
		t.Fatalf("semantic override: %+v", rule)
	}
}

func TestDeletedQuestionsDoNotBypassHardGates(t *testing.T) {
	in := baseInput(scopedomain.ModeIntentBased)
	semantic := domain.SemanticAnswer{Available: true, DirectedToBot: 0.9, IntentCategory: "closing"}
	rule := EvaluateRules(in)
	if got := EvaluateReplyPolicy(in, rule, semantic); got.ReasonCode != "closing" {
		t.Fatalf("closing: %+v", got)
	}
	questions := domain.DefaultDecisionQuestions()
	delete(questions, "intent")
	rule.Questions = questions
	if got := EvaluateReplyPolicy(in, rule, semantic); got.ReasonCode != "directed_to_bot" {
		t.Fatalf("deleted closing: %+v", got)
	}
	delete(questions, "directed_to_bot")
	if got := EvaluateReplyPolicy(in, rule, semantic); got.ReasonCode != "ambiguous" {
		t.Fatalf("deleted directed: %+v", got)
	}
	in.Policy.Enabled = false
	rule = EvaluateRules(in)
	rule.Questions = questions
	if got := EvaluateReplyPolicy(in, rule, semantic); got.ReasonCode != "policy_disabled" {
		t.Fatalf("hard gate bypassed: %+v", got)
	}
}

func TestEmptyQuestionListDoesNotClaimAmbiguousSemantic(t *testing.T) {
	in := baseInput(scopedomain.ModeIntentBased)
	rule, _ := EvaluateRuleChain(in, &domain.Ruleset{ID: 2, Version: 3}, nil)
	got := EvaluateReplyPolicy(in, rule, domain.SemanticAnswer{})
	if got.Decision != domain.DecisionSilent || got.ReasonCode != "no_decision_question" {
		t.Fatalf("empty group questions = %+v", got)
	}
	in.Task.Kind = "private"
	rule, _ = EvaluateRuleChain(in, &domain.Ruleset{ID: 2, Version: 3}, nil)
	got = EvaluateReplyPolicy(in, rule, domain.SemanticAnswer{})
	if got.Decision != domain.DecisionReply || got.ReasonCode != "private_normal" {
		t.Fatalf("empty private questions = %+v", got)
	}
}

func TestRuleChainSpecificReplacesGlobalQuestions(t *testing.T) {
	in := baseInput(scopedomain.ModeIntentBased)
	global := domain.Ruleset{ID: 1, Version: 2, Questions: domain.DefaultDecisionQuestions()}
	specific := domain.Ruleset{ID: 2, Version: 3}
	rule, version := EvaluateRuleChain(in, &specific, &global)
	if version != "scope:2@3" || rule.NeedSemantic || len(rule.Questions) != 0 {
		t.Fatalf("empty specific set = %+v %s", rule, version)
	}
	specific.Questions = domain.DefaultDecisionQuestions()
	rule, version = EvaluateRuleChain(in, &specific, &global)
	if version != "scope:2@3" || !rule.NeedSemantic || len(rule.Questions) != 5 {
		t.Fatalf("specific questions = %+v %s", rule, version)
	}
	rule, version = EvaluateRuleChain(in, nil, &global)
	if version != "global:1@2" || !rule.NeedSemantic || len(rule.Questions) != 5 {
		t.Fatalf("global questions = %+v %s", rule, version)
	}
	in.Policy.Enabled = false
	rule, _ = EvaluateRuleChain(in, &specific, &global)
	if !rule.Blocked || rule.ReasonCode != "policy_disabled" {
		t.Fatalf("hard constraint bypassed: %+v", rule)
	}
}
