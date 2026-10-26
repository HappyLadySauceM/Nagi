// Package memory serves long-term memory management endpoints.
// Package memory 提供长期记忆管理接口。
package memory

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/HappyLadySauceM/Nagi/cmd/app/middleware"
	"github.com/HappyLadySauceM/Nagi/cmd/app/svc"
	"github.com/HappyLadySauceM/Nagi/cmd/app/types/common"
	memoryapp "github.com/HappyLadySauceM/Nagi/internal/memory/application"
	memorydomain "github.com/HappyLadySauceM/Nagi/internal/memory/domain"
)

// middlewareActor reads the authenticated actor for approval records.
// middlewareActor 读取已认证身份，用于审批记录。
func middlewareActor(r *http.Request) string {
	return middleware.ActorFrom(r.Context())
}

// Handler serves the memory management endpoints.
// Handler 提供记忆管理接口。
type Handler struct {
	svcCtx *svc.ServiceContext
}

// NewHandler builds the memory handler.
// NewHandler 构建记忆处理器。
func NewHandler(svcCtx *svc.ServiceContext) *Handler {
	return &Handler{svcCtx: svcCtx}
}

// Register wires routes using Go 1.22 method+path patterns.
// Register 以 Go 1.22 的方法+路径模式注册路由。
func (h *Handler) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/memories", h.List)
	mux.HandleFunc("POST /api/v1/memories", h.Create)
	mux.HandleFunc("PATCH /api/v1/memories/{id}", h.Patch)
	mux.HandleFunc("DELETE /api/v1/memories/{id}", h.Delete)
}

// requireDB replies with 503 when the database is not configured.
// requireDB 在未配置数据库时返回 503。
func (h *Handler) requireDB(w http.ResponseWriter) bool {
	if h.svcCtx.Memory == nil {
		common.WriteFailure(w, http.StatusServiceUnavailable, "database not configured")
		return false
	}
	return true
}

// List returns one page of memories filtered by visibility, owner, status or search.
// List 返回按可见性、归属、状态或搜索过滤的一页记忆。
func (h *Handler) List(w http.ResponseWriter, r *http.Request) {
	if !h.requireDB(w) {
		return
	}
	q := r.URL.Query()
	f := memorydomain.ListFilter{
		Visibility: memorydomain.Visibility(q.Get("visibility")),
		OwnerKey:   q.Get("owner_key"),
		Status:     memorydomain.Status(q.Get("status")),
		Search:     q.Get("search"),
	}
	if raw := q.Get("bot_id"); raw != "" {
		if v, err := strconv.ParseInt(raw, 10, 64); err == nil {
			f.BotID = &v
		}
	}
	f.Page, _ = strconv.Atoi(q.Get("page"))
	f.Limit, _ = strconv.Atoi(q.Get("limit"))
	items, total, err := h.svcCtx.Memory.List(r.Context(), f)
	if err != nil {
		common.WriteFailure(w, http.StatusInternalServerError, "list memories failed")
		return
	}
	common.WriteSuccess(w, map[string]any{"items": items, "total": total, "page": f.Page, "limit": f.Limit})
}

// createRequest is the POST body; approve=true activates the memory immediately.
// createRequest 是 POST 请求体；approve=true 时立即激活记忆。
type createRequest struct {
	BotID      int64   `json:"bot_id"`
	Visibility string  `json:"visibility"`
	OwnerKey   string  `json:"owner_key"`
	Content    string  `json:"content"`
	Source     string  `json:"source"`
	Confidence float64 `json:"confidence"`
	Approve    bool    `json:"approve"`
}

// Create stores a new memory, optionally approved by the acting administrator.
// Create 存储新记忆，可由当前操作的管理员直接批准。
func (h *Handler) Create(w http.ResponseWriter, r *http.Request) {
	if !h.requireDB(w) {
		return
	}
	var body createRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		common.WriteFailure(w, http.StatusBadRequest, "invalid request body")
		return
	}
	id, duplicate, err := h.svcCtx.Memory.Propose(r.Context(), memoryapp.CandidateInput{
		BotID:      body.BotID,
		Visibility: memorydomain.Visibility(body.Visibility),
		OwnerKey:   body.OwnerKey,
		Content:    body.Content,
		Source:     body.Source,
		Confidence: body.Confidence,
	})
	if err != nil {
		common.WriteFailure(w, http.StatusBadRequest, "invalid memory")
		return
	}
	if body.Approve && !duplicate {
		if err := h.svcCtx.Memory.Approve(r.Context(), id, middlewareActor(r)); err != nil {
			common.WriteFailure(w, http.StatusInternalServerError, "approve memory failed")
			return
		}
	}
	common.WriteSuccess(w, map[string]any{"id": id, "duplicate": duplicate})
}

// patchRequest is the PATCH body; empty fields keep current values.
// patchRequest 是 PATCH 请求体；空字段保持当前值。
type patchRequest struct {
	Content    *string `json:"content"`
	Visibility string  `json:"visibility"`
	OwnerKey   *string `json:"owner_key"`
	Status     string  `json:"status"`
}

// statusAction is the effect of the PATCH status field on one memory.
// statusAction 表示 PATCH 的 status 字段对一条记忆产生的效果。
type statusAction int

const (
	statusNone statusAction = iota
	statusApprove
	statusDelete
)

// parseStatusAction validates the requested transition. Only "" (keep the
// current status), "active" (approve) and "deleted" (soft delete) are supported:
// the management API cannot move a memory back to candidate or superseded, and
// silently accepting those values would report success without any effect.
// parseStatusAction 校验状态变更：只支持 ""（保持不变）、active（审批）与 deleted（软删除）。
// 管理接口无法把记忆改回 candidate 或 superseded，静默接受会谎报成功。
func parseStatusAction(status string) (statusAction, bool) {
	switch status {
	case "":
		return statusNone, true
	case "active":
		return statusApprove, true
	case "deleted":
		return statusDelete, true
	default:
		return statusNone, false
	}
}

// Patch revises one memory and optionally applies a status transition. The
// status is validated before any write so an unsupported transition cannot
// leave a half-applied edit behind.
// Patch 修订一条记忆并可应用状态变更；状态在任何写入前先校验，
// 避免不支持的变更留下「内容已改、状态未变」的半成品。
func (h *Handler) Patch(w http.ResponseWriter, r *http.Request) {
	if !h.requireDB(w) {
		return
	}
	id := r.PathValue("id")
	var body patchRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		common.WriteFailure(w, http.StatusBadRequest, "invalid request body")
		return
	}
	action, ok := parseStatusAction(body.Status)
	if !ok {
		common.WriteFailure(w, http.StatusBadRequest, "unsupported status; use active or deleted")
		return
	}
	var content string
	if body.Content != nil {
		content = *body.Content
	}
	var ownerKey *string
	if body.OwnerKey != nil {
		ownerKey = body.OwnerKey
	}
	if err := h.svcCtx.Memory.Revise(r.Context(), id, content, memorydomain.Visibility(body.Visibility), ownerKey); err != nil {
		common.WriteFailure(w, http.StatusBadRequest, "revise memory failed")
		return
	}
	switch action {
	case statusApprove:
		if err := h.svcCtx.Memory.Approve(r.Context(), id, middlewareActor(r)); err != nil {
			common.WriteFailure(w, http.StatusInternalServerError, "approve memory failed")
			return
		}
	case statusDelete:
		if err := h.svcCtx.Memory.Delete(r.Context(), id); err != nil {
			common.WriteFailure(w, http.StatusInternalServerError, "delete memory failed")
			return
		}
	case statusNone:
	}
	common.WriteSuccess(w, map[string]any{"id": id})
}

// Delete removes one memory from retrieval.
// Delete 将一条记忆从检索中移除。
func (h *Handler) Delete(w http.ResponseWriter, r *http.Request) {
	if !h.requireDB(w) {
		return
	}
	if err := h.svcCtx.Memory.Delete(r.Context(), r.PathValue("id")); err != nil {
		common.WriteFailure(w, http.StatusInternalServerError, "delete memory failed")
		return
	}
	common.WriteSuccess(w, map[string]any{"deleted": true})
}
