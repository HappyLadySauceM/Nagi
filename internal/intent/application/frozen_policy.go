package application

import (
	"math"

	"github.com/HappyLadySauceM/Nagi/internal/intent/domain"
)

// EvaluateFrozenPolicy runs the intent decision with supplied context and scores.
// A nil ruleset selects this build's built-in rules. Missing required evidence is
// returned instead of a decision. An explicit unavailable answer preserves a
// recorded model failure.
// EvaluateFrozenPolicy 使用传入的上下文和分数执行意图决策。nil 规则集选择本构建的
// 内置规则。必要证据缺失时返回缺项。显式不可用的回答保留已记录的模型失败。
func EvaluateFrozenPolicy(in domain.Input, set *domain.Ruleset, answer *domain.SemanticAnswer) (domain.FinalDecision, []string) {
	if !allowsTopicContinuation(in) {
		in.Topics = nil
	}
	adjusted, _ := topicRuleInput(in)
	rule, version := EvaluateRuleChain(adjusted, set, nil)
	rule.NeedSemantic = rule.NeedSemantic || needsTopicSemantic(adjusted, rule)
	var semantic domain.SemanticAnswer
	if rule.NeedSemantic && !rule.Blocked {
		if answer == nil || !answer.Available && answer.FailureCode == "" {
			return domain.FinalDecision{}, []string{"semantic_answer"}
		}
		if answer.Available {
			var missing []string
			questions := decisionQuestions(rule)
			for _, key := range domain.SortedDecisionQuestions(questions) {
				q := questions[key]
				if !domain.QuestionApplies(q, in.Task.Kind) {
					continue
				}
				if score, ok := answer.Scores[key]; !ok || !validFrozenScore(score) {
					missing = append(missing, "semantic_score:"+key)
				}
			}
			if in.Topics != nil {
				if len(answer.TopicJudgments) != len(in.Topics.Candidates) {
					missing = append(missing, "semantic_topic_judgments")
				} else {
					for _, judgment := range answer.TopicJudgments {
						if !validFrozenScore(judgment.Affinity) {
							missing = append(missing, "semantic_topic_judgments")
							break
						}
					}
				}
			}
			if len(missing) != 0 {
				return domain.FinalDecision{}, missing
			}
			semantic = *answer
		}
	}
	decision := evaluateTopicDecision(in, rule, semantic)
	decision.RulesetVersion = version
	return decision, nil
}

func validFrozenScore(value float64) bool {
	return !math.IsNaN(value) && value >= 0 && value <= 1
}
