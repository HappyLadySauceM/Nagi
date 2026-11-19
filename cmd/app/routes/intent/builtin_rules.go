// Read-only built-in rule catalog and effective rule view for the rules page.
// 规则页使用的只读内置规则目录与生效视图。
package intent

import (
	"fmt"
	"net/http"

	"github.com/HappyLadySauceM/Nagi/cmd/app/types/common"
	v1 "github.com/HappyLadySauceM/Nagi/cmd/app/types/v1"
	intentdomain "github.com/HappyLadySauceM/Nagi/internal/intent/domain"
	scopedomain "github.com/HappyLadySauceM/Nagi/internal/scope/domain"
)

// BuiltinRules returns the complete built-in rule catalog. It is static and
// works without a database, Langfuse or any external credentials.
// BuiltinRules 返回完整的内置规则目录；不依赖数据库、Langfuse 或任何凭据。
func (h *Handler) BuiltinRules(w http.ResponseWriter, r *http.Request) {
	common.WriteSuccess(w, map[string]any{
		"Version":          intentdomain.BuiltinRulesVersion,
		"Source":           "builtin",
		"FixedConstraints": fixedConstraintCatalog(),
		"Rules":            builtinRuleCatalog(),
	})
}

// EffectiveRules returns the built-in catalog together with the effective
// switches from the existing versioned rulesets. Drafts and switches continue
// to be managed through /api/v1/intent/rulesets.
// EffectiveRules 返回内置目录及现有版本规则集的实际开关；
// 草稿与开关继续通过 /api/v1/intent/rulesets 管理。
func (h *Handler) EffectiveRules(w http.ResponseWriter, r *http.Request) {
	botID, scopeType, scopeID, ok := scopeQuery(w, r)
	if !ok {
		return
	}

	policy := scopedomain.Default(scopedomain.ScopeType(scopeType), scopeID)
	if h.svcCtx.Scope != nil {
		resolved, err := h.svcCtx.Scope.Resolve(r.Context(), botID, scopedomain.ScopeType(scopeType), scopeID)
		if err != nil {
			common.WriteFailure(w, http.StatusInternalServerError, "读取作用域策略失败")
			return
		}
		policy = resolved
	}

	switches := map[string]bool{}
	var specific, global *intentdomain.Ruleset
	hasSpecific, hasGlobal := false, false
	if h.svcCtx.Rulesets != nil {
		if scopeID > 0 {
			loaded, found, err := h.svcCtx.Rulesets.Active(r.Context(), botID, scopeType, scopeID)
			if err != nil {
				common.WriteFailure(w, http.StatusInternalServerError, "读取生效规则失败")
				return
			}
			if found {
				specific, hasSpecific = &loaded, true
			}
		}
		loaded, found, err := h.svcCtx.Rulesets.Active(r.Context(), botID, "group", 0)
		if err != nil {
			common.WriteFailure(w, http.StatusInternalServerError, "读取全局规则失败")
			return
		}
		if found {
			global, hasGlobal = &loaded, true
		}
		for key, value := range mapOfRules(global, hasGlobal) {
			switches[key] = value
		}
		for key, value := range mapOfRules(specific, hasSpecific) {
			switches[key] = value
		}
	}

	rules := make([]map[string]any, 0, len(intentdomain.BuiltinRules()))
	for _, definition := range intentdomain.BuiltinRules() {
		enabled := true
		if value, exists := switches[definition.RuleID]; exists {
			enabled = value
		}
		applicable := enabled && intentdomain.BuiltinRuleAppliesToScope(definition, scopeType)
		status, reason := "active", ""
		switch {
		case !enabled:
			status, reason = "not_applicable", "disabled"
		case !applicable:
			status, reason = "not_applicable", "scope"
		}
		rules = append(rules, map[string]any{
			"RuleID": definition.RuleID, "Order": definition.Order, "Title": definition.Title,
			"Condition": definition.Condition, "Action": string(definition.Action),
			"Editable": definition.Editable, "Source": definition.Source,
			"Enabled": enabled, "Applicable": applicable, "Status": status, "Reason": reason,
		})
	}

	version, source := intentdomain.BuiltinRulesVersion, "builtin"
	var activeRuleset any
	switch {
	case hasSpecific:
		version = fmt.Sprintf("scope:%d@%d", specific.ID, specific.Version)
		source = "specific"
		activeRuleset = toRulesetInfo(*specific)
	case hasGlobal:
		version = fmt.Sprintf("global:%d@%d", global.ID, global.Version)
		source = "global"
		activeRuleset = toRulesetInfo(*global)
	}
	response := map[string]any{
		"Version":             version,
		"BuiltinRulesVersion": intentdomain.BuiltinRulesVersion,
		"Source":              source,
		"BuiltinRules":        rules,
		"FixedConstraints":    effectiveFixedConstraints(policy),
		"ActiveRuleset":       activeRuleset,
		"Policy": map[string]any{
			"Source": policy.Source, "Version": policy.Version, "TemplateVersion": policy.TemplateVersion,
			"Enabled": policy.Enabled, "ReplyMode": string(policy.ReplyMode),
			"MinReplyIntervalMS": policy.MinReplyIntervalMS, "MaxRepliesPerHour": policy.MaxRepliesPerHour,
			"MaxMessagesPerRun": policy.MaxMessagesPerRun, "MaxCharsPerMessage": policy.MaxCharsPerMessage,
			"MaxTotalCharsPerRun": policy.MaxTotalCharsPerRun, "MessageIntervalMS": policy.MessageIntervalMS,
		},
		"Thresholds": effectiveThresholds(h),
	}
	if hasGlobal {
		response["GlobalRuleset"] = toRulesetInfo(*global)
	}
	common.WriteSuccess(w, response)
}

func builtinRuleCatalog() []v1.BuiltinRuleCatalogEntry {
	definitions := intentdomain.BuiltinRules()
	out := make([]v1.BuiltinRuleCatalogEntry, 0, len(definitions))
	for _, definition := range definitions {
		out = append(out, v1.BuiltinRuleCatalogEntry{
			RuleID: definition.RuleID, Order: definition.Order, Title: definition.Title,
			Condition: definition.Condition, Action: string(definition.Action),
			Editable: definition.Editable, Source: definition.Source,
		})
	}
	return out
}

func fixedConstraintCatalog() []v1.FixedConstraintCatalogEntry {
	definitions := intentdomain.FixedConstraints()
	out := make([]v1.FixedConstraintCatalogEntry, 0, len(definitions))
	for _, definition := range definitions {
		out = append(out, v1.FixedConstraintCatalogEntry{
			ConstraintID: definition.ConstraintID, Order: definition.Order, Title: definition.Title,
			Condition: definition.Condition, Action: definition.Action, ReasonCode: definition.ReasonCode,
			Editable: definition.Editable, Source: definition.Source,
		})
	}
	return out
}

func effectiveFixedConstraints(policy scopedomain.Policy) []map[string]any {
	values := map[string]any{
		"policy_enabled":   policy.Enabled,
		"observe_only":     policy.ReplyMode == scopedomain.ModeObserveOnly,
		"cooldown":         policy.MinReplyIntervalMS,
		"hourly_limit":     policy.MaxRepliesPerHour,
		"tool_permissions": "server_enforced",
		"sending_budget": map[string]int{
			"MaxMessagesPerRun": policy.MaxMessagesPerRun, "MaxCharsPerMessage": policy.MaxCharsPerMessage,
			"MaxTotalCharsPerRun": policy.MaxTotalCharsPerRun, "MessageIntervalMS": policy.MessageIntervalMS,
		},
	}
	definitions := intentdomain.FixedConstraints()
	out := make([]map[string]any, 0, len(definitions))
	for _, definition := range definitions {
		applicable := definition.ConstraintID == "policy_enabled" || policy.Enabled
		status, reason := "active", ""
		if !applicable {
			status, reason = "not_applicable", "scope_disabled"
		}
		out = append(out, map[string]any{
			"ConstraintID": definition.ConstraintID, "Order": definition.Order, "Title": definition.Title,
			"Condition": definition.Condition, "Action": definition.Action, "ReasonCode": definition.ReasonCode,
			"Editable": definition.Editable, "Source": definition.Source,
			"Value": values[definition.ConstraintID], "Applicable": applicable, "Status": status, "Reason": reason,
		})
	}
	return out
}

func effectiveThresholds(h *Handler) map[string]float64 {
	thresholds := intentdomain.Thresholds{}
	if h.svcCtx.SettingsProvider != nil {
		live := h.svcCtx.SettingsProvider.Settings()
		thresholds = intentdomain.Thresholds{
			Continuation:      live.Intent.ContinuationThreshold,
			Interjection:      live.Intent.InterjectionThreshold,
			AddressedOtherMax: live.Intent.AddressedOtherMax,
		}
	}
	return map[string]float64{
		"Continuation": thresholds.Continuation, "Interjection": thresholds.Interjection,
		"AddressedOtherMax": thresholds.AddressedOtherMax,
	}
}

func mapOfRules(set *intentdomain.Ruleset, ok bool) map[string]bool {
	if !ok || set == nil {
		return nil
	}
	return set.BuiltinRules
}
