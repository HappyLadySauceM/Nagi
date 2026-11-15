// Read-only built-in rule catalog shared by evaluation and the administrative API.
// 只读内置规则目录，供求值与管理 API 共用。
package domain

import (
	scopedomain "github.com/HappyLadySauceM/Nagi/internal/scope/domain"
)

// BuiltinRulesVersion is the immutable version of the built-in rule catalog.
// BuiltinRulesVersion 是内置规则目录的不可变版本。
const BuiltinRulesVersion = "builtin-v1"

// BuiltinRuleScope limits where one built-in rule can apply.
// BuiltinRuleScope 限定单条内置规则的适用会话类型。
type BuiltinRuleScope string

const (
	// BuiltinRuleScopeAll applies to both group and private conversations.
	// BuiltinRuleScopeAll 适用于群聊与私聊。
	BuiltinRuleScopeAll BuiltinRuleScope = "all"
	// BuiltinRuleScopeGroup applies only to group conversations.
	// BuiltinRuleScopeGroup 仅适用于群聊。
	BuiltinRuleScopeGroup BuiltinRuleScope = "group"
)

// BuiltinRuleDefinition is one ordered built-in judgment. The exported fields
// are returned by the read-only catalog; the unexported matcher is the same
// predicate used by live evaluation, so display and execution cannot drift.
// BuiltinRuleDefinition 是一条有顺序的内置判断。导出字段由只读目录返回；
// 未导出的匹配函数与在线求值共用，避免展示与执行不一致。
type BuiltinRuleDefinition struct {
	RuleID     string
	Order      int
	Title      string
	Condition  string
	Action     RuleActionType
	ReasonCode string
	Editable   bool
	Source     string
	scope      BuiltinRuleScope
	matches    func(in Input, semantic SemanticAnswer, th Thresholds) bool
}

// FixedConstraintDefinition is one server-owned hard constraint shown to the
// rule page. Constraints that run outside the intent pipeline have no matcher.
// FixedConstraintDefinition 是规则页展示的一条服务端硬约束；
// 在意图流水线之外执行的约束没有匹配函数。
type FixedConstraintDefinition struct {
	ConstraintID string
	Order        int
	Title        string
	Condition    string
	Action       string
	ReasonCode   string
	Editable     bool
	Source       string
	matches      func(in Input) bool
}

// BuiltinRules returns the fixed ordered catalog of built-in judgments.
// BuiltinRules 返回固定顺序的内置判断目录。
func BuiltinRules() []BuiltinRuleDefinition {
	return []BuiltinRuleDefinition{
		{
			RuleID: "closing", Order: 10, Title: "告别结束", Condition: "intent=closing",
			Action: ActionSilent, ReasonCode: "closing", Editable: true, Source: "builtin",
			scope: BuiltinRuleScopeAll,
			matches: func(_ Input, semantic SemanticAnswer, _ Thresholds) bool {
				return semantic.IntentCategory == "closing"
			},
		},
		{
			RuleID: "addressed_to_other", Order: 20, Title: "对他人说话优先抑制",
			Condition: "addressed_to_other >= AddressedOtherMax",
			Action:    ActionSilent, ReasonCode: "addressed_to_other", Editable: true, Source: "builtin",
			scope: BuiltinRuleScopeGroup,
			matches: func(_ Input, semantic SemanticAnswer, th Thresholds) bool {
				return semantic.AddressedToOther >= th.AddressedOtherMax
			},
		},
		{
			RuleID: "continuation", Order: 30, Title: "延续机器人话题",
			Condition: "continues_bot_thread >= Continuation",
			Action:    ActionReply, ReasonCode: "active_thread_continuation", Editable: true, Source: "builtin",
			scope: BuiltinRuleScopeGroup,
			matches: func(_ Input, semantic SemanticAnswer, th Thresholds) bool {
				return semantic.ContinuesBotThread >= th.Continuation
			},
		},
		{
			RuleID: "interjection", Order: 40, Title: "自然插话",
			Condition: "reply_mode=intent_based && appropriate_to_join >= Interjection",
			Action:    ActionReply, ReasonCode: "interjection", Editable: true, Source: "builtin",
			scope: BuiltinRuleScopeGroup,
			matches: func(in Input, semantic SemanticAnswer, th Thresholds) bool {
				return in.Policy.ReplyMode == scopedomain.ModeIntentBased && semantic.AppropriateToJoin >= th.Interjection
			},
		},
		{
			RuleID: "directed_to_bot", Order: 50, Title: "指向机器人",
			Condition: "directed_to_bot >= Continuation",
			Action:    ActionReply, ReasonCode: "directed_to_bot", Editable: true, Source: "builtin",
			scope: BuiltinRuleScopeGroup,
			matches: func(_ Input, semantic SemanticAnswer, th Thresholds) bool {
				return semantic.DirectedToBot >= th.Continuation
			},
		},
	}
}

// FixedConstraints returns the server-owned hard constraints in evaluation
// order. They remain enforced outside prompt content and cannot be disabled
// from the rule page.
// FixedConstraints 按求值顺序返回服务端硬约束；它们不进入 Prompt 内容，
// 也不能在规则页关闭。
func FixedConstraints() []FixedConstraintDefinition {
	return []FixedConstraintDefinition{
		{
			ConstraintID: "policy_enabled", Order: 10, Title: "作用域启用",
			Condition: "policy.enabled=false", Action: "silent", ReasonCode: "policy_disabled",
			Editable: false, Source: "server",
			matches: func(in Input) bool { return !in.Policy.Enabled },
		},
		{
			ConstraintID: "observe_only", Order: 20, Title: "观察模式",
			Condition: "reply_mode=observe_only", Action: "silent", ReasonCode: "observe_only",
			Editable: false, Source: "server",
			matches: func(in Input) bool { return in.Policy.ReplyMode == scopedomain.ModeObserveOnly },
		},
		{
			ConstraintID: "cooldown", Order: 30, Title: "回复冷却",
			Condition: "seconds_since_bot_reply < MinReplyIntervalMS", Action: "silent", ReasonCode: "cooldown",
			Editable: false, Source: "server",
			matches: func(in Input) bool {
				if in.Policy.MinReplyIntervalMS <= 0 {
					return false
				}
				if since := in.BotLastSpoke(); since >= 0 {
					return since*1000 < int64(in.Policy.MinReplyIntervalMS)
				}
				return false
			},
		},
		{
			ConstraintID: "hourly_limit", Order: 40, Title: "小时回复额度",
			Condition: "replies_last_hour >= MaxRepliesPerHour", Action: "silent", ReasonCode: "rate_limited",
			Editable: false, Source: "server",
			matches: func(in Input) bool {
				return in.Policy.MaxRepliesPerHour > 0 && in.BotRepliesLastHour() >= in.Policy.MaxRepliesPerHour
			},
		},
		{
			ConstraintID: "tool_permissions", Order: 50, Title: "工具权限",
			Condition: "tool permission not granted", Action: "deny_tool", ReasonCode: "authorization_revoked",
			Editable: false, Source: "server",
		},
		{
			ConstraintID: "sending_budget", Order: 60, Title: "发送预算",
			Condition: "max_messages_per_run / max_chars_per_message / max_total_chars_per_run exceeded",
			Action:    "deny_send", ReasonCode: "budget_exceeded", Editable: false, Source: "server",
		},
	}
}

// BuiltinRuleAppliesToScope reports whether a rule can apply to one scope kind.
// BuiltinRuleAppliesToScope 报告一条规则是否适用于指定会话类型。
func BuiltinRuleAppliesToScope(rule BuiltinRuleDefinition, scopeType string) bool {
	return rule.scope == BuiltinRuleScopeAll || scopeType != "private"
}

// EvaluateFixedConstraints applies the intent-pipeline hard constraints in
// catalog order and returns the first matched reason code.
// EvaluateFixedConstraints 按目录顺序应用意图流水线硬约束，返回首个命中的原因码。
func EvaluateFixedConstraints(in Input) (string, bool) {
	for _, constraint := range FixedConstraints() {
		if constraint.matches != nil && constraint.matches(in) {
			return constraint.ReasonCode, true
		}
	}
	return "", false
}

// EvaluateBuiltinRules applies enabled built-in judgments in catalog order and
// returns the first matching decision.
// EvaluateBuiltinRules 按目录顺序应用已启用的内置判断，返回首个命中的决策。
func EvaluateBuiltinRules(in Input, rule RuleResult, semantic SemanticAnswer, th Thresholds) (FinalDecision, bool) {
	enabled := func(key string) bool {
		value, exists := rule.BuiltinRules[key]
		return !exists || value
	}
	for _, definition := range BuiltinRules() {
		if !enabled(definition.RuleID) || !BuiltinRuleAppliesToScope(definition, in.Task.Kind) {
			continue
		}
		if definition.matches == nil || !definition.matches(in, semantic, th) {
			continue
		}
		decision := DecisionSilent
		if definition.Action == ActionReply {
			decision = DecisionReply
		}
		return FinalDecision{Decision: decision, ReasonCode: definition.ReasonCode}, true
	}
	return FinalDecision{}, false
}
