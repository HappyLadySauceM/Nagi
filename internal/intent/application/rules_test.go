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

// TestGroupAlwaysReplyFallsBackToJevUnavailable pins the current behavior of
// group always_reply: without explicit addressing the rules neither pass an
// explicit mention nor request semantics, so the policy treats the absent
// semantic answer as jev_unavailable and stays silent. P4 makes always_reply a
// real unconditional reply; update this test together with that change.
// TestGroupAlwaysReplyFallsBackToJevUnavailable 固定群聊 always_reply 的当前行为：
// 未被明确指向时，规则既不判定为明确指向也不请求语义判定，因此策略把缺失的语义
// 回答视为 jev_unavailable 并保持沉默。P4 会把 always_reply 改为真正的无条件回复，
// 届时同步修改本测试。
func TestGroupAlwaysReplyFallsBackToJevUnavailable(t *testing.T) {
	in := baseInput(scopedomain.ModeAlwaysReply)
	rule := EvaluateRules(in)
	if rule.Blocked || rule.Explicit || rule.NeedSemantic {
		t.Fatalf("rules = %+v, want unblocked without explicit or semantic", rule)
	}
	// The pipeline only calls Jev when NeedSemantic is set, so the policy always
	// sees an unavailable answer in this mode.
	// 流水线仅在 NeedSemantic 置位时调用 Jev，因此该模式下策略只会看到不可用回答。
	if d := EvaluateReplyPolicy(in, rule, domain.SemanticAnswer{}, thresholds); d.Decision != domain.DecisionSilent || d.ReasonCode != "jev_unavailable" {
		t.Fatalf("decision = %+v, want silent/jev_unavailable", d)
	}

	// Explicit addressing still replies in this mode.
	// 明确指向在本模式下仍然回复。
	in.Task.Segments = []obdomain.Segment{domainSegmentAt("100")}
	rule = EvaluateRules(in)
	if !rule.Explicit {
		t.Fatalf("explicit mention should set Explicit: %+v", rule)
	}
	if d := EvaluateReplyPolicy(in, rule, domain.SemanticAnswer{}, thresholds); d.Decision != domain.DecisionReply || d.ReasonCode != "explicit_mention" {
		t.Fatalf("decision = %+v, want reply/explicit_mention", d)
	}
}

// TestPrivateReplyModesShareTheSameDecision pins that private conversations
// return early in both the rules gate and the reply policy, so intent_based,
// always_reply and mention_only produce identical decisions whether or not the
// bot is explicitly addressed. observe_only is excluded because it is blocked
// before the private branch. P4 reintroduces private semantic judgment; update
// this test together with that change.
// TestPrivateReplyModesShareTheSameDecision 固定私聊在规则闸门与回复策略中均提前返回：
// intent_based、always_reply 与 mention_only 无论是否明确指向机器人都产出相同决策。
// observe_only 在进入私聊分支前已被拦截，因此不在比较范围内。P4 会重新引入私聊语义
// 判定，届时同步修改本测试。
func TestPrivateReplyModesShareTheSameDecision(t *testing.T) {
	modes := []scopedomain.ReplyMode{
		scopedomain.ModeIntentBased,
		scopedomain.ModeAlwaysReply,
		scopedomain.ModeMentionOnly,
	}
	for _, addressed := range []bool{false, true} {
		name := "unaddressed"
		if addressed {
			name = "addressed"
		}
		t.Run(name, func(t *testing.T) {
			var want domain.FinalDecision
			for i, mode := range modes {
				in := baseInput(mode)
				in.Task.Kind = "private"
				in.Task.TargetID = 200
				in.Policy.ScopeType = scopedomain.ScopePrivate
				in.Policy.ScopeID = 200
				if addressed {
					in.Task.Segments = []obdomain.Segment{domainSegmentAt("100")}
				}
				rule := EvaluateRules(in)
				if rule.Blocked || rule.NeedSemantic {
					t.Fatalf("mode %s: rules = %+v, want unblocked without semantic", mode, rule)
				}
				got := EvaluateReplyPolicy(in, rule, domain.SemanticAnswer{}, thresholds)
				if i == 0 {
					want = got
					wantReason := "private_direct"
					if addressed {
						wantReason = "explicit_mention"
					}
					if want.Decision != domain.DecisionReply || want.ReasonCode != wantReason {
						t.Fatalf("mode %s: decision = %+v, want reply/%s", mode, want, wantReason)
					}
					continue
				}
				if got.Decision != want.Decision || got.ReasonCode != want.ReasonCode {
					t.Fatalf("mode %s: decision = %+v, want same as %s: reply/%s", mode, got, modes[0], want.ReasonCode)
				}
			}
		})
	}
}

// TestReplyPolicyPrivateDirect verifies that private chats reply without semantics.
// TestReplyPolicyPrivateDirect 验证私聊无需语义判定直接回复。
func TestReplyPolicyPrivateDirect(t *testing.T) {
	in := baseInput(scopedomain.ModeIntentBased)
	in.Task.Kind = "private"
	in.Task.TargetID = 200
	in.Policy.ScopeType = scopedomain.ScopePrivate
	rule := EvaluateRules(in)
	if rule.Blocked || rule.NeedSemantic {
		t.Fatalf("private rules: %+v", rule)
	}
	if d := EvaluateReplyPolicy(in, rule, domain.SemanticAnswer{}, thresholds); d.Decision != domain.DecisionReply || d.ReasonCode != "private_direct" {
		t.Fatalf("private decision: %+v", d)
	}
}
