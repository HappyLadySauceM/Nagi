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
	Priority int        `json:"priority"`
	Match    RuleMatch  `json:"match"`
	Action   RuleAction `json:"action"`
}

// Ruleset is one stored version together with its entries.
// Ruleset 是一个已存储版本及其条目。
type Ruleset struct {
	ID        int64
	BotID     int64
	ScopeType string
	ScopeID   int64
	Version   int
	Status    string
	Note      string
	Entries   []RuleEntry
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
