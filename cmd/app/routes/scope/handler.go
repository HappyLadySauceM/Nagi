// Package scope serves scope policy management endpoints.
// Package scope 提供作用域策略管理接口。
package scope

import (
	"encoding/json"
	"errors"
	"net/http"
	"sort"
	"strconv"
	"strings"

	"github.com/HappyLadySauceM/Nagi/cmd/app/svc"
	"github.com/HappyLadySauceM/Nagi/cmd/app/types/common"
	scopedomain "github.com/HappyLadySauceM/Nagi/internal/scope/domain"
)

// Handler serves the scope policy endpoints.
// Handler 提供作用域策略接口。
type Handler struct {
	svcCtx *svc.ServiceContext
}

// NewHandler builds the scope handler.
// NewHandler 构建作用域处理器。
func NewHandler(svcCtx *svc.ServiceContext) *Handler {
	return &Handler{svcCtx: svcCtx}
}

// Register wires routes using Go 1.22 method+path patterns.
// Register 以 Go 1.22 的方法+路径模式注册路由。
func (h *Handler) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/scopes", h.List)
	mux.HandleFunc("GET /api/v1/scopes/{type}/{id}", h.Get)
	mux.HandleFunc("PUT /api/v1/scopes/{type}/{id}", h.Put)
	mux.HandleFunc("DELETE /api/v1/scopes/{type}/{id}", h.Delete)
}

// requireDB replies with 503 when the database is not configured.
// requireDB 在未配置数据库时返回 503。
func (h *Handler) requireDB(w http.ResponseWriter) bool {
	if h.svcCtx.Scope == nil {
		common.WriteFailure(w, http.StatusServiceUnavailable, "database not configured")
		return false
	}
	return true
}

// botID resolves the request account; ambiguous or unknown accounts are refused.
// botID 解析请求所属账号；多账号未指定或身份未知时拒绝。
func (h *Handler) botID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	botID, err := h.svcCtx.ResolveBotID(r.URL.Query().Get("bot_id"))
	if err != nil {
		switch {
		case errors.Is(err, svc.ErrBotIDRequired):
			common.WriteFailure(w, http.StatusBadRequest, "bot_id is required when several accounts are configured")
		case errors.Is(err, svc.ErrAccountUnknown):
			common.WriteFailure(w, http.StatusConflict, "bot account identity is not available yet; connect OneBot first")
		default:
			common.WriteFailure(w, http.StatusBadRequest, "invalid bot_id")
		}
		return 0, false
	}
	return botID, true
}

// List returns stored policies, or one effective policy when type and id are given.
// List 返回已存储策略；提供 type 与 id 时返回单个生效策略。
func (h *Handler) List(w http.ResponseWriter, r *http.Request) {
	if !h.requireDB(w) {
		return
	}
	q := r.URL.Query()
	if t, id, ok := parseScope(q.Get("type"), q.Get("id")); ok {
		botID, found := h.botID(w, r)
		if !found {
			return
		}
		policy, err := h.svcCtx.Scope.Get(r.Context(), botID, t, id)
		if err != nil {
			common.WriteFailure(w, http.StatusInternalServerError, "resolve scope failed")
			return
		}
		common.WriteSuccess(w, policy)
		return
	}
	var botID *int64
	if raw := q.Get("bot_id"); raw != "" {
		if v, err := strconv.ParseInt(raw, 10, 64); err == nil && v > 0 {
			botID = &v
		}
	}
	policies, err := h.svcCtx.Scope.List(r.Context(), botID)
	if err != nil {
		common.WriteFailure(w, http.StatusInternalServerError, "list scopes failed")
		return
	}
	common.WriteSuccess(w, policies)
}

// Get returns the effective policy of one scope.
// Get 返回单个作用域的生效策略。
func (h *Handler) Get(w http.ResponseWriter, r *http.Request) {
	if !h.requireDB(w) {
		return
	}
	t, id, ok := parseScope(r.PathValue("type"), r.PathValue("id"))
	if !ok {
		common.WriteFailure(w, http.StatusBadRequest, "invalid scope type or id")
		return
	}
	botID, found := h.botID(w, r)
	if !found {
		return
	}
	policy, err := h.svcCtx.Scope.Get(r.Context(), botID, t, id)
	if err != nil {
		common.WriteFailure(w, http.StatusInternalServerError, "resolve scope failed")
		return
	}
	common.WriteSuccess(w, policy)
}

// policyUpdate is the accepted JSON body of PUT /api/v1/scopes/{type}/{id}.
// policyUpdate 是 PUT /api/v1/scopes/{type}/{id} 接受的 JSON 请求体。
type policyUpdate struct {
	Enabled            bool            `json:"enabled"`
	ReplyMode          string          `json:"reply_mode"`
	StoreMessages      bool            `json:"store_messages"`
	AllowMemoryWrite   bool            `json:"allow_memory_write"`
	AllowHistoryRead   bool            `json:"allow_history_read"`
	MinReplyIntervalMS int             `json:"min_reply_interval_ms"`
	MaxRepliesPerHour  int             `json:"max_replies_per_hour"`
	RetentionDays      int             `json:"retention_days"`
	ToolPermissions    map[string]bool `json:"tool_permissions"`
}

// Put creates or updates one scope policy; changes take effect immediately.
// The account must be explicit: writing under a guessed id would create a
// policy the runtime can never resolve.
// Put 创建或更新一条作用域策略；变更立即生效。账号必须显式指定：
// 写在猜测的 ID 上会产生运行时永远无法命中的策略。
func (h *Handler) Put(w http.ResponseWriter, r *http.Request) {
	if !h.requireDB(w) {
		return
	}
	botID, ok := h.botID(w, r)
	if !ok {
		return
	}
	t, id, ok := parseScope(r.PathValue("type"), r.PathValue("id"))
	if !ok {
		common.WriteFailure(w, http.StatusBadRequest, "invalid scope type or id")
		return
	}
	var body policyUpdate
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		common.WriteFailure(w, http.StatusBadRequest, "invalid request body")
		return
	}
	// An override for a permission no registered tool declares has no effect at
	// runtime, so reject it instead of storing a misleading policy. A permission
	// that already exists on the stored row is tolerated: removing a provider
	// must not lock the scope out of editing.
	// 没有已注册工具声明的权限覆盖在运行时无效，因此拒绝，而不是存入误导性策略。
	// 已存在于当前策略行的权限予以放行：移除提供方后作用域仍需可编辑。
	if unknown := unknownPermissions(body.ToolPermissions, h.svcCtx.KnownToolPermissions()); len(unknown) > 0 {
		current, err := h.svcCtx.Scope.Get(r.Context(), botID, t, id)
		if err != nil {
			common.WriteFailure(w, http.StatusInternalServerError, "resolve scope failed")
			return
		}
		if stale := rejectablePermissions(unknown, current.ToolPermissions); len(stale) > 0 {
			common.WriteFailure(w, http.StatusBadRequest,
				"unknown tool permissions: "+strings.Join(stale, ", "))
			return
		}
	}
	policy := scopedomain.Policy{
		ScopeType:          t,
		ScopeID:            id,
		Enabled:            body.Enabled,
		ReplyMode:          scopedomain.ReplyMode(body.ReplyMode),
		StoreMessages:      body.StoreMessages,
		AllowMemoryWrite:   body.AllowMemoryWrite,
		AllowHistoryRead:   body.AllowHistoryRead,
		MinReplyIntervalMS: body.MinReplyIntervalMS,
		MaxRepliesPerHour:  body.MaxRepliesPerHour,
		RetentionDays:      body.RetentionDays,
		ToolPermissions:    body.ToolPermissions,
	}
	if err := h.svcCtx.Scope.Upsert(r.Context(), botID, policy); err != nil {
		common.WriteFailure(w, http.StatusBadRequest, "invalid scope policy")
		return
	}
	common.WriteSuccess(w, policy)
}

// Delete removes one stored policy; the scope falls back to default-deny at once.
// Delete 删除一条已存策略；该作用域立即回到默认拒绝。
func (h *Handler) Delete(w http.ResponseWriter, r *http.Request) {
	if !h.requireDB(w) {
		return
	}
	t, id, ok := parseScope(r.PathValue("type"), r.PathValue("id"))
	if !ok {
		common.WriteFailure(w, http.StatusBadRequest, "invalid scope type or id")
		return
	}
	botID, found := h.botID(w, r)
	if !found {
		return
	}
	deleted, err := h.svcCtx.Scope.Delete(r.Context(), botID, t, id)
	if err != nil {
		common.WriteFailure(w, http.StatusInternalServerError, "delete scope failed")
		return
	}
	common.WriteSuccess(w, map[string]any{"deleted": deleted})
}

// unknownPermissions lists overrides that no registered tool declares. A nil
// known set means the tool registry is unavailable, so nothing is validated.
// unknownPermissions 列出没有任何已注册工具声明的覆盖项。
// known 为 nil 表示工具注册表不可用，此时不做校验。
func unknownPermissions(overrides, known map[string]bool) []string {
	if known == nil {
		return nil
	}
	unknown := make([]string, 0)
	for key := range overrides {
		if !known[key] {
			unknown = append(unknown, key)
		}
	}
	sort.Strings(unknown)
	return unknown
}

// rejectablePermissions keeps the unknown keys that are not already stored.
// A permission removed from the tool catalogue stays writable on the existing
// policy, so removing a provider never locks the scope out of editing.
// rejectablePermissions 保留尚未存储过的未知键。
// 已从工具清单消失的权限在现有策略上仍可写入，
// 因此移除提供方不会让该作用域无法编辑。
func rejectablePermissions(unknown []string, stored map[string]bool) []string {
	out := make([]string, 0, len(unknown))
	for _, permission := range unknown {
		if _, existed := stored[permission]; existed {
			continue
		}
		out = append(out, permission)
	}
	return out
}

// parseScope parses and validates a scope type and numeric id.
// parseScope 解析并校验作用域类型与数字 ID。
func parseScope(rawType, rawID string) (scopedomain.ScopeType, int64, bool) {
	t := scopedomain.ScopeType(rawType)
	if !t.Valid() {
		return "", 0, false
	}
	id, err := strconv.ParseInt(rawID, 10, 64)
	if err != nil || id <= 0 {
		return "", 0, false
	}
	return t, id, true
}
