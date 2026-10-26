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
		return domain.RuleResult{Explicit: explicit}
	}
	switch in.Policy.ReplyMode {
	case scopedomain.ModeMentionOnly:
		if !explicit {
			return blocked("not_addressed")
		}
		return domain.RuleResult{Explicit: explicit}
	case scopedomain.ModeAlwaysReply:
		return domain.RuleResult{Explicit: explicit}
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
	if in.Task.Kind == "private" {
		return reply("private_direct")
	}
	if !semantic.Available {
		// Jev unavailable: never auto-interject; only explicit messages pass,
		// and they were already returned above.
		// Jev 不可用：绝不自动插话；仅明确指向放行，且已在上方返回。
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
