// Deterministic rules gate: it has the final veto and never calls external services.
// 确定性规则闸门：拥有最终否决权，绝不调用外部服务。
package application

import (
	"fmt"
	"strings"

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
	if rule.ForceReply {
		// A published rule overrides the built-in reason but still runs after the
		// hard gates, so frequency limits keep their veto.
		// 已发布规则覆盖内置原因码，但仍排在硬闸门之后，频率限制保留否决权。
		return reply(rule.ForceReason)
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
	enabled := func(key string) bool { value, exists := rule.BuiltinRules[key]; return !exists || value }
	if enabled("closing") && semantic.IntentCategory == "closing" {
		return withScores(domain.DecisionSilent, "closing", scores)
	}
	if enabled("continuation") && semantic.ContinuesBotThread >= th.Continuation && (!enabled("addressed_to_other") || semantic.AddressedToOther < th.AddressedOtherMax) {
		return withScores(domain.DecisionReply, "active_thread_continuation", scores)
	}
	if enabled("interjection") && in.Policy.ReplyMode == scopedomain.ModeIntentBased &&
		semantic.AppropriateToJoin >= th.Interjection {
		return withScores(domain.DecisionReply, "interjection", scores)
	}
	if enabled("directed_to_bot") && semantic.DirectedToBot >= th.Continuation && (!enabled("addressed_to_other") || semantic.AddressedToOther < th.AddressedOtherMax) {
		return withScores(domain.DecisionReply, "directed_to_bot", scores)
	}
	if enabled("addressed_to_other") && semantic.AddressedToOther >= th.AddressedOtherMax {
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

// ApplyRuleset overlays a published ruleset on the deterministic gate. Hard
// rules keep their veto: blocked inputs are returned untouched, and entries are
// evaluated in priority order with the first match winning.
// ApplyRuleset 把已发布规则叠加到确定性闸门上。硬规则保留否决权：
// 已拦截的输入原样返回；条目按优先级求值，首个命中生效。
func ApplyRuleset(rule domain.RuleResult, in domain.Input, entries []domain.RuleEntry) domain.RuleResult {
	if rule.Blocked {
		return rule
	}
	for _, entry := range entries {
		evaluation := domain.RuleEvaluation{Priority: entry.Priority, ReasonCode: entry.Action.ReasonCode, Status: "unmatched"}
		if entry.Enabled != nil && !*entry.Enabled {
			evaluation.Status = "disabled"
			rule.Evaluations = append(rule.Evaluations, evaluation)
			continue
		}
		if !matchEntry(entry.Match, in) {
			rule.Evaluations = append(rule.Evaluations, evaluation)
			continue
		}
		evaluation.Status = "matched"
		rule.Evaluations = append(rule.Evaluations, evaluation)
		rule.Matched = true
		switch entry.Action.Type {
		case domain.ActionReply:
			rule.NeedSemantic = false
			rule.ForceReply = true
			rule.ForceReason = entry.Action.ReasonCode
			return rule
		case domain.ActionSilent:
			rule.Blocked = true
			rule.ReasonCode = entry.Action.ReasonCode
			return rule
		case domain.ActionRequireSemantic:
			rule.NeedSemantic = true
			rule.Explicit = false
			rule.AlwaysReply = false
			rule.ForceReply = false
			return rule
		}
	}
	return rule
}

// matchEntry reports whether every non-zero condition holds.
// matchEntry 报告所有非零条件是否同时成立。
func matchEntry(m domain.RuleMatch, in domain.Input) bool {
	if m.ReplyMode != "" && string(in.Policy.ReplyMode) != m.ReplyMode {
		return false
	}
	if m.Kind != "" && in.Task.Kind != m.Kind {
		return false
	}
	if m.Explicit != nil {
		explicit := in.MentionsBot() || in.RepliesToBot()
		if explicit != *m.Explicit {
			return false
		}
	}
	if m.TextContains != "" && !strings.Contains(in.Task.Message.Text, m.TextContains) {
		return false
	}
	if m.MinSecondsSinceReply > 0 {
		since := in.BotLastSpoke()
		if since >= 0 && since < int64(m.MinSecondsSinceReply) {
			return false
		}
	}
	return true
}

// EvaluateRuleChain evaluates specific rules before global rules and shares builtin settings.
// EvaluateRuleChain 先执行指定会话规则，再执行全局规则，并合并内置判断设置。
func EvaluateRuleChain(in domain.Input, specific, global *domain.Ruleset) (domain.RuleResult, string) {
	rule := EvaluateRules(in)
	rule.BuiltinRules = make(map[string]bool)
	version := ""
	for _, set := range []*domain.Ruleset{global, specific} {
		if set != nil {
			for key, value := range set.BuiltinRules {
				rule.BuiltinRules[key] = value
			}
		}
	}
	if specific != nil {
		before := len(rule.Evaluations)
		rule = ApplyRuleset(rule, in, specific.Entries)
		for i := before; i < len(rule.Evaluations); i++ {
			rule.Evaluations[i].RulesetID = specific.ID
			rule.Evaluations[i].Version = specific.Version
			rule.Evaluations[i].Scope = "specific"
		}
		version = fmt.Sprintf("scope:%d@%d", specific.ID, specific.Version)
	}
	if global != nil {
		version += fmt.Sprintf(";global:%d@%d", global.ID, global.Version)
		if !rule.Matched {
			before := len(rule.Evaluations)
			rule = ApplyRuleset(rule, in, global.Entries)
			for i := before; i < len(rule.Evaluations); i++ {
				rule.Evaluations[i].RulesetID = global.ID
				rule.Evaluations[i].Version = global.Version
				rule.Evaluations[i].Scope = "global"
			}
		}
	}
	return rule, version
}
