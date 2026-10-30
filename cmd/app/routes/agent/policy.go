// Per-scope sending policy endpoints: the gray-release switch for autonomous replies.
// 按作用域的出站策略接口：自主回复的灰度开关。
package agent

import (
	"net/http"
	"strconv"

	"github.com/HappyLadySauceM/Nagi/cmd/app/types/common"
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
	EffectiveEnabled    bool
	BlockedReasons      []string
	MinIntervalMS       int
	MaxMessagesPerRun   int
	MaxCharsPerMessage  int
	MaxTotalCharsPerRun int
}

// RegisterPolicy wires the sending-policy endpoint.
// RegisterPolicy 注册出站策略接口。
func (h *Handler) RegisterPolicy(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/agent/sending-policy", h.GetSendingPolicy)
}

// GetSendingPolicy returns the stored policy, or the live settings when the scope
// has no row yet. There is no process-level switch any more: the conversation's
// own grant is the only sending permission.
// GetSendingPolicy 返回已存策略；作用域尚无记录时返回实时配置。
// 进程级总开关已被移除：会话自身的授权就是唯一的发送许可。
func (h *Handler) GetSendingPolicy(w http.ResponseWriter, r *http.Request) {
	botID, scopeType, scopeID, ok := policyQuery(w, r)
	if !ok {
		return
	}
	live := h.svcCtx.SettingsProvider.Settings()
	info := policyInfo{
		BotID: botID, ScopeType: scopeType, ScopeID: scopeID,
		MinIntervalMS:       700,
		BlockedReasons:      make([]string, 0),
		MaxMessagesPerRun:   live.Agent.Sending.MaxMessagesPerRun,
		MaxCharsPerMessage:  live.Agent.Sending.MaxCharsPerMessage,
		MaxTotalCharsPerRun: live.Agent.Sending.MaxTotalCharsPerRun,
	}
	policy, exists, err := h.svcCtx.SendingPolicies.Get(r.Context(), botID, scopeType, scopeID)
	if err != nil {
		common.WriteFailure(w, http.StatusInternalServerError, "读取出站策略失败")
		return
	}
	if exists {
		info.Exists = true
		info.MinIntervalMS = policy.MinIntervalMS
		info.MaxMessagesPerRun = policy.MaxMessagesPerRun
		info.MaxCharsPerMessage = policy.MaxCharsPerMessage
		info.MaxTotalCharsPerRun = policy.MaxTotalCharsPerRun
	}
	if h.svcCtx.Scope != nil {
		p, e := h.svcCtx.Scope.Resolve(r.Context(), botID, scopedomain.ScopeType(scopeType), scopeID)
		if e != nil {
			common.WriteFailure(w, 500, "policy unavailable")
			return
		}
		if !p.Enabled || !p.StoreMessages || p.ReplyMode == scopedomain.ModeObserveOnly {
			info.BlockedReasons = append(info.BlockedReasons, "会话参与或消息存储未授权")
		}
		granted := live.Tools.PermissionDefaults["qq.reply.current"]
		if v, ok := p.ToolPermissions["qq.reply.current"]; ok {
			granted = v
		}
		info.AllowSendCurrent = granted
		if !granted {
			info.BlockedReasons = append(info.BlockedReasons, "未授予当前会话回复工具权限")
		}
	}
	if !live.AI.Enabled {
		info.BlockedReasons = append(info.BlockedReasons, "未启用生成模型")
	}
	if !live.Intent.Enabled {
		info.BlockedReasons = append(info.BlockedReasons, "未启用意图流水线")
	}
	registered := false
	if h.svcCtx.Tools != nil {
		for _, tool := range h.svcCtx.Tools.Snapshot() {
			if tool.Descriptor.ID == "qq.send_current" {
				registered = true
			}
		}
	}
	if !registered {
		info.BlockedReasons = append(info.BlockedReasons, "当前进程未注册连续发送工具")
	}
	online := false
	if h.svcCtx.Accounts != nil {
		if conn, ok := h.svcCtx.Accounts.Lookup(botID); ok {
			online = conn.Connected()
		}
	}
	if !online {
		info.BlockedReasons = append(info.BlockedReasons, "机器人账号离线")
	}
	info.EffectiveEnabled = len(info.BlockedReasons) == 0
	common.WriteSuccess(w, info)
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
