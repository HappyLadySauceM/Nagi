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
	reason, _ := domain.EvaluateFixedConstraints(in)
	return evaluateRules(in, reason)
}

func evaluateRules(in domain.Input, fixedReason string) domain.RuleResult {
	if fixedReason != "" {
		return blocked(fixedReason)
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
		if awaitingReply(in) {
			return domain.RuleResult{ForceReply: true, ForceReason: "awaited_content_received"}
		}
		if !explicit {
			return blocked("not_addressed")
		}
		if requestsFollowingContent(in) {
			return domain.RuleResult{WaitForContent: true, ReasonCode: "awaiting_content"}
		}
		return domain.RuleResult{Explicit: explicit}
	case scopedomain.ModeAlwaysReply:
		// always_reply must not depend on the semantic decider: an unavailable
		// Jev used to silence this mode entirely.
		// always_reply 不得依赖语义判定：语义不可用曾让该模式完全沉默。
		return domain.RuleResult{Explicit: explicit, AlwaysReply: true}
	default: // intent_based
		if explicit {
			if in.MentionsBot() && requestsFollowingContent(in) && !hasTopicCandidates(in) {
				return domain.RuleResult{WaitForContent: true, ReasonCode: "awaiting_content"}
			}
			return domain.RuleResult{Explicit: true, NeedSemantic: !in.MentionsBot()}
		}
		return domain.RuleResult{NeedSemantic: true}
	}
}

func blocked(reason string) domain.RuleResult {
	return domain.RuleResult{Blocked: true, ReasonCode: reason}
}

// EvaluateReplyPolicy combines the rule gate and decision questions into the
// final decision. Each question carries its own threshold, action and scope.
// EvaluateReplyPolicy 将规则闸门与决策问题合成为最终决策。
// 每条问题自带阈值、动作和适用范围。
func EvaluateReplyPolicy(in domain.Input, rule domain.RuleResult, semantic domain.SemanticAnswer) domain.FinalDecision {
	if rule.Blocked {
		return silent(rule.ReasonCode)
	}
	if rule.WaitForContent {
		return domain.FinalDecision{Decision: domain.DecisionDefer, ReasonCode: rule.ReasonCode}
	}
	if in.CallAddressingRequired() {
		if in.GroupContext == nil && addressesOther(in) && !in.MentionsBot() {
			return withScores(domain.DecisionSilent, "addressed_to_other", decisionScores(semantic))
		}
		if !in.MentionsBot() {
			switch semantic.CallAddressing {
			case domain.CallNo:
				return withScores(domain.DecisionSilent, "addressed_to_other", decisionScores(semantic))
			case domain.CallUncertain, domain.CallUnknown:
				if awaitingReply(in) && (semantic.CallAddressing == domain.CallUncertain || !hasSupplementText(in.Task)) {
					return withScores(domain.DecisionReply, "awaited_content_received", decisionScores(semantic))
				}
				if semantic.FailureCode != "" && !rule.Explicit {
					return silent("jev_unavailable")
				}
				if !rule.Explicit || semantic.CallAddressing != domain.CallUnknown || semantic.CallFailureCode != "name_unavailable" {
					return withScores(domain.DecisionSilent, "call_addressing_uncertain", decisionScores(semantic))
				}
			case domain.CallYes:
				if requestsFollowingContent(in) {
					return withScores(domain.DecisionDefer, "awaiting_content", decisionScores(semantic))
				}
				if awaitingReply(in) {
					return withScores(domain.DecisionReply, "awaited_content_received", decisionScores(semantic))
				}
				return withScores(domain.DecisionReply, "semantic_direct_call", decisionScores(semantic))
			}
		}
		if in.MentionsBot() && requestsFollowingContent(in) {
			return withScores(domain.DecisionDefer, "awaiting_content", decisionScores(semantic))
		}
		if awaitingReply(in) && in.MentionsBot() {
			return withScores(domain.DecisionReply, "awaited_content_received", decisionScores(semantic))
		}
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
	if rule.ForceReply {
		return reply(rule.ForceReason)
	}
	if rule.NeedSemantic && !semantic.Available {
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
	scores := decisionScores(semantic)
	questions := rule.Questions
	if questions == nil {
		questions = domain.DefaultDecisionQuestions()
	}
	if decision, matched := matchDecisionQuestions(in, semantic, questions, scores); matched {
		return decision
	}
	// A one-to-one conversation is addressed to the bot by construction. Treat
	// normal private messages as expected replies; closing remains a question.
	// 一对一对话按语义即指向机器人。私聊正常消息按期待回复处理；告别仍由决策问题表达。
	if in.Task.Kind == "private" {
		return withScores(domain.DecisionReply, "private_normal", scores)
	}
	if !rule.NeedSemantic {
		return silent("no_decision_question")
	}
	return withScores(domain.DecisionSilent, "ambiguous", scores)
}

func decisionScores(semantic domain.SemanticAnswer) map[string]float64 {
	scores := map[string]float64{
		"directed_to_bot":      semantic.DirectedToBot,
		"continues_bot_thread": semantic.ContinuesBotThread,
		"addressed_to_other":   semantic.AddressedToOther,
		"appropriate_to_join":  semantic.AppropriateToJoin,
	}
	for key, value := range semantic.Scores {
		scores[key] = value
	}
	return scores
}

// matchDecisionQuestions applies the ordered question list and returns the first hit.
// matchDecisionQuestions 按顺序检查决策问题，返回首个命中。
func matchDecisionQuestions(in domain.Input, semantic domain.SemanticAnswer, questions map[string]domain.PromptQuestion, scores map[string]float64) (domain.FinalDecision, bool) {
	for _, key := range domain.SortedDecisionQuestions(questions) {
		question := questions[key]
		if !domain.QuestionApplies(question, in.Task.Kind) || !questionMatched(semantic, key, question) {
			continue
		}
		decision := domain.DecisionSilent
		if question.Action == domain.ActionReply {
			decision = domain.DecisionReply
		}
		return withScores(decision, domain.DecisionReason(key, question), scores), true
	}
	return domain.FinalDecision{}, false
}

func questionMatched(semantic domain.SemanticAnswer, key string, question domain.PromptQuestion) bool {
	score, ok := domain.QuestionScore(semantic, key, question)
	return ok && score >= question.Threshold
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
		enabled := entry.Enabled == nil || *entry.Enabled
		evaluation := domain.RuleEvaluation{Priority: entry.Priority, Enabled: enabled, Match: entry.Match, Action: entry.Action, ReasonCode: entry.Action.ReasonCode, Status: "unmatched"}
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

// EvaluateRuleChain selects the decision questions for one message. A specific
// ruleset replaces the global one entirely, including an empty question list.
// Neither set means the caller keeps the five defaults. Condition entries are
// not evaluated.
// EvaluateRuleChain 为一条消息选择决策问题。指定会话规则完整替换全局规则，
// 包括空问题列表。两者都没有时调用方继续使用五条默认问题。条件条目不再求值。
func EvaluateRuleChain(in domain.Input, specific, global *domain.Ruleset) (domain.RuleResult, string) {
	return applyDecisionQuestions(in, EvaluateRules(in), specific, global)
}

func applyDecisionQuestions(in domain.Input, rule domain.RuleResult, specific, global *domain.Ruleset) (domain.RuleResult, string) {
	var questions map[string]domain.PromptQuestion
	version := ""
	fromRuleset := false
	switch {
	case specific != nil:
		questions = domain.WithDecisionDefaults(specific.Questions)
		fromRuleset = true
		version = fmt.Sprintf("scope:%d@%d", specific.ID, specific.Version)
	case global != nil:
		questions = domain.WithDecisionDefaults(global.Questions)
		fromRuleset = true
		version = fmt.Sprintf("global:%d@%d", global.ID, global.Version)
	}
	if fromRuleset {
		if questions == nil {
			questions = map[string]domain.PromptQuestion{}
		}
		rule.Questions = questions
	}
	effective := questions
	if !fromRuleset {
		effective = domain.DefaultDecisionQuestions()
	}
	if rule.NeedSemantic && !in.CallAddressingRequired() && !domain.HasApplicableQuestion(effective, in.Task.Kind) {
		rule.NeedSemantic = false
	}
	return rule, version
}
