// Admin API for versioned intent rulesets and read-only simulation.
// 版本化意图规则的管理 API 与只读模拟。
package intent

import (
	"encoding/json"
	"errors"
	"net/http"
	"sort"
	"strconv"
	"time"

	"github.com/HappyLadySauceM/Nagi/cmd/app/types/common"
	intentapp "github.com/HappyLadySauceM/Nagi/internal/intent/application"
	intentdomain "github.com/HappyLadySauceM/Nagi/internal/intent/domain"
	intentinfra "github.com/HappyLadySauceM/Nagi/internal/intent/infrastructure"
	obdomain "github.com/HappyLadySauceM/Nagi/internal/onebot/domain"
	scopedomain "github.com/HappyLadySauceM/Nagi/internal/scope/domain"
)

// rulesetInfo is the API representation of one version.
// rulesetInfo 是单个版本的 API 表示。
type rulesetInfo struct {
	ID           int64
	BotID        int64
	ScopeType    string
	ScopeID      int64
	Version      int
	Status       string
	Note         string
	Entries      []entryInfo
	BuiltinRules map[string]bool
	Targets      []intentdomain.RuleTarget
}

// entryInfo is one rule entry.
// entryInfo 是一条规则条目。
type entryInfo struct {
	Enabled  *bool
	Priority int
	Match    intentdomain.RuleMatch
	Action   intentdomain.RuleAction
}

// RegisterRules wires the ruleset endpoints.
// RegisterRules 注册规则集接口。
func (h *Handler) RegisterRules(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/intent/rulesets", h.ListRulesets)
	mux.HandleFunc("POST /api/v1/intent/rulesets", h.CreateRuleset)
	mux.HandleFunc("GET /api/v1/intent/rulesets/{id}", h.GetRuleset)
	mux.HandleFunc("PATCH /api/v1/intent/rulesets/{id}", h.UpdateRuleset)
	mux.HandleFunc("DELETE /api/v1/intent/rulesets/{id}", h.DeleteRuleset)
	mux.HandleFunc("POST /api/v1/intent/rulesets/{id}/publish", h.PublishRuleset)
	mux.HandleFunc("POST /api/v1/intent/rulesets/{id}/rollback", h.RollbackRuleset)
	mux.HandleFunc("POST /api/v1/intent/simulate", h.Simulate)
}

// scopeQuery parses the bot/scope triple shared by the endpoints.
// scopeQuery 解析各接口共用的账号与作用域三元组。
func scopeQuery(w http.ResponseWriter, r *http.Request) (int64, string, int64, bool) {
	botID, err := strconv.ParseInt(r.URL.Query().Get("bot_id"), 10, 64)
	if err != nil || botID <= 0 {
		common.WriteFailure(w, http.StatusBadRequest, "bot_id 必填且必须为正整数")
		return 0, "", 0, false
	}
	scopeType := r.URL.Query().Get("scope_type")
	if scopeType != string(scopedomain.ScopeGroup) && scopeType != string(scopedomain.ScopePrivate) {
		common.WriteFailure(w, http.StatusBadRequest, "scope_type 必须是 group 或 private")
		return 0, "", 0, false
	}
	scopeID, err := strconv.ParseInt(r.URL.Query().Get("scope_id"), 10, 64)
	if err != nil || scopeID < 0 {
		common.WriteFailure(w, http.StatusBadRequest, "scope_id 必须为正整数")
		return 0, "", 0, false
	}
	return botID, scopeType, scopeID, true
}

// ListRulesets returns every version of one scope.
// ListRulesets 返回某作用域的所有版本。
func (h *Handler) ListRulesets(w http.ResponseWriter, r *http.Request) {
	botID, err := strconv.ParseInt(r.URL.Query().Get("bot_id"), 10, 64)
	if err != nil || botID <= 0 {
		common.WriteFailure(w, 400, "invalid bot_id")
		return
	}
	scopeType := r.URL.Query().Get("scope_type")
	scopeID := int64(0)
	if scopeType != "" {
		var ok bool
		botID, scopeType, scopeID, ok = scopeQuery(w, r)
		if !ok {
			return
		}
	}
	sets, err := h.svcCtx.Rulesets.List(r.Context(), botID, scopeType, scopeID)
	if err != nil {
		common.WriteFailure(w, http.StatusInternalServerError, "读取规则集失败")
		return
	}
	items := make([]rulesetInfo, 0, len(sets))
	for _, set := range sets {
		items = append(items, toRulesetInfo(set))
	}
	common.WriteSuccess(w, map[string]any{"Items": items})
}

// CreateRuleset creates the next version as a draft.
// CreateRuleset 以草稿创建下一个版本。
func (h *Handler) CreateRuleset(w http.ResponseWriter, r *http.Request) {
	var body struct {
		BotID       int64  `json:"bot_id"`
		ScopeType   string `json:"scope_type"`
		ScopeID     int64  `json:"scope_id"`
		Note        string `json:"note"`
		CloneActive bool   `json:"clone_active"`
		CloneFromID int64  `json:"clone_from_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		common.WriteFailure(w, http.StatusBadRequest, "请求体不是合法 JSON")
		return
	}
	if body.BotID <= 0 || body.ScopeID < 0 ||
		(body.ScopeType != string(scopedomain.ScopeGroup) && body.ScopeType != string(scopedomain.ScopePrivate)) {
		common.WriteFailure(w, http.StatusBadRequest, "bot_id、scope_type、scope_id 均为必填")
		return
	}
	set, err := h.svcCtx.Rulesets.CreateDraft(r.Context(), body.BotID, body.ScopeType, body.ScopeID, body.Note, body.CloneActive, body.CloneFromID)
	if errors.Is(err, intentinfra.ErrRulesetNotFound) {
		common.WriteFailure(w, http.StatusNotFound, "所选版本不存在或已被删除")
		return
	}
	if err != nil {
		common.WriteFailure(w, http.StatusInternalServerError, "创建草稿失败")
		return
	}
	common.WriteSuccess(w, toRulesetInfo(set))
}

// GetRuleset returns one version with its entries.
// GetRuleset 返回单个版本及其条目。
func (h *Handler) GetRuleset(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		common.WriteFailure(w, http.StatusBadRequest, "规则集 id 无效")
		return
	}
	set, err := h.svcCtx.Rulesets.Get(r.Context(), id)
	if errors.Is(err, intentinfra.ErrRulesetNotFound) {
		common.WriteFailure(w, http.StatusNotFound, "规则集不存在")
		return
	}
	if err != nil {
		common.WriteFailure(w, http.StatusInternalServerError, "读取规则集失败")
		return
	}
	common.WriteSuccess(w, toRulesetInfo(set))
}

// UpdateRuleset replaces the entries of a draft.
// UpdateRuleset 重写草稿的条目。
func (h *Handler) UpdateRuleset(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		common.WriteFailure(w, http.StatusBadRequest, "规则集 id 无效")
		return
	}
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	var body struct {
		Entries      []intentdomain.RuleEntry   `json:"entries"`
		BuiltinRules map[string]bool            `json:"builtin_rules"`
		Targets      *[]intentdomain.RuleTarget `json:"targets"`
	}
	if err := decoder.Decode(&body); err != nil {
		common.WriteFailure(w, http.StatusBadRequest, "请求体不合法或包含未知字段")
		return
	}
	targets := []intentdomain.RuleTarget{}
	if body.Targets != nil {
		targets = *body.Targets
	}
	if err := (intentdomain.Ruleset{Targets: targets, Entries: body.Entries, BuiltinRules: body.BuiltinRules}).Validate(); err != nil {
		common.WriteFailure(w, http.StatusBadRequest, err.Error())
		return
	}
	switch err := h.svcCtx.Rulesets.ReplaceEntries(r.Context(), id, body.Entries, intentinfra.RuleOptions{BuiltinRules: body.BuiltinRules, Targets: body.Targets}); {
	case errors.Is(err, intentinfra.ErrTargetConflict):
		common.WriteFailure(w, http.StatusConflict, "target already has another active ruleset")
		return
	case errors.Is(err, intentinfra.ErrRulesetNotFound):
		common.WriteFailure(w, http.StatusNotFound, "规则集不存在")
		return
	case errors.Is(err, intentinfra.ErrRulesetImmutable):
		common.WriteFailure(w, http.StatusConflict, "已发布版本不可修改")
		return
	case err != nil:
		common.WriteFailure(w, http.StatusInternalServerError, "保存草稿失败")
		return
	}
	set, err := h.svcCtx.Rulesets.Get(r.Context(), id)
	if err != nil {
		common.WriteFailure(w, http.StatusInternalServerError, "读取规则集失败")
		return
	}
	common.WriteSuccess(w, toRulesetInfo(set))
}

// PublishRuleset activates one version.
// PublishRuleset 激活某个版本。
func (h *Handler) PublishRuleset(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		common.WriteFailure(w, http.StatusBadRequest, "规则集 id 无效")
		return
	}
	switch err := h.svcCtx.Rulesets.Publish(r.Context(), id); {
	case errors.Is(err, intentinfra.ErrTargetConflict):
		common.WriteFailure(w, http.StatusConflict, "target already has another active ruleset")
		return
	case errors.Is(err, intentinfra.ErrRulesetNotFound):
		common.WriteFailure(w, http.StatusNotFound, "规则集不存在")
		return
	case err != nil:
		common.WriteFailure(w, http.StatusInternalServerError, "发布失败")
		return
	}
	common.WriteSuccess(w, map[string]any{"ID": id})
}

// RollbackRuleset re-activates a historical version.
// RollbackRuleset 重新激活历史版本。
func (h *Handler) RollbackRuleset(w http.ResponseWriter, r *http.Request) {
	var body struct {
		BotID     int64  `json:"bot_id"`
		ScopeType string `json:"scope_type"`
		ScopeID   int64  `json:"scope_id"`
		Version   int    `json:"version"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Version <= 0 ||
		body.BotID <= 0 || body.ScopeID < 0 {
		common.WriteFailure(w, http.StatusBadRequest, "bot_id、scope_type、scope_id 与 version 均为必填")
		return
	}
	switch err := h.svcCtx.Rulesets.Rollback(r.Context(), body.BotID, body.ScopeType, body.ScopeID, body.Version); {
	case errors.Is(err, intentinfra.ErrTargetConflict):
		common.WriteFailure(w, http.StatusConflict, "target already has another active ruleset")
		return
	case errors.Is(err, intentinfra.ErrRulesetNotFound):
		common.WriteFailure(w, http.StatusNotFound, "目标版本不存在")
		return
	case err != nil:
		common.WriteFailure(w, http.StatusInternalServerError, "回滚失败")
		return
	}
	common.WriteSuccess(w, map[string]any{"Version": body.Version})
}

// Simulate evaluates the active ruleset against a synthetic message without
// writing anything and without calling Jev.
// Simulate 用当前生效规则对一个合成消息求值，不写库、不调用 Jev。
func (h *Handler) Simulate(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Mode              string                    `json:"mode"`
		PreviewGlobal     bool                      `json:"preview_global"`
		BotID             int64                     `json:"bot_id"`
		ScopeType         string                    `json:"scope_type"`
		ScopeID           int64                     `json:"scope_id"`
		Kind              string                    `json:"kind"`
		ReplyMode         string                    `json:"reply_mode"`
		Explicit          bool                      `json:"explicit"`
		Text              string                    `json:"text"`
		SecondsSinceReply int64                     `json:"seconds_since_reply"`
		Entries           *[]intentdomain.RuleEntry `json:"entries"`
		BuiltinRules      map[string]bool           `json:"builtin_rules"`
		RulesetID         int64                     `json:"ruleset_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		common.WriteFailure(w, http.StatusBadRequest, "请求体不是合法 JSON")
		return
	}
	if (body.Mode != "" && body.Mode != "rules" && body.Mode != "semantic") || body.BotID <= 0 || body.ScopeID <= 0 || (body.Kind != "group" && body.Kind != "private") {
		common.WriteFailure(w, http.StatusBadRequest, "bot_id、scope_id 与 kind 均为必填")
		return
	}
	set, ok, err := h.svcCtx.Rulesets.Active(r.Context(), body.BotID, body.ScopeType, body.ScopeID)
	if err != nil {
		common.WriteFailure(w, http.StatusInternalServerError, "读取生效规则失败")
		return
	}
	activeSpecific, hasActiveSpecific := set, ok
	if body.SecondsSinceReply < 0 || body.SecondsSinceReply > 315360000 || body.ScopeType != body.Kind {
		common.WriteFailure(w, http.StatusBadRequest, "invalid simulation input")
		return
	}
	if body.Entries != nil && body.RulesetID != 0 {
		common.WriteFailure(w, http.StatusBadRequest, "select entries or version")
		return
	}
	if body.RulesetID != 0 {
		selected, readErr := h.svcCtx.Rulesets.Get(r.Context(), body.RulesetID)
		if readErr != nil || selected.BotID != body.BotID {
			common.WriteFailure(w, http.StatusBadRequest, "invalid ruleset")
			return
		}
		set, ok = selected, true
	}
	if body.Entries != nil {
		set.Entries = *body.Entries
		set.BuiltinRules = body.BuiltinRules
		set.Version = 0
		ok = true
	}
	if err := set.Validate(); err != nil {
		common.WriteFailure(w, http.StatusBadRequest, "invalid rule entries")
		return
	}
	sort.SliceStable(set.Entries, func(i, j int) bool { return set.Entries[i].Priority < set.Entries[j].Priority })
	now := time.Now()
	in := intentdomain.Input{
		Now:      now,
		Activity: intentdomain.BotActivity{Known: true, LastReplyAt: now.Add(-time.Duration(body.SecondsSinceReply) * time.Second)},
		Task: intentdomain.Task{Kind: body.Kind, BotID: body.BotID, TargetID: body.ScopeID,
			Message: intentdomain.Message{Text: body.Text}},
		Policy: scopedomain.Policy{
			Enabled: true, ReplyMode: scopedomain.ReplyMode(body.ReplyMode),
			ScopeType: scopedomain.ScopeType(body.ScopeType), ScopeID: body.ScopeID,
		},
	}
	if !in.Policy.ReplyMode.Valid() {
		common.WriteFailure(w, 400, "invalid reply mode")
		return
	}
	if body.Mode == "semantic" {
		if h.svcCtx.IntentSimulator == nil || h.svcCtx.Scope == nil {
			common.WriteFailure(w, 503, "semantic simulation unavailable")
			return
		}
		effective, err := h.svcCtx.Scope.Resolve(r.Context(), body.BotID, scopedomain.ScopeType(body.Kind), body.ScopeID)
		if err != nil {
			common.WriteFailure(w, 500, "read effective policy failed")
			return
		}
		effective.ReplyMode = in.Policy.ReplyMode
		in.Policy = effective
		prepared, err := h.svcCtx.IntentSimulator.PrepareSimulation(r.Context(), in)
		if err != nil {
			common.WriteFailure(w, 500, "read simulation activity failed")
			return
		}
		in = prepared
	}
	if body.Explicit {
		in.Task.Segments = []obdomain.Segment{{Type: obdomain.SegAt, Data: map[string]string{"qq": strconv.FormatInt(body.BotID, 10)}}}
	}
	global, hasGlobal, err := h.svcCtx.Rulesets.Active(r.Context(), body.BotID, "group", 0)
	if err != nil {
		common.WriteFailure(w, 500, "read global rules failed")
		return
	}
	var specificSet, globalSet *intentdomain.Ruleset
	if hasActiveSpecific {
		specificSet = &activeSpecific
	}
	if hasGlobal {
		globalSet = &global
	}
	if ok {
		if (body.RulesetID != 0 && len(set.Targets) == 0) || (body.Entries != nil && body.PreviewGlobal) {
			globalSet = &set
		} else {
			specificSet = &set
		}
	}
	rule, version := intentapp.EvaluateRuleChain(in, specificSet, globalSet)
	var final *intentdomain.FinalDecision
	var steps []intentdomain.Step
	if body.Mode == "semantic" {
		result, evaluatedSteps := h.svcCtx.IntentSimulator.EvaluateSimulation(r.Context(), in, rule, true)
		result.RulesetVersion = version
		final = &result
		steps = evaluatedSteps
	}
	reasonCode := rule.ReasonCode
	if rule.ForceReply {
		reasonCode = rule.ForceReason
	}

	common.WriteSuccess(w, map[string]any{
		"Final":          final,
		"Steps":          steps,
		"Mode":           body.Mode,
		"BuiltinRules":   rule.BuiltinRules,
		"Matched":        rule.Matched,
		"Blocked":        rule.Blocked,
		"Explicit":       rule.Explicit,
		"NeedSemantic":   rule.NeedSemantic,
		"AlwaysReply":    rule.AlwaysReply,
		"ForceReply":     rule.ForceReply,
		"ReasonCode":     reasonCode,
		"RulesetVersion": version,
	})
}

func toRulesetInfo(set intentdomain.Ruleset) rulesetInfo {
	info := rulesetInfo{
		ID: set.ID, BotID: set.BotID, ScopeType: set.ScopeType, ScopeID: set.ScopeID,
		Version: set.Version, Status: set.Status, Note: set.Note, BuiltinRules: set.BuiltinRules, Targets: set.Targets,
	}
	for _, entry := range set.Entries {
		info.Entries = append(info.Entries, entryInfo{Enabled: entry.Enabled, Priority: entry.Priority, Match: entry.Match, Action: entry.Action})
	}
	return info
}

// DeleteRuleset deletes one version belonging to the selected account.
// DeleteRuleset 删除所选账号下的单个版本。
func (h *Handler) DeleteRuleset(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		common.WriteFailure(w, http.StatusBadRequest, "规则集 id 无效")
		return
	}
	botID, err := strconv.ParseInt(r.URL.Query().Get("bot_id"), 10, 64)
	if err != nil || botID <= 0 {
		common.WriteFailure(w, http.StatusBadRequest, "bot_id 必填且必须为正整数")
		return
	}
	switch err = h.svcCtx.Rulesets.Delete(r.Context(), botID, id); {
	case errors.Is(err, intentinfra.ErrRulesetNotFound):
		common.WriteFailure(w, http.StatusNotFound, "规则集不存在")
		return
	case err != nil:
		common.WriteFailure(w, http.StatusInternalServerError, "删除规则集失败")
		return
	}
	common.WriteSuccess(w, map[string]any{"ID": id})
}
