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

func hasTopicCandidates(in domain.Input) bool {
	return in.Topics != nil && len(in.Topics.Candidates) != 0
}

func needsTopicSemantic(in domain.Input, rule domain.RuleResult) bool {
	if in.Topics == nil || len(in.Topics.Candidates) == 0 || rule.Blocked || rule.WaitForContent {
		return false
	}
	_, text := supplementContents(in.Task)
	return text != "" || rule.NeedSemantic
}

func selectTopic(in domain.Input, semantic domain.SemanticAnswer) (*domain.TopicCandidate, string) {
	if in.Topics.BoundID != 0 && len(in.Topics.Candidates) == 1 {
		return &in.Topics.Candidates[0], "routing"
	}
	if in.Topics.QuotedID != 0 {
		for i := range in.Topics.Candidates {
			candidate := &in.Topics.Candidates[i]
			if candidate.ID != in.Topics.QuotedID {
				continue
			}
			if !candidate.Waiting {
				return candidate, "quote"
			}
			if awaitingReply(in) {
				return candidate, "supplement"
			}
			if candidate.Active && semantic.Available && i < len(semantic.TopicJudgments) && quotesOwnWait(in) {
				switch semantic.TopicJudgments[i].Action {
				case domain.TopicLeave, domain.TopicClose:
					return candidate, "quote"
				}
			}
			return nil, ""
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
		if i >= len(semantic.TopicJudgments) || semantic.TopicJudgments[i].AffinityMissing {
			return nil, ""
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

func quotesOwnWait(in domain.Input) bool {
	previous := in.Topics.Reference
	if previous == nil || previous.IsBot || previous.SenderID != in.Task.Message.SenderID || previous.MessageID == 0 {
		return false
	}
	for _, segment := range in.Task.Segments {
		if segment.Type == ob.SegReply {
			return segment.Data["id"] == strconv.FormatInt(previous.MessageID, 10)
		}
	}
	return false
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
	directed := rule.Explicit || rule.ForceReply || semantic.CallAddressing == domain.CallYes || decision.ReasonCode == "awaited_content_received"
	if addressesOther(in) && !in.MentionsBot() && !rule.AlwaysReply {
		return withScores(domain.DecisionSilent, "addressed_to_other", decisionScores(semantic))
	}
	if candidate != nil && candidate.Active {
		switch action {
		case domain.TopicLeave, domain.TopicClose:
			if candidate.Waiting && semantic.CallAddressing == domain.CallNo {
				return decision
			}
			decision = withScores(domain.DecisionSilent, "topic_"+action, decisionScores(semantic))
			decision.Topic = &domain.TopicChange{ID: candidate.ID, Action: action, Source: source,
				ExpiresAt: candidate.ExpiresAt}
			return decision
		case domain.TopicFollowUp, domain.TopicContribution:
			if decision.Decision == domain.DecisionReply || semantic.CallAddressing == "" && decision.Decision == domain.DecisionSilent && decision.ReasonCode != "addressed_to_other" {
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
	if in.Topics.BoundID != 0 {
		if candidate == nil || !candidate.Active {
			return silent("topic_version_stale")
		}
		if decision.Decision == domain.DecisionReply {
			decision.Topic = &domain.TopicChange{ID: candidate.ID, Action: domain.TopicFollowUp, Source: "routing", ExpiresAt: candidate.ExpiresAt}
		}
		return decision
	}
	if cooldown && (decision.Decision == domain.DecisionReply || decision.Decision == domain.DecisionDefer) {
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

// topicWithoutHistorySegments keeps executable attachment references out of decision snapshots.
// topicWithoutHistorySegments 从决策快照中移除可执行的历史附件引用。
func topicWithoutHistorySegments(topics *domain.TopicContext) *domain.TopicContext {
	if topics == nil || topics.BoundID == 0 {
		return topics
	}
	copy := *topics
	copy.Candidates = slices.Clone(topics.Candidates)
	for i := range copy.Candidates {
		copy.Candidates[i].History = slices.Clone(copy.Candidates[i].History)
		for j := range copy.Candidates[i].History {
			copy.Candidates[i].History[j].Segments = nil
		}
	}
	if topics.Reference != nil {
		ref := *topics.Reference
		ref.Segments = nil
		copy.Reference = &ref
	}
	return &copy
}

func topicReplyHistory(in domain.Input, decision domain.FinalDecision) []domain.Message {
	if in.Topics != nil && decision.Topic != nil && decision.Topic.Action != "opened" && decision.Topic.Action != "waiting" {
		for _, candidate := range in.Topics.Candidates {
			if candidate.ID == decision.Topic.ID {
				if decision.ReasonCode == "awaited_content_received" && len(candidate.History) == 0 {
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
	if decision.ReasonCode == "awaited_content_received" && awaitingReply(in) {
		return replyHistory(in, decision.ReasonCode)
	}
	if in.Topics != nil && decision.Topic != nil && decision.Topic.Action == "opened" {
		if in.Topics.Reference != nil {
			return []domain.Message{*in.Topics.Reference}
		}
	}
	return replyHistory(in, decision.ReasonCode)
}
