// Per-scope sending policy endpoints: the gray-release switch for autonomous replies.
// 按作用域的出站策略接口：自主回复的灰度开关。
package agent

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/HappyLadySauceM/Nagi/cmd/app/types/common"
	outboundinfra "github.com/HappyLadySauceM/Nagi/internal/outbound/infrastructure"
	scopedomain "github.com/HappyLadySauceM/Nagi/internal/scope/domain"
)

// policyInfo is the API representation of one scope's sending policy.
// policyInfo 是单个作用域出站策略的 API 表示。
type policyInfo struct {
	BotID               int64
	ScopeType           string
	ScopeID             int64
	Exists              bool
	AllowSendCurrent    bool
	MaxMessagesPerRun   int
	MaxCharsPerMessage  int
	MaxTotalCharsPerRun int
}

// RegisterPolicy wires the sending-policy endpoint.
// RegisterPolicy 注册出站策略接口。
func (h *Handler) RegisterPolicy(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/agent/sending-policy", h.GetSendingPolicy)
	mux.HandleFunc("PUT /api/v1/agent/sending-policy", h.PutSendingPolicy)
}

// GetSendingPolicy returns the stored policy, or the process defaults when the
// scope has no row yet.
// GetSendingPolicy 返回已存策略；作用域尚无记录时返回进程默认值。
func (h *Handler) GetSendingPolicy(w http.ResponseWriter, r *http.Request) {
	botID, scopeType, scopeID, ok := policyQuery(w, r)
	if !ok {
		return
	}
	info := policyInfo{
		BotID: botID, ScopeType: scopeType, ScopeID: scopeID,
		MaxMessagesPerRun:   h.svcCtx.Config.Agent.Sending.MaxMessagesPerRun,
		MaxCharsPerMessage:  h.svcCtx.Config.Agent.Sending.MaxCharsPerMessage,
		MaxTotalCharsPerRun: h.svcCtx.Config.Agent.Sending.MaxTotalCharsPerRun,
	}
	policy, exists, err := h.svcCtx.SendingPolicies.Get(r.Context(), botID, scopeType, scopeID)
	if err != nil {
		common.WriteFailure(w, http.StatusInternalServerError, "读取出站策略失败")
		return
	}
	if exists {
		info.Exists = true
		info.AllowSendCurrent = policy.AllowSendCurrent
		info.MaxMessagesPerRun = policy.MaxMessagesPerRun
		info.MaxCharsPerMessage = policy.MaxCharsPerMessage
		info.MaxTotalCharsPerRun = policy.MaxTotalCharsPerRun
	}
	common.WriteSuccess(w, info)
}

// PutSendingPolicy stores one policy.
// PutSendingPolicy 保存一条策略。
func (h *Handler) PutSendingPolicy(w http.ResponseWriter, r *http.Request) {
	var body struct {
		BotID               int64  `json:"bot_id"`
		ScopeType           string `json:"scope_type"`
		ScopeID             int64  `json:"scope_id"`
		AllowSendCurrent    bool   `json:"allow_send_current"`
		MaxMessagesPerRun   int    `json:"max_messages_per_run"`
		MaxCharsPerMessage  int    `json:"max_chars_per_message"`
		MaxTotalCharsPerRun int    `json:"max_total_chars_per_run"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		common.WriteFailure(w, http.StatusBadRequest, "请求体不是合法 JSON")
		return
	}
	if body.BotID <= 0 || body.ScopeID <= 0 ||
		(body.ScopeType != string(scopedomain.ScopeGroup) && body.ScopeType != string(scopedomain.ScopePrivate)) {
		common.WriteFailure(w, http.StatusBadRequest, "bot_id、scope_type、scope_id 均为必填")
		return
	}
	if body.MaxMessagesPerRun <= 0 || body.MaxCharsPerMessage <= 0 || body.MaxTotalCharsPerRun <= 0 {
		common.WriteFailure(w, http.StatusBadRequest, "预算上限必须为正数")
		return
	}
	if body.MaxCharsPerMessage > body.MaxTotalCharsPerRun {
		common.WriteFailure(w, http.StatusBadRequest, "单条上限不得大于整轮上限")
		return
	}
	if err := h.svcCtx.SendingPolicies.Upsert(r.Context(), outboundinfra.Policy{
		BotID: body.BotID, ScopeType: body.ScopeType, ScopeID: body.ScopeID,
		AllowSendCurrent: body.AllowSendCurrent, MaxMessagesPerRun: body.MaxMessagesPerRun,
		MaxCharsPerMessage: body.MaxCharsPerMessage, MaxTotalCharsPerRun: body.MaxTotalCharsPerRun,
	}); err != nil {
		common.WriteFailure(w, http.StatusInternalServerError, "保存出站策略失败")
		return
	}
	common.WriteSuccess(w, map[string]any{"Saved": true})
}

// policyQuery parses the bot/scope triple.
// policyQuery 解析账号与作用域三元组。
func policyQuery(w http.ResponseWriter, r *http.Request) (int64, string, int64, bool) {
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
	if err != nil || scopeID <= 0 {
		common.WriteFailure(w, http.StatusBadRequest, "scope_id 必须为正整数")
		return 0, "", 0, false
	}
	return botID, scopeType, scopeID, true
}
