// Package settings serves the runtime-editable configuration endpoints.
// Package settings 提供运行时可编辑配置的管理接口。
package settings

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/HappyLadySauceM/Nagi/cmd/app/middleware"
	"github.com/HappyLadySauceM/Nagi/cmd/app/svc"
	"github.com/HappyLadySauceM/Nagi/cmd/app/types/common"
	settingsapp "github.com/HappyLadySauceM/Nagi/internal/settings/application"
	settingsdomain "github.com/HappyLadySauceM/Nagi/internal/settings/domain"
)

// maxRequestBytes bounds one settings payload; the model is small and a huge
// body would only be an attack surface.
// maxRequestBytes 限制单次配置载荷；模型很小，超大请求体只会成为攻击面。
const maxRequestBytes = 1 << 20

// Handler serves GET/PUT /api/v1/settings and the reset endpoint.
// Handler 提供 GET/PUT /api/v1/settings 与重置接口。
type Handler struct {
	svcCtx *svc.ServiceContext
}

// NewHandler builds the settings handler.
// NewHandler 构建设置处理器。
func NewHandler(svcCtx *svc.ServiceContext) *Handler {
	return &Handler{svcCtx: svcCtx}
}

// Register wires routes using Go 1.22 method+path patterns.
// Register 以 Go 1.22 的方法+路径模式注册路由。
func (h *Handler) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/settings", h.Get)
	mux.HandleFunc("PUT /api/v1/settings", h.Put)
	mux.HandleFunc("POST /api/v1/settings/reset", h.Reset)
}

// updateRequest is the PUT body: the full model plus the revision the client
// edited, so a concurrent save is detected instead of silently overwritten.
// updateRequest 是 PUT 请求体：完整模型加上客户端编辑时的修订号，
// 并发保存会被发现而不是被静默覆盖。
type updateRequest struct {
	Settings settingsdomain.Settings `json:"Settings"`
	Version  int64                   `json:"Version"`
}

// Get returns the current revision, the baseline and the credential state.
// Get 返回当前修订、默认基线与凭据状态。
func (h *Handler) Get(w http.ResponseWriter, r *http.Request) {
	if !h.requireService(w) {
		return
	}
	common.WriteSuccess(w, h.svcCtx.Settings.View())
}

// Put validates and persists a new revision; the response is the stored one.
// Put 校验并持久化新修订；响应返回已存储的修订。
func (h *Handler) Put(w http.ResponseWriter, r *http.Request) {
	if !h.requireService(w) {
		return
	}
	var body updateRequest
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxRequestBytes))
	if err := decoder.Decode(&body); err != nil {
		common.WriteFailure(w, http.StatusBadRequest, "请求体不是合法的 JSON")
		return
	}
	h.apply(w, r, body.Settings, body.Version)
}

// Reset restores the configuration-file baseline as a new revision.
// Reset 把配置文件基线作为新修订恢复。
func (h *Handler) Reset(w http.ResponseWriter, r *http.Request) {
	if !h.requireService(w) {
		return
	}
	view, err := h.svcCtx.Settings.Reset(r.Context(), middleware.ActorFrom(r.Context()))
	if err != nil {
		h.writeError(w, err)
		return
	}
	common.WriteSuccess(w, view)
}

func (h *Handler) apply(w http.ResponseWriter, r *http.Request, settings settingsdomain.Settings, version int64) {
	view, err := h.svcCtx.Settings.Update(r.Context(), settings, version, middleware.ActorFrom(r.Context()))
	if err != nil {
		h.writeError(w, err)
		return
	}
	common.WriteSuccess(w, view)
}

// writeError maps domain failures onto the documented status codes.
// writeError 把领域错误映射到约定的状态码。
func (h *Handler) writeError(w http.ResponseWriter, err error) {
	var invalid *settingsdomain.ValidationError
	switch {
	case errors.As(err, &invalid):
		common.WriteJSON(w, http.StatusBadRequest, common.Envelope{
			Code:    http.StatusBadRequest,
			Message: "配置校验未通过",
			Data:    map[string]any{"errors": invalid.Fields},
		})
	case errors.Is(err, settingsapp.ErrConflict):
		common.WriteFailure(w, http.StatusConflict, "配置已被其他页面修改，请刷新后重试")
	default:
		h.svcCtx.Logger.Error("settings update failed", "error", err)
		common.WriteFailure(w, http.StatusInternalServerError, "保存配置失败")
	}
}

func (h *Handler) requireService(w http.ResponseWriter) bool {
	if h.svcCtx.Settings == nil {
		common.WriteFailure(w, http.StatusServiceUnavailable, "database not configured")
		return false
	}
	return true
}
