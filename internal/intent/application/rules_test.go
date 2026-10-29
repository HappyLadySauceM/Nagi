// Tests for the rules gate and reply policy combination.
// 规则闸门与回复策略组合的测试。
package application

import (
	"testing"
	"time"

	obdomain "github.com/HappyLadySauceM/Nagi/internal/onebot/domain"
	scopedomain "github.com/HappyLadySauceM/Nagi/internal/scope/domain"

	"github.com/HappyLadySauceM/Nagi/internal/intent/domain"
)

// domainSegmentAt builds an at segment targeting the given id.
// domainSegmentAt 构造指向指定 ID 的 at 消息段。
func domainSegmentAt(qq string) obdomain.Segment {
	return obdomain.Segment{Type: obdomain.SegAt, Data: map[string]string{"qq": qq}}
}

func groupPolicy(mode scopedomain.ReplyMode) scopedomain.Policy {
	return scopedomain.Policy{
		ScopeType: scopedomain.ScopeGroup, ScopeID: 700,
		Enabled: true, ReplyMode: mode,
		MinReplyIntervalMS: 10000, MaxRepliesPerHour: 20,
	}
}

func baseInput(mode scopedomain.ReplyMode) domain.Input {
	now := time.Now()
	return domain.Input{
		Task: domain.Task{
			JobID: 1, BotID: 100, Kind: "group", TargetID: 700,
			Message: domain.Message{MessageID: 5001, SenderID: 200, Text: "那你觉得呢？"},
		},
		Recent: []domain.Message{
			{MessageID: 5000, SenderID: 200, Text: "小雪，Go 合适吗？", CreatedAt: now.Add(-40 * time.Second)},
			{MessageID: 4990, SenderID: 100, IsBot: true, Text: "适合，主要看你要做什么。", CreatedAt: now.Add(-35 * time.Second)},
		},
		Policy: groupPolicy(mode),
		Now:    now,
	}
}

var thresholds = domain.Thresholds{
	Continuation: 0.80, Interjection: 0.95, AddressedOtherMax: 0.60,
}

// TestRulesCooldownVetoesEvenExplicitMention verifies the final veto of rate rules.
// TestRulesCooldownVetoesEvenExplicitMention 验证频率规则的最终否决权。
func TestRulesCooldownVetoesEvenExplicitMention(t *testing.T) {
	in := baseInput(scopedomain.ModeMentionOnly)
	in.Task.Segments = []obdomain.Segment{domainSegmentAt("100")}
	// Bot spoke 35s ago; min interval is 10s so cooldown does not trigger here.
	// 机器人 35 秒前发言，最小间隔 10 秒，此处不应触发冷却。
	rule := EvaluateRules(in)
	if rule.Blocked {
		t.Fatalf("unexpected block: %s", rule.ReasonCode)
	}
	// Now make the bot reply very recently to trigger cooldown.
	// 现在让机器人刚刚发言以触发冷却。
	in.Recent[1].CreatedAt = in.Now.Add(-2 * time.Second)
	rule = EvaluateRules(in)
	if !rule.Blocked || rule.ReasonCode != "cooldown" {
		t.Fatalf("expected cooldown block, got %+v", rule)
	}
}

// TestRulesMentionOnlyRequiresExplicitAddressing verifies that plain text without
// an at segment does not pass the mention_only gate.
// TestRulesMentionOnlyRequiresExplicitAddressing 验证没有 at 段的纯文本
// 不能通过 mention_only 闸门。
func TestRulesMentionOnlyRequiresExplicitAddressing(t *testing.T) {
	in := baseInput(scopedomain.ModeMentionOnly)
	if rule := EvaluateRules(in); !rule.Blocked || rule.ReasonCode != "not_addressed" {
		t.Fatalf("expected not_addressed, got %+v", rule)
	}
	in.Task.Segments = []obdomain.Segment{domainSegmentAt("100")}
	if rule := EvaluateRules(in); rule.Blocked || !rule.Explicit {
		t.Fatalf("explicit mention should pass, got %+v", rule)
	}
}

// TestRulesIntentBasedNeedsSemantic verifies the semantic path for unaddressed
// messages in intent_based mode.
// TestRulesIntentBasedNeedsSemantic 验证 intent_based 模式下未指向消息进入语义判定。
func TestRulesIntentBasedNeedsSemantic(t *testing.T) {
	in := baseInput(scopedomain.ModeIntentBased)
	rule := EvaluateRules(in)
	if rule.Blocked || !rule.NeedSemantic {
		t.Fatalf("expected need-semantic, got %+v", rule)
	}
	in.Task.Segments = []obdomain.Segment{domainSegmentAt("100")}
	if rule := EvaluateRules(in); rule.NeedSemantic || !rule.Explicit {
		t.Fatalf("explicit mention should bypass semantic, got %+v", rule)
	}
}

// TestReplyPolicyContinuationAndInterjection verifies threshold-driven decisions.
// TestReplyPolicyContinuationAndInterjection 验证阈值驱动的决策。
func TestReplyPolicyContinuationAndInterjection(t *testing.T) {
	in := baseInput(scopedomain.ModeIntentBased)
	rule := domain.RuleResult{NeedSemantic: true}

	cont := domain.SemanticAnswer{Available: true, ContinuesBotThread: 0.9, AddressedToOther: 0.2, IntentCategory: "chat"}
	if d := EvaluateReplyPolicy(in, rule, cont, thresholds); d.Decision != domain.DecisionReply || d.ReasonCode != "active_thread_continuation" {
		t.Fatalf("continuation: %+v", d)
	}

	join := domain.SemanticAnswer{Available: true, ContinuesBotThread: 0.1, AppropriateToJoin: 0.97, AddressedToOther: 0.1, IntentCategory: "chat"}
	if d := EvaluateReplyPolicy(in, rule, join, thresholds); d.Decision != domain.DecisionReply || d.ReasonCode != "interjection" {
		t.Fatalf("interjection: %+v", d)
	}

	other := domain.SemanticAnswer{Available: true, ContinuesBotThread: 0.1, AddressedToOther: 0.9, IntentCategory: "chat"}
	if d := EvaluateReplyPolicy(in, rule, other, thresholds); d.Decision != domain.DecisionSilent || d.ReasonCode != "addressed_to_other" {
		t.Fatalf("addressed to other: %+v", d)
	}

	closing := domain.SemanticAnswer{Available: true, ContinuesBotThread: 0.9, AddressedToOther: 0.1, IntentCategory: "closing"}
	if d := EvaluateReplyPolicy(in, rule, closing, thresholds); d.Decision != domain.DecisionSilent || d.ReasonCode != "closing" {
		t.Fatalf("closing: %+v", d)
	}

	unavailable := domain.SemanticAnswer{}
	if d := EvaluateReplyPolicy(in, rule, unavailable, thresholds); d.Decision != domain.DecisionSilent || d.ReasonCode != "jev_unavailable" {
		t.Fatalf("jev unavailable: %+v", d)
	}

	ambiguous := domain.SemanticAnswer{Available: true, ContinuesBotThread: 0.5, AddressedToOther: 0.5, IntentCategory: "chat"}
	if d := EvaluateReplyPolicy(in, rule, ambiguous, thresholds); d.Decision != domain.DecisionSilent || d.ReasonCode != "ambiguous" {
		t.Fatalf("ambiguous: %+v", d)
	}
}

// TestGroupAlwaysReplyRepliesWithoutSemantics verifies that the group
// always_reply mode answers unconditionally: it must not depend on the
// semantic decider, which used to silence the mode when Jev was unavailable.
// TestGroupAlwaysReplyRepliesWithoutSemantics 验证群聊 always_reply 无条件回复：
// 它不依赖语义判定，而语义不可用曾让该模式完全沉默。
func TestGroupAlwaysReplyRepliesWithoutSemantics(t *testing.T) {
	in := baseInput(scopedomain.ModeAlwaysReply)
	rule := EvaluateRules(in)
	if rule.Blocked || rule.Explicit || rule.NeedSemantic || !rule.AlwaysReply {
		t.Fatalf("rules = %+v, want unblocked always_reply without semantic", rule)
	}
	// The pipeline never calls Jev in this mode, so the policy must not need it.
	// 该模式不会调用 Jev，因此策略不得依赖语义回答。
	if d := EvaluateReplyPolicy(in, rule, domain.SemanticAnswer{}, thresholds); d.Decision != domain.DecisionReply || d.ReasonCode != "always_reply" {
		t.Fatalf("decision = %+v, want reply/always_reply", d)
	}

	// Explicit addressing still wins with its own reason code.
	// 明确指向仍以其自身原因码优先。
	in.Task.Segments = []obdomain.Segment{domainSegmentAt("100")}
	rule = EvaluateRules(in)
	if !rule.Explicit || !rule.AlwaysReply {
		t.Fatalf("explicit mention should keep AlwaysReply: %+v", rule)
	}
	if d := EvaluateReplyPolicy(in, rule, domain.SemanticAnswer{}, thresholds); d.Decision != domain.DecisionReply || d.ReasonCode != "explicit_mention" {
		t.Fatalf("decision = %+v, want reply/explicit_mention", d)
	}
}

// TestPrivateReplyModesDiverge verifies that private conversations honor each
// reply mode instead of replying identically regardless of the configured mode.
// TestPrivateReplyModesDiverge 验证私聊按各自的回复模式决策，
// 而不是无论配置何种模式都给出相同结果。
func TestPrivateReplyModesDiverge(t *testing.T) {
	privateInput := func(mode scopedomain.ReplyMode, addressed bool) domain.Input {
		in := baseInput(mode)
		in.Task.Kind = "private"
		in.Task.TargetID = 200
		in.Policy.ScopeType = scopedomain.ScopePrivate
		in.Policy.ScopeID = 200
		if addressed {
			in.Task.Segments = []obdomain.Segment{domainSegmentAt("100")}
		}
		return in
	}

	cases := []struct {
		name       string
		mode       scopedomain.ReplyMode
		addressed  bool
		semantic   domain.SemanticAnswer
		want       domain.Decision
		wantReason string
	}{
		{
			name: "mention_only_ignores_unaddressed", mode: scopedomain.ModeMentionOnly,
			want: domain.DecisionSilent, wantReason: "not_addressed",
		},
		{
			name: "mention_only_replies_when_addressed", mode: scopedomain.ModeMentionOnly, addressed: true,
			want: domain.DecisionReply, wantReason: "explicit_mention",
		},
		{
			name: "always_reply_never_needs_semantics", mode: scopedomain.ModeAlwaysReply,
			want: domain.DecisionReply, wantReason: "always_reply",
		},
		{
			name: "intent_based_falls_back_to_reply_without_jev", mode: scopedomain.ModeIntentBased,
			want: domain.DecisionReply, wantReason: "private_semantic_unavailable",
		},
		{
			name: "intent_based_uses_semantics", mode: scopedomain.ModeIntentBased,
			semantic: domain.SemanticAnswer{Available: true, AppropriateToJoin: 0.99, AddressedToOther: 0.1},
			want:     domain.DecisionReply, wantReason: "interjection",
		},
		{
			name: "intent_based_stays_silent_for_other_thread", mode: scopedomain.ModeIntentBased,
			semantic: domain.SemanticAnswer{Available: true, AddressedToOther: 0.9},
			want:     domain.DecisionSilent, wantReason: "addressed_to_other",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			in := privateInput(tc.mode, tc.addressed)
			rule := EvaluateRules(in)
			if rule.Blocked {
				if tc.wantReason != rule.ReasonCode {
					t.Fatalf("blocked reason = %s, want %s", rule.ReasonCode, tc.wantReason)
				}
				return
			}
			got := EvaluateReplyPolicy(in, rule, tc.semantic, thresholds)
			if got.Decision != tc.want || got.ReasonCode != tc.wantReason {
				t.Fatalf("decision = %+v, want %s/%s", got, tc.want, tc.wantReason)
			}
		})
	}
}
