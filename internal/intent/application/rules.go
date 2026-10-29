// Deterministic rules gate: it has the final veto and never calls external services.
// 确定性规则闸门：拥有最终否决权，绝不调用外部服务。
package application

import (
	scopedomain "github.com/HappyLadySauceM/Nagi/internal/scope/domain"

	"github.com/HappyLadySauceM/Nagi/internal/intent/domain"
)

// EvaluateRules applies hard rules in veto order: policy, rate limits, then
// addressing gates.
// EvaluateRules 按否决顺序应用硬规则：策略、频率限制，然后是指向性闸门。
func EvaluateRules(in domain.Input) domain.RuleResult {
	if !in.Policy.Enabled {
		return blocked("policy_disabled")
	}
	if in.Policy.ReplyMode == scopedomain.ModeObserveOnly {
		return blocked("observe_only")
	}
	// Rate limits have the final veto, even for explicit mentions.
	// 频率限制拥有最终否决权，对明确 @ 也不例外。
	if in.Policy.MinReplyIntervalMS > 0 {
		if since := in.BotLastSpoke(); since >= 0 &&
			since*1000 < int64(in.Policy.MinReplyIntervalMS) {
			return blocked("cooldown")
		}
	}
	if in.Policy.MaxRepliesPerHour > 0 && in.BotRepliesLastHour() >= in.Policy.MaxRepliesPerHour {
		return blocked("rate_limited")
	}

	explicit := in.MentionsBot() || in.RepliesToBot()
	if in.Task.Kind == "private" {
		// Private conversations honor the same reply modes as groups: silence is
		// only correct when the scope asked for it.
		// 私聊与群聊遵循同一套回复模式：只有作用域明确要求时才沉默。
		switch in.Policy.ReplyMode {
		case scopedomain.ModeMentionOnly:
			if !explicit {
				return blocked("not_addressed")
			}
			return domain.RuleResult{Explicit: true}
		case scopedomain.ModeAlwaysReply:
			return domain.RuleResult{AlwaysReply: true}
		default: // intent_based
			if explicit {
				return domain.RuleResult{Explicit: true}
			}
			return domain.RuleResult{NeedSemantic: true}
		}
	}
	switch in.Policy.ReplyMode {
	case scopedomain.ModeMentionOnly:
		if !explicit {
			return blocked("not_addressed")
		}
		return domain.RuleResult{Explicit: explicit}
	case scopedomain.ModeAlwaysReply:
		// always_reply must not depend on the semantic decider: an unavailable
		// Jev used to silence this mode entirely.
		// always_reply 不得依赖语义判定：语义不可用曾让该模式完全沉默。
		return domain.RuleResult{Explicit: explicit, AlwaysReply: true}
	default: // intent_based
		if explicit {
			return domain.RuleResult{Explicit: true}
		}
		return domain.RuleResult{NeedSemantic: true}
	}
}

func blocked(reason string) domain.RuleResult {
	return domain.RuleResult{Blocked: true, ReasonCode: reason}
}

// EvaluateReplyPolicy combines rule gate, semantic answer and thresholds into the
// final decision; it never invents reasons from raw probabilities alone.
// EvaluateReplyPolicy 将规则闸门、语义回答与阈值合成为最终决策；
// 不会把单一概率直接当作动作指令。
func EvaluateReplyPolicy(in domain.Input, rule domain.RuleResult, semantic domain.SemanticAnswer, th domain.Thresholds) domain.FinalDecision {
	if rule.Blocked {
		return silent(rule.ReasonCode)
	}
	// Explicit addressing passes the intent gate and is only rate-bound.
	// 明确指向通过意图闸门，仅受频率规则约束。
	if rule.Explicit {
		return reply("explicit_mention")
	}
	if rule.AlwaysReply {
		// Reply mode wins over an absent semantic answer.
		// 回复模式优先于缺失的语义回答。
		return reply("always_reply")
	}
	if !semantic.Available {
		// Semantic unavailability must not silence a one-to-one chat: the scope
		// already accepted intent-based replies there.
		// 语义不可用不应让一对一对话沉默：该作用域已允许按意图回复。
		if in.Task.Kind == "private" {
			return reply("private_semantic_unavailable")
		}
		// Jev unavailable: never auto-interject in groups; only explicit
		// messages pass, and they were already returned above.
		// Jev 不可用：群聊绝不自动插话；仅明确指向放行，且已在上方返回。
		return silent("jev_unavailable")
	}
	scores := map[string]float64{
		"directed_to_bot":      semantic.DirectedToBot,
		"continues_bot_thread": semantic.ContinuesBotThread,
		"addressed_to_other":   semantic.AddressedToOther,
		"appropriate_to_join":  semantic.AppropriateToJoin,
	}
	if semantic.IntentCategory == "closing" {
		return withScores(domain.DecisionSilent, "closing", scores)
	}
	if semantic.ContinuesBotThread >= th.Continuation && semantic.AddressedToOther < th.AddressedOtherMax {
		return withScores(domain.DecisionReply, "active_thread_continuation", scores)
	}
	if in.Policy.ReplyMode == scopedomain.ModeIntentBased &&
		semantic.AppropriateToJoin >= th.Interjection {
		return withScores(domain.DecisionReply, "interjection", scores)
	}
	if semantic.DirectedToBot >= th.Continuation && semantic.AddressedToOther < th.AddressedOtherMax {
		return withScores(domain.DecisionReply, "directed_to_bot", scores)
	}
	if semantic.AddressedToOther >= th.AddressedOtherMax {
		return withScores(domain.DecisionSilent, "addressed_to_other", scores)
	}
	return withScores(domain.DecisionSilent, "ambiguous", scores)
}

func silent(reason string) domain.FinalDecision {
	return domain.FinalDecision{Decision: domain.DecisionSilent, ReasonCode: reason}
}

func reply(reason string) domain.FinalDecision {
	return domain.FinalDecision{Decision: domain.DecisionReply, ReasonCode: reason}
}

func withScores(d domain.Decision, reason string, scores map[string]float64) domain.FinalDecision {
	return domain.FinalDecision{Decision: d, ReasonCode: reason, Scores: scores}
}
