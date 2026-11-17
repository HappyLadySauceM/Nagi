package application

import (
	"testing"
	"time"

	scopedomain "github.com/HappyLadySauceM/Nagi/internal/scope/domain"

	"github.com/HappyLadySauceM/Nagi/internal/intent/domain"
)

// TestBuiltinRuleCatalogMatchesEvaluation verifies the published definition and
// the live evaluator agree on rule order, actions and reason codes.
// TestBuiltinRuleCatalogMatchesEvaluation 验证对外目录与在线求值在顺序、动作和原因码上一致。
func TestBuiltinRuleCatalogMatchesEvaluation(t *testing.T) {
	definitions := domain.BuiltinRules()
	if len(definitions) != 5 {
		t.Fatalf("builtin rule count = %d, want 5", len(definitions))
	}
	wantIDs := []string{"closing", "addressed_to_other", "continuation", "interjection", "directed_to_bot"}
	lastOrder := 0
	for index, definition := range definitions {
		if definition.RuleID != wantIDs[index] {
			t.Fatalf("rule %d = %q, want %q", index, definition.RuleID, wantIDs[index])
		}
		if definition.Order <= lastOrder {
			t.Fatalf("rule %s order %d not ascending after %d", definition.RuleID, definition.Order, lastOrder)
		}
		lastOrder = definition.Order
		if definition.Source != "builtin" || !definition.Editable {
			t.Fatalf("rule %s source/editable = %q/%v", definition.RuleID, definition.Source, definition.Editable)
		}
	}

	cases := []struct {
		name       string
		semantic   domain.SemanticAnswer
		wantReason string
		wantAction domain.Decision
	}{
		{
			name:       "closing",
			semantic:   domain.SemanticAnswer{Available: true, IntentCategory: "closing"},
			wantReason: "closing",
			wantAction: domain.DecisionSilent,
		},
		{
			name:       "addressed_to_other",
			semantic:   domain.SemanticAnswer{Available: true, AddressedToOther: 0.9},
			wantReason: "addressed_to_other",
			wantAction: domain.DecisionSilent,
		},
		{
			name:       "continuation",
			semantic:   domain.SemanticAnswer{Available: true, ContinuesBotThread: 0.9},
			wantReason: "active_thread_continuation",
			wantAction: domain.DecisionReply,
		},
		{
			name:       "interjection",
			semantic:   domain.SemanticAnswer{Available: true, AppropriateToJoin: 0.97},
			wantReason: "interjection",
			wantAction: domain.DecisionReply,
		},
		{
			name:       "directed_to_bot",
			semantic:   domain.SemanticAnswer{Available: true, DirectedToBot: 0.9},
			wantReason: "directed_to_bot",
			wantAction: domain.DecisionReply,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			in := baseInput(scopedomain.ModeIntentBased)
			rule := EvaluateRules(in)
			if !rule.NeedSemantic {
				t.Fatalf("rules = %+v, want semantic path", rule)
			}
			got := EvaluateReplyPolicy(in, rule, tc.semantic, thresholds)
			if got.Decision != tc.wantAction || got.ReasonCode != tc.wantReason {
				t.Fatalf("decision = %s/%s, want %s/%s", got.Decision, got.ReasonCode, tc.wantAction, tc.wantReason)
			}
			rule.BuiltinRules = map[string]bool{tc.name: false}
			if disabled := EvaluateReplyPolicy(in, rule, tc.semantic, thresholds); disabled.ReasonCode == tc.wantReason {
				t.Fatalf("disabled rule %s still produced %q", tc.name, tc.wantReason)
			}
		})
	}
}

// TestBuiltinRuleCatalogPrivateApplicability verifies only closing applies to
// private conversations; group participation rules are explicitly inapplicable.
// TestBuiltinRuleCatalogPrivateApplicability 验证私聊只适用告别规则，群聊参与规则明确不适用。
func TestBuiltinRuleCatalogPrivateApplicability(t *testing.T) {
	for _, definition := range domain.BuiltinRules() {
		got := domain.BuiltinRuleAppliesToScope(definition, "private")
		want := definition.RuleID == "closing"
		if got != want {
			t.Fatalf("private applicability for %s = %v, want %v", definition.RuleID, got, want)
		}
		if !domain.BuiltinRuleAppliesToScope(definition, "group") {
			t.Fatalf("group applicability for %s = false", definition.RuleID)
		}
	}
}

// TestBuiltinRulesSkipWhenRoleAbsent verifies a rule whose named role was not
// asked (RolePresent false) is skipped, so removing a question disables the
// corresponding built-in rule instead of matching a zero-valued placeholder.
// TestBuiltinRulesSkipWhenRoleAbsent 验证具名角色未被提问（RolePresent=false）时跳过该规则；
// 删除问题即关闭对应内置规则，而不是匹配零值占位。
func TestBuiltinRulesSkipWhenRoleAbsent(t *testing.T) {
	in := baseInput(scopedomain.ModeIntentBased)
	rule := EvaluateRules(in)
	if !rule.NeedSemantic {
		t.Fatalf("rules = %+v, want semantic path", rule)
	}
	absent := domain.SemanticAnswer{
		Available: true, DirectedToBot: 0.99,
		RolePresent: map[string]bool{"directed_to_bot": false},
	}
	if got := EvaluateReplyPolicy(in, rule, absent, thresholds); got.Decision != domain.DecisionSilent || got.ReasonCode != "ambiguous" {
		t.Fatalf("absent role decision = %+v, want silent/ambiguous", got)
	}
	present := domain.SemanticAnswer{
		Available: true, DirectedToBot: 0.99,
		RolePresent: map[string]bool{"directed_to_bot": true},
	}
	if got := EvaluateReplyPolicy(in, rule, present, thresholds); got.ReasonCode != "directed_to_bot" {
		t.Fatalf("present role decision = %+v, want directed_to_bot", got)
	}
}

// TestFixedConstraintCatalogMatchesEvaluation verifies the displayed hard
// constraints use the same order and reason codes as the live gate.
// TestFixedConstraintCatalogMatchesEvaluation 验证展示的服务端硬约束与在线闸门使用相同顺序和原因码。
func TestFixedConstraintCatalogMatchesEvaluation(t *testing.T) {
	definitions := domain.FixedConstraints()
	if len(definitions) < 4 {
		t.Fatalf("fixed constraint count = %d", len(definitions))
	}
	lastOrder := 0
	ids := make(map[string]bool, len(definitions))
	for _, definition := range definitions {
		if definition.Order <= lastOrder || definition.Source != "server" || definition.Editable {
			t.Fatalf("constraint %+v has invalid order/source", definition)
		}
		lastOrder = definition.Order
		ids[definition.ConstraintID] = true
	}
	for _, id := range []string{"policy_enabled", "observe_only", "cooldown", "hourly_limit", "tool_permissions", "sending_budget"} {
		if !ids[id] {
			t.Fatalf("missing fixed constraint %q", id)
		}
	}

	cases := []struct {
		name   string
		mutate func(*domain.Input)
		want   string
	}{
		{"policy_disabled", func(in *domain.Input) { in.Policy.Enabled = false }, "policy_disabled"},
		{"observe_only", func(in *domain.Input) { in.Policy.ReplyMode = scopedomain.ModeObserveOnly }, "observe_only"},
		{"cooldown", func(in *domain.Input) { in.Recent[1].CreatedAt = in.Now.Add(-2 * time.Second) }, "cooldown"},
		{"hourly_limit", func(in *domain.Input) {
			in.Policy.MaxRepliesPerHour = 1
			in.Recent = append(in.Recent, domain.Message{MessageID: 4991, SenderID: 100, IsBot: true, Text: "更早回复", CreatedAt: in.Now.Add(-20 * time.Second)})
		}, "rate_limited"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			in := baseInput(scopedomain.ModeIntentBased)
			tc.mutate(&in)
			if got := EvaluateRules(in); !got.Blocked || got.ReasonCode != tc.want {
				t.Fatalf("rules = %+v, want blocked/%s", got, tc.want)
			}
		})
	}
}
