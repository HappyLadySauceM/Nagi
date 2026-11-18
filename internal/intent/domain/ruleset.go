// Versioned intent rulesets: drafts are editable, published versions immutable.
// 版本化意图规则：草稿可编辑，已发布版本不可变。
package domain

import (
	"errors"
	"fmt"
	"strings"
)

// RuleActionType is the effect of a matched rule.
// RuleActionType 是规则命中后的效果。
type RuleActionType string

// Supported actions. Unknown actions are rejected at validation time.
// 支持的动作。未知动作在校验阶段即被拒绝。
const (
	ActionReply           RuleActionType = "reply"
	ActionSilent          RuleActionType = "silent"
	ActionRequireSemantic RuleActionType = "require_semantic"
)

// ErrInvalidRule reports a rule entry that cannot be stored or evaluated.
// ErrInvalidRule 表示规则条目无法存储或求值。
var ErrInvalidRule = errors.New("intent: invalid rule")

// RuleMatch is the condition of one rule entry; zero fields are wildcards.
// RuleMatch 是规则条目的条件；零值字段表示通配。
type RuleMatch struct {
	ReplyMode            string `json:"reply_mode,omitempty"`
	Kind                 string `json:"kind,omitempty"`
	Explicit             *bool  `json:"explicit,omitempty"`
	TextContains         string `json:"text_contains,omitempty"`
	MinSecondsSinceReply int    `json:"min_seconds_since_reply,omitempty"`
}

// RuleAction is what a matched entry does.
// RuleAction 是命中条目的动作。
type RuleAction struct {
	Type       RuleActionType `json:"type"`
	ReasonCode string         `json:"reason_code"`
}

// RuleEntry is one prioritized rule.
// RuleEntry 是一条带优先级的规则。
type RuleEntry struct {
	Enabled  *bool      `json:"enabled,omitempty"`
	Priority int        `json:"priority"`
	Match    RuleMatch  `json:"match"`
	Action   RuleAction `json:"action"`
}

// Ruleset is one stored version together with its entries.
// Ruleset 是一个已存储版本及其条目。
type RuleTarget struct {
	Kind string `json:"kind"`
	ID   int64  `json:"id"`
}

type Ruleset struct {
	Targets      []RuleTarget
	ID           int64
	BotID        int64
	ScopeType    string
	ScopeID      int64
	Version      int
	Status       string
	Note         string
	Entries      []RuleEntry
	BuiltinRules map[string]bool
	// Questions overrides the SystemOne question set for this version. Empty
	// means the built-in prompt questions are used.
	// Questions 覆盖本版本使用的 SystemOne 问题集；为空表示沿用内置 Prompt 问题。
	Questions map[string]PromptQuestion
}

// Validate rejects malformed entries before they reach storage or evaluation.
// Validate 在条目进入存储或求值前拒绝格式错误。
func (e RuleEntry) Validate() error {
	if e.Priority < 0 {
		return fmt.Errorf("%w: priority must be >= 0", ErrInvalidRule)
	}
	switch e.Action.Type {
	case ActionReply, ActionSilent, ActionRequireSemantic:
	default:
		return fmt.Errorf("%w: unknown action %q", ErrInvalidRule, e.Action.Type)
	}
	if strings.TrimSpace(e.Action.ReasonCode) == "" {
		return fmt.Errorf("%w: action.reason_code is required", ErrInvalidRule)
	}
	if e.Match.ReplyMode != "" && e.Match.ReplyMode != "intent_based" &&
		e.Match.ReplyMode != "always_reply" && e.Match.ReplyMode != "mention_only" &&
		e.Match.ReplyMode != "observe_only" {
		return fmt.Errorf("%w: unknown reply_mode %q", ErrInvalidRule, e.Match.ReplyMode)
	}
	if e.Match.Kind != "" && e.Match.Kind != "group" && e.Match.Kind != "private" {
		return fmt.Errorf("%w: unknown kind %q", ErrInvalidRule, e.Match.Kind)
	}
	if e.Match.MinSecondsSinceReply < 0 {
		return fmt.Errorf("%w: min_seconds_since_reply must be >= 0", ErrInvalidRule)
	}
	return nil
}

// Validate checks priorities, actions and duplicate priorities.
// Validate 校验优先级、动作与重复优先级。
func (r Ruleset) Validate() error {
	seenTargets := make(map[RuleTarget]bool)
	if len(r.Targets) > 200 {
		return fmt.Errorf("%w: too many targets", ErrInvalidRule)
	}
	for _, target := range r.Targets {
		if (target.Kind != "private" && target.Kind != "group") || target.ID <= 0 || seenTargets[target] {
			return fmt.Errorf("%w: invalid or duplicate target", ErrInvalidRule)
		}
		seenTargets[target] = true
	}
	for key := range r.BuiltinRules {
		switch key {
		case "closing", "continuation", "directed_to_bot", "addressed_to_other", "interjection":
		default:
			return fmt.Errorf("%w: unknown builtin rule %q", ErrInvalidRule, key)
		}
	}
	if len(r.Questions) > 0 {
		if err := ValidateSystemOnePrompt(&SystemOnePrompt{Questions: r.Questions}); err != nil {
			return err
		}
		if err := validateDecisionQuestions(r.Questions); err != nil {
			return err
		}
	}
	seen := make(map[int]bool, len(r.Entries))
	for _, entry := range r.Entries {
		if err := entry.Validate(); err != nil {
			return err
		}
		if seen[entry.Priority] {
			return fmt.Errorf("%w: duplicate priority %d", ErrInvalidRule, entry.Priority)
		}
		seen[entry.Priority] = true
	}
	return nil
}

// validateDecisionQuestions checks the ruleset-only decision fields. Prompt
// content validation stays in ValidateSystemOnePrompt and does not require them.
// validateDecisionQuestions 校验只属于规则集的决策字段。Prompt 内容校验仍在
// ValidateSystemOnePrompt，不要求这些字段。
func validateDecisionQuestions(questions map[string]PromptQuestion) error {
	for key, question := range questions {
		switch question.Scope {
		case QuestionScopeAll, QuestionScopeGroup, QuestionScopePrivate:
		default:
			return fmt.Errorf("%w: question %s scope", ErrInvalidRule, key)
		}
		switch question.Action {
		case ActionReply, ActionSilent:
		default:
			return fmt.Errorf("%w: question %s action", ErrInvalidRule, key)
		}
		if question.Order < 0 {
			return fmt.Errorf("%w: question %s order", ErrInvalidRule, key)
		}
		if question.Threshold <= 0 || question.Threshold > 1 {
			return fmt.Errorf("%w: question %s threshold", ErrInvalidRule, key)
		}
	}
	return nil
}
