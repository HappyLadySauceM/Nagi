// Package directory serves directory management endpoints.
// Package directory 提供目录管理接口。
package directory

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/HappyLadySauceM/Nagi/cmd/app/svc"
	"github.com/HappyLadySauceM/Nagi/cmd/app/types/common"
	directoryapp "github.com/HappyLadySauceM/Nagi/internal/directory/application"
	"github.com/HappyLadySauceM/Nagi/internal/directory/domain"
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

// Register wires routes using Go 1.22 method+path patterns. Every listing needs
// an explicit account once more than one is configured.
// Register 以 Go 1.22 的方法+路径模式注册路由。配置多个账号后每个列表都必须显式指定账号。
func (h *Handler) Register(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/v1/accounts/{bot_id}/sync", h.Sync)
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

// botID resolves the request account from the path, query or single-account default.
// botID 从路径、查询参数或单账号默认值解析请求所属账号。
func (h *Handler) botID(w http.ResponseWriter, r *http.Request, fromPath bool) (int64, bool) {
	raw := r.URL.Query().Get("bot_id")
	if fromPath {
		raw = r.PathValue("bot_id")
	}
	botID, err := h.svcCtx.ResolveBotID(raw)
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

// Sync refreshes the friend and group directory of one account.
// Sync 刷新指定账号的好友与群目录。
func (h *Handler) Sync(w http.ResponseWriter, r *http.Request) {
	if !h.requireDB(w) {
		return
	}
	botID, ok := h.botID(w, r, true)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	result, err := h.svcCtx.Directory.Sync(ctx, botID)
	if err != nil {
		if errors.Is(err, directoryapp.ErrAccountOffline) {
			common.WriteFailure(w, http.StatusConflict, "bot account is not connected")
			return
		}
		common.WriteFailure(w, http.StatusBadGateway, "directory sync failed")
		return
	}
	common.WriteSuccess(w, result)
}

// Contacts returns one page of the account's friend directory.
// Contacts 返回该账号的一页好友目录。
func (h *Handler) Contacts(w http.ResponseWriter, r *http.Request) {
	if !h.requireDB(w) {
		return
	}
	botID, ok := h.botID(w, r, false)
	if !ok {
		return
	}
	page, err := h.svcCtx.Directory.Contacts(r.Context(), botID, pageQuery(r))
	if err != nil {
		common.WriteFailure(w, http.StatusInternalServerError, "list contacts failed")
		return
	}
	common.WriteSuccess(w, page)
}

// Groups returns one page of the account's group directory.
// Groups 返回该账号的一页群目录。
func (h *Handler) Groups(w http.ResponseWriter, r *http.Request) {
	if !h.requireDB(w) {
		return
	}
	botID, ok := h.botID(w, r, false)
	if !ok {
		return
	}
	page, err := h.svcCtx.Directory.Groups(r.Context(), botID, pageQuery(r))
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
	botID, ok := h.botID(w, r, false)
	if !ok {
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
	page, err := h.svcCtx.Directory.GroupMembers(ctx, botID, groupID, pageQuery(r), refresh)
	if err != nil {
		// Refreshing needs a synced account row to attach member rows to.
		// 刷新成员需要一个已同步的账号行来关联成员数据。
		if errors.Is(err, directoryapp.ErrBotNotSynced) {
			common.WriteFailure(w, http.StatusConflict, "bot account not synced yet; run directory sync first")
			return
		}
		if errors.Is(err, directoryapp.ErrAccountOffline) {
			common.WriteFailure(w, http.StatusConflict, "bot account is not connected")
			return
		}
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
