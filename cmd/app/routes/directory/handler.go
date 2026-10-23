// Package directory serves directory management endpoints.
// Package directory 提供目录管理接口。
package directory

import (
	"context"
	"net/http"
	"strconv"
	"time"

	"github.com/HappyLadySauceM/QQBot/cmd/app/svc"
	"github.com/HappyLadySauceM/QQBot/cmd/app/types/common"
	"github.com/HappyLadySauceM/QQBot/internal/directory/domain"
)

// Handler serves the directory sync and listing endpoints.
// Handler 提供目录同步与列表查询接口。
type Handler struct {
	svcCtx *svc.ServiceContext
}

// NewHandler builds the directory handler.
// NewHandler 构建目录处理器。
func NewHandler(svcCtx *svc.ServiceContext) *Handler {
	return &Handler{svcCtx: svcCtx}
}

// Register wires routes using Go 1.22 method+path patterns.
// Register 以 Go 1.22 的方法+路径模式注册路由。
func (h *Handler) Register(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/v1/directory/sync", h.Sync)
	mux.HandleFunc("GET /api/v1/accounts", h.Accounts)
	mux.HandleFunc("GET /api/v1/contacts", h.Contacts)
	mux.HandleFunc("GET /api/v1/groups", h.Groups)
	mux.HandleFunc("GET /api/v1/groups/{id}/members", h.GroupMembers)
}

// requireDB replies with 503 when the database is not configured.
// requireDB 在未配置数据库时返回 503。
func (h *Handler) requireDB(w http.ResponseWriter) bool {
	if h.svcCtx.Directory == nil {
		common.WriteFailure(w, http.StatusServiceUnavailable, "database not configured")
		return false
	}
	return true
}

// Sync refreshes the friend and group directory from the connected OneBot implementation.
// Sync 从已连接的 OneBot 实现刷新好友与群目录。
func (h *Handler) Sync(w http.ResponseWriter, r *http.Request) {
	if !h.requireDB(w) {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	result, err := h.svcCtx.Directory.Sync(ctx)
	if err != nil {
		common.WriteFailure(w, http.StatusBadGateway, "directory sync failed; ensure the bot is connected")
		return
	}
	common.WriteSuccess(w, result)
}

// Accounts reports the bot account and connection state.
// Accounts 报告机器人账号与连接状态。
func (h *Handler) Accounts(w http.ResponseWriter, r *http.Request) {
	status := h.svcCtx.Onebot.Status()
	account := map[string]any{
		"connected":     status.Connected,
		"db_configured": h.svcCtx.Directory != nil,
	}
	if status.SelfIDKnown {
		account["bot_id"] = int64(status.SelfID)
	}
	common.WriteSuccess(w, account)
}

// Contacts returns one page of the friend directory.
// Contacts 返回一页好友目录。
func (h *Handler) Contacts(w http.ResponseWriter, r *http.Request) {
	if !h.requireDB(w) {
		return
	}
	page, err := h.svcCtx.Directory.Contacts(r.Context(), pageQuery(r))
	if err != nil {
		common.WriteFailure(w, http.StatusInternalServerError, "list contacts failed")
		return
	}
	common.WriteSuccess(w, page)
}

// Groups returns one page of the group directory.
// Groups 返回一页群目录。
func (h *Handler) Groups(w http.ResponseWriter, r *http.Request) {
	if !h.requireDB(w) {
		return
	}
	page, err := h.svcCtx.Directory.Groups(r.Context(), pageQuery(r))
	if err != nil {
		common.WriteFailure(w, http.StatusInternalServerError, "list groups failed")
		return
	}
	common.WriteSuccess(w, page)
}

// GroupMembers returns one page of group members, optionally refreshed on demand.
// GroupMembers 返回一页群成员，可按需触发刷新。
func (h *Handler) GroupMembers(w http.ResponseWriter, r *http.Request) {
	if !h.requireDB(w) {
		return
	}
	groupID, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || groupID <= 0 {
		common.WriteFailure(w, http.StatusBadRequest, "invalid group id")
		return
	}
	refresh, _ := strconv.ParseBool(r.URL.Query().Get("refresh"))
	ctx := r.Context()
	if refresh {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
	}
	page, err := h.svcCtx.Directory.GroupMembers(ctx, groupID, pageQuery(r), refresh)
	if err != nil {
		common.WriteFailure(w, http.StatusInternalServerError, "list group members failed")
		return
	}
	common.WriteSuccess(w, page)
}

// pageQuery parses the shared search/page/limit parameters.
// pageQuery 解析共用的 search/page/limit 参数。
func pageQuery(r *http.Request) domain.PageQuery {
	q := r.URL.Query()
	page, _ := strconv.Atoi(q.Get("page"))
	limit, _ := strconv.Atoi(q.Get("limit"))
	return domain.PageQuery{Search: q.Get("search"), Page: page, Limit: limit}
}
