// Read-only built-in rule catalog shared by evaluation and the administrative API.
// 只读内置规则目录，供求值与管理 API 共用。
package domain

import (
	"strconv"

	scopedomain "github.com/HappyLadySauceM/Nagi/internal/scope/domain"
)

// BuiltinRulesVersion is the immutable version of the built-in rule catalog.
// BuiltinRulesVersion 是内置规则目录的不可变版本。
const BuiltinRulesVersion = "builtin-v1"

const (
	CooldownRuleID      = "cooldown"
	CooldownRuleVersion = "cooldown-v1"
	cooldownOperator    = "<"
	cooldownScale       = 1000
)

// CooldownEvent is a decision-local observation of the operands and branch.
// CooldownEvent 保存本次决策使用的操作数和所走分支。
type CooldownEvent struct {
	RuleID          string
	Version         string
	Sequence        int
	Attempt         int `json:",omitempty"`
	Branch          string
	Operator        string `json:",omitempty"`
	ThresholdMS     *int   `json:",omitempty"`
	ElapsedMS       *int64 `json:",omitempty"`
	PrecisionMS     int    `json:",omitempty"`
	InputSource     string `json:",omitempty"`
	PolicySource    string `json:",omitempty"`
	PolicyVersion   int64  `json:",omitempty"`
	TemplateVersion int64  `json:",omitempty"`
	TopicSource     string `json:",omitempty"`
	ReasonCode      string `json:",omitempty"`
}

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
				return questionRolePresent(semantic, QuestionRoleIntent) && semantic.IntentCategory == "closing"
			},
		},
		{
			RuleID: "addressed_to_other", Order: 20, Title: "对他人说话优先抑制",
			Condition: "addressed_to_other >= AddressedOtherMax",
			Action:    ActionSilent, ReasonCode: "addressed_to_other", Editable: true, Source: "builtin",
			scope: BuiltinRuleScopeGroup,
			matches: func(_ Input, semantic SemanticAnswer, th Thresholds) bool {
				return questionRolePresent(semantic, QuestionRoleAddressedToOther) && semantic.AddressedToOther >= th.AddressedOtherMax
			},
		},
		{
			RuleID: "continuation", Order: 30, Title: "延续机器人话题",
			Condition: "continues_bot_thread >= Continuation",
			Action:    ActionReply, ReasonCode: "active_thread_continuation", Editable: true, Source: "builtin",
			scope: BuiltinRuleScopeGroup,
			matches: func(_ Input, semantic SemanticAnswer, th Thresholds) bool {
				return questionRolePresent(semantic, QuestionRoleContinuesBotThread) && semantic.ContinuesBotThread >= th.Continuation
			},
		},
		{
			RuleID: "interjection", Order: 40, Title: "自然插话",
			Condition: "reply_mode=intent_based && appropriate_to_join >= Interjection",
			Action:    ActionReply, ReasonCode: "interjection", Editable: true, Source: "builtin",
			scope: BuiltinRuleScopeGroup,
			matches: func(in Input, semantic SemanticAnswer, th Thresholds) bool {
				return in.Policy.ReplyMode == scopedomain.ModeIntentBased &&
					questionRolePresent(semantic, QuestionRoleAppropriateToJoin) &&
					semantic.AppropriateToJoin >= th.Interjection
			},
		},
		{
			RuleID: "directed_to_bot", Order: 50, Title: "指向机器人",
			Condition: "directed_to_bot >= Continuation",
			Action:    ActionReply, ReasonCode: "directed_to_bot", Editable: true, Source: "builtin",
			scope: BuiltinRuleScopeGroup,
			matches: func(_ Input, semantic SemanticAnswer, th Thresholds) bool {
				return questionRolePresent(semantic, QuestionRoleDirectedToBot) && semantic.DirectedToBot >= th.Continuation
			},
		},
	}
}

// questionRolePresent reports whether a named role contributed a score. A nil
// RolePresent means the answer predates dynamic questions or was built by a
// legacy caller; the named score is then treated as present so the original
// five-question behavior is unchanged. Non-nil maps only report configured roles.
// questionRolePresent 报告具名角色是否产生了分数。RolePresent 为 nil 表示回答早于
// 动态问题或由旧调用方构造；此时按存在处理，保持原有五问行为不变。
// 非 nil 映射只报告已配置的角色。
func questionRolePresent(semantic SemanticAnswer, role string) bool {
	if semantic.RolePresent == nil {
		return true
	}
	return semantic.RolePresent[role]
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
			ConstraintID: CooldownRuleID, Order: 30, Title: "回复冷却",
			Condition: "seconds_since_bot_reply × " + strconv.Itoa(cooldownScale) + " " + cooldownOperator + " MinReplyIntervalMS", Action: "silent", ReasonCode: CooldownRuleID,
			Editable: false, Source: "server",
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
	reason, _ := EvaluateCooldownGate(in, false)
	return reason, reason != ""
}

// EvaluateCooldownGate runs the fixed gates in order and records the cooldown branch.
// EvaluateCooldownGate 按顺序执行固定闸门，并记录冷却分支。
func EvaluateCooldownGate(in Input, canDefer bool) (string, CooldownEvent) {
	event := CooldownEvent{RuleID: CooldownRuleID, Version: CooldownRuleVersion, Sequence: 1, Branch: "not_executed"}
	for _, constraint := range FixedConstraints() {
		if constraint.ConstraintID == CooldownRuleID {
			event = compareCooldown(in, event)
			if event.Branch == "blocked" {
				if !canDefer {
					return constraint.ReasonCode, event
				}
				event.Branch = "deferred"
			}
			continue
		}
		if constraint.matches != nil && constraint.matches(in) {
			if event.Branch == "not_executed" {
				event.ReasonCode = constraint.ReasonCode
			}
			return constraint.ReasonCode, event
		}
	}
	return "", event
}

func compareCooldown(in Input, event CooldownEvent) CooldownEvent {
	threshold := in.Policy.MinReplyIntervalMS
	event.ThresholdMS = &threshold
	event.Operator, event.PrecisionMS = cooldownOperator, cooldownScale
	event.PolicySource, event.PolicyVersion = in.Policy.Source, in.Policy.Version
	event.TemplateVersion = in.Policy.TemplateVersion
	if threshold <= 0 {
		event.Branch = "disabled"
		return event
	}
	since, source := in.botLastSpoke()
	event.InputSource = source
	if since < 0 {
		event.Branch = "input_missing"
		return event
	}
	elapsed := since * cooldownScale
	event.ElapsedMS = &elapsed
	switch {
	case elapsed < int64(threshold):
		event.Branch = "blocked"
	case elapsed == int64(threshold):
		event.Branch = "equal"
	default:
		event.Branch = "passed"
	}
	return event
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
