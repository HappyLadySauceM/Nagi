// Package system provides health checks, the runtime status endpoint and the
// account snapshot that stays available without a database.
// Package system 提供健康检查、运行状态接口，以及不依赖数据库的账号快照。
package system

import (
	"net/http"

	"github.com/HappyLadySauceM/QQBot/cmd/app/svc"
	"github.com/HappyLadySauceM/QQBot/cmd/app/types/common"
)

// Handler serves /livez, /readyz, /api/v1/status and /api/v1/accounts.
// Handler 服务于 /livez、/readyz、/api/v1/status 与 /api/v1/accounts。
type Handler struct {
	svcCtx *svc.ServiceContext
}

// NewHandler builds the system handler.
// NewHandler 构建系统接口处理器。
func NewHandler(svcCtx *svc.ServiceContext) *Handler {
	return &Handler{svcCtx: svcCtx}
}

// Register wires routes using Go 1.22 method+path patterns.
// Register 以 Go 1.22 的方法+路径模式注册路由。
func (h *Handler) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /livez", h.Livez)
	mux.HandleFunc("GET /readyz", h.Readyz)
	mux.HandleFunc("GET /api/v1/status", h.Status)
	mux.HandleFunc("GET /api/v1/accounts", h.Accounts)
}

// Livez reports process liveness without touching external dependencies.
// Livez 报告进程存活，不检查外部依赖。
func (h *Handler) Livez(w http.ResponseWriter, r *http.Request) {
	common.WriteSuccess(w, map[string]string{"status": "alive"})
}

// Readyz reports whether the OneBot connection is ready.
// Readyz 报告 OneBot 连接是否就绪；未就绪返回 503。
func (h *Handler) Readyz(w http.ResponseWriter, r *http.Request) {
	if h.svcCtx.Onebot == nil || !h.svcCtx.Onebot.Connected() {
		common.WriteFailure(w, http.StatusServiceUnavailable, "onebot connection not ready")
		return
	}
	common.WriteSuccess(w, map[string]string{"status": "ready"})
}

// Status returns the runtime status snapshot.
// Status 返回运行状态快照。
func (h *Handler) Status(w http.ResponseWriter, r *http.Request) {
	common.WriteSuccess(w, h.svcCtx.StatusSnapshot())
}

// Accounts reports the bot account and connection state. It only reads the
// OneBot connection, so it must stay available without a database.
// Accounts 报告机器人账号与连接状态；它只读取 OneBot 连接，
// 因此在未配置数据库时也必须可用。
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
