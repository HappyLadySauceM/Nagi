package application

import (
	"context"
	"slices"
	"strconv"
	"time"

	"github.com/HappyLadySauceM/Nagi/internal/intent/domain"
	ob "github.com/HappyLadySauceM/Nagi/internal/onebot/domain"
	scope "github.com/HappyLadySauceM/Nagi/internal/scope/domain"
)

type topicReader interface {
	Topics(context.Context, domain.Task, int64, int) (*domain.TopicContext, error)
}

func allowsTopicContinuation(in domain.Input) bool {
	return in.Task.Kind == "group" && in.Policy.Enabled && in.Policy.AllowHistoryRead && in.Policy.ReplyMode == scope.ModeIntentBased
}

func activeAttention(in domain.Input) bool {
	return in.Topics != nil && slices.ContainsFunc(in.Topics.Candidates, func(candidate domain.TopicCandidate) bool {
		return candidate.Active && !candidate.Waiting
	})
}

func explainAttention(steps []domain.Step, choice *domain.AttentionChoice) {
	steps[3].Detail["SkipReason"] = "active_topic"
	steps[3].Summary = "活跃话题由 Agent 判断"
	steps[4].Detail["Attention"] = choice
}

func attentionDecision(in domain.Input, choice domain.AttentionChoice) domain.FinalDecision {
	result := domain.FinalDecision{
		Decision: domain.DecisionSilent, ReasonCode: "attention_wait",
		Provider: "agent_attention", Model: choice.Model, Attention: &choice,
	}
	if in.Topics == nil || !allowsTopicContinuation(in) {
		result.ReasonCode = "attention_unavailable"
		return result
	}
	if choice.Action == "unavailable" || choice.Action == "invalid" {
		result.ReasonCode = "attention_" + choice.Action
		return result
	}
	if addressesOther(in) && !in.MentionsBot() {
		result.ReasonCode = "addressed_to_other"
		return result
	}
	var selected *domain.TopicCandidate
	for i := range in.Topics.Candidates {
		candidate := &in.Topics.Candidates[i]
		if candidate.ID == choice.TopicID && (candidate.Active && !candidate.Waiting || in.Topics.QuotedID == candidate.ID) {
			selected = candidate
			break
		}
	}
	change := &domain.TopicChange{
		ID: in.Task.JobID, Source: "agent", ParticipantID: in.Task.Message.SenderID,
		ExpiresAt: topicExpiry(in),
	}
	input := in
	switch choice.Action {
	case "wait":
		return result
	case "open":
		if choice.TopicID != 0 || in.Topics.QuotedID != 0 {
			result.ReasonCode = "attention_invalid"
			return result
		}
		change.Action = "opened"
	case domain.TopicFollowUp, domain.TopicClose, domain.TopicLeave:
		if selected == nil || in.Topics.QuotedID != 0 && in.Topics.QuotedID != selected.ID {
			result.ReasonCode = "attention_invalid"
			return result
		}
		change.ID = selected.ID
		change.Action = choice.Action
		if choice.Action != domain.TopicFollowUp {
			change.ExpiresAt = selected.ExpiresAt
			result.Topic = change
			result.ReasonCode = "topic_" + choice.Action
			return result
		}
		if !selected.Active {
			change.Action = "reopened"
		}
		input, change.CooldownBypassed = topicRuleInput(in)
	default:
		result.ReasonCode = "attention_invalid"
		return result
	}
	if reason, blocked := domain.EvaluateFixedConstraints(input); blocked {
		result.ReasonCode = reason
		return result
	}
	result.Decision = domain.DecisionReply
	result.ReasonCode = "attention_reply"
	result.Topic = change
	return result
}

func (d *Decider) loadTopics(ctx context.Context, in domain.Input, runtime RuntimeConfig) (*domain.TopicContext, error) {
	reader, ok := d.recorder.(topicReader)
	if !ok || !runtime.AIEnabled || runtime.TopicIdleSeconds <= 0 || !allowsTopicContinuation(in) {
		return nil, nil
	}
	var reference int64
	for _, s := range in.Task.Segments {
		if s.Type == ob.SegReply {
			reference, _ = strconv.ParseInt(s.Data["id"], 10, 64)
			break
		}
	}
	if reference == 0 && awaitingReply(in) {
		reference = in.AwaitedRequest.MessageID
	}
	return reader.Topics(ctx, in.Task, reference, runtime.TopicIdleSeconds)
}

func topicRuleInput(in domain.Input) (domain.Input, bool) {
	if in.Topics != nil && len(in.Topics.Candidates) > 0 {
		if reason, _ := domain.EvaluateFixedConstraints(in); reason == "cooldown" {
			in.Policy.MinReplyIntervalMS = 0
			return in, true
		}
	}
	return in, false
}

func needsTopicSemantic(in domain.Input, rule domain.RuleResult) bool {
	if in.Topics == nil || len(in.Topics.Candidates) == 0 || rule.Blocked || rule.WaitForContent || awaitingReply(in) {
		return false
	}
	_, text := supplementContents(in.Task)
	return text != "" || rule.NeedSemantic
}

func selectTopic(in domain.Input, semantic domain.SemanticAnswer) (*domain.TopicCandidate, string) {
	if in.Topics.QuotedID != 0 {
		for i := range in.Topics.Candidates {
			candidate := &in.Topics.Candidates[i]
			if candidate.ID == in.Topics.QuotedID && (!candidate.Waiting || awaitingReply(in)) {
				if awaitingReply(in) {
					return candidate, "supplement"
				}
				return candidate, "quote"
			}
		}
	}
	if !semantic.Available {
		return nil, ""
	}
	var best *domain.TopicCandidate
	var highest, runnerUp float64
	for i := range in.Topics.Candidates {
		candidate := &in.Topics.Candidates[i]
		if !candidate.Active || candidate.Waiting {
			continue
		}
		if i >= len(semantic.TopicJudgments) {
			continue
		}
		score := semantic.TopicJudgments[i].Affinity
		if score > highest {
			runnerUp, highest, best = highest, score, candidate
		} else if score > runnerUp {
			runnerUp = score
		}
	}
	if highest < .80 || highest-runnerUp < .15 {
		return nil, ""
	}
	return best, "semantic"
}

func evaluateTopicDecision(in domain.Input, rule domain.RuleResult, semantic domain.SemanticAnswer) domain.FinalDecision {
	decision := EvaluateReplyPolicy(in, rule, semantic)
	if in.Topics == nil || !in.Policy.AllowHistoryRead || rule.Blocked {
		return decision
	}
	_, cooldown := topicRuleInput(in)
	candidate, source := selectTopic(in, semantic)
	action := ""
	if candidate != nil && semantic.Available {
		for i := range in.Topics.Candidates {
			if candidate == &in.Topics.Candidates[i] && i < len(semantic.TopicJudgments) {
				action = semantic.TopicJudgments[i].Action
				break
			}
		}
	}
	directed := rule.Explicit || rule.ForceReply && rule.ForceReason != "unanswered_call_followup"
	if addressesOther(in) && !in.MentionsBot() && !rule.AlwaysReply {
		return withScores(domain.DecisionSilent, "addressed_to_other", decisionScores(semantic))
	}
	if candidate != nil && candidate.Active {
		switch action {
		case domain.TopicLeave, domain.TopicClose:
			decision = withScores(domain.DecisionSilent, "topic_"+action, decisionScores(semantic))
			decision.Topic = &domain.TopicChange{ID: candidate.ID, Action: action, Source: source,
				ExpiresAt: candidate.ExpiresAt, Waiting: candidate.Waiting}
			return decision
		case domain.TopicFollowUp, domain.TopicContribution:
			if decision.ReasonCode != "addressed_to_other" {
				if !directed {
					decision = withScores(domain.DecisionReply, "active_thread_continuation", decisionScores(semantic))
				}
				decision.Topic = &domain.TopicChange{ID: candidate.ID, Action: action, Source: source, CooldownBypassed: cooldown,
					ExpiresAt: topicExpiry(in)}
				return decision
			}
		}
		if directed && (in.Topics.QuotedBot || awaitingReply(in)) && decision.Decision == domain.DecisionReply {
			decision.Topic = &domain.TopicChange{ID: candidate.ID, Action: domain.TopicFollowUp, Source: source,
				CooldownBypassed: cooldown, ExpiresAt: topicExpiry(in)}
			return decision
		}
	}
	if cooldown {
		return silent("cooldown")
	}
	if !directed && decision.ReasonCode == "active_thread_continuation" {
		return withScores(domain.DecisionSilent, "topic_ambiguous", decisionScores(semantic))
	}
	if decision.Decision != domain.DecisionReply && decision.Decision != domain.DecisionDefer {
		return decision
	}
	id := in.Task.JobID
	change := "opened"
	if candidate != nil && !candidate.Active && source == "quote" && (directed || action == domain.TopicFollowUp) {
		id, change = candidate.ID, "reopened"
	}
	if decision.Decision == domain.DecisionDefer {
		change = "waiting"
	}
	if id != 0 {
		if source == "" {
			source = "decision"
		}
		decision.Topic = &domain.TopicChange{ID: id, Action: change, Source: source, ExpiresAt: topicExpiry(in),
			Waiting: decision.Decision == domain.DecisionDefer}
	}
	return decision
}

func topicExpiry(in domain.Input) time.Time {
	at := in.Task.Message.CreatedAt
	if at.IsZero() {
		at = in.Now
	}
	return at.Add(time.Duration(in.Topics.IdleSeconds) * time.Second)
}

func addressesOther(in domain.Input) bool {
	bot := strconv.FormatInt(in.Task.BotID, 10)
	for _, segment := range in.Task.Segments {
		if segment.Type == ob.SegAt && segment.Data["qq"] != bot {
			return true
		}
	}
	return false
}

func topicReplyHistory(in domain.Input, decision domain.FinalDecision) []domain.Message {
	if decision.Topic != nil && decision.Topic.Action != "opened" && decision.Topic.Action != "waiting" {
		for _, candidate := range in.Topics.Candidates {
			if candidate.ID == decision.Topic.ID {
				if awaitingReply(in) && len(candidate.History) == 0 {
					break
				}
				if in.Topics.Reference != nil && len(in.Topics.Reference.Segments) > 0 {
					history := slices.Clone(candidate.History)
					for i := range history {
						if history[i].MessageID == in.Topics.Reference.MessageID {
							history[i] = *in.Topics.Reference
							return history
						}
					}
				}
				return candidate.History
			}
		}
	}
	if in.Topics != nil && decision.Topic != nil && decision.Topic.Action == "opened" {
		if in.Topics.Reference != nil {
			return []domain.Message{*in.Topics.Reference}
		}
	}
	return replyHistory(in, decision.ReasonCode)
}
