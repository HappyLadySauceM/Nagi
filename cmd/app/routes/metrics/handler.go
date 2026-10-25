// Package metrics serves aggregate runtime metrics.
// Package metrics 提供聚合运行指标接口。
package metrics

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/HappyLadySauceM/QQBot/cmd/app/svc"
	"github.com/HappyLadySauceM/QQBot/cmd/app/types/common"
)

// Handler serves the metrics endpoint.
// Handler 提供指标接口。
type Handler struct {
	svcCtx *svc.ServiceContext
}

// NewHandler builds the metrics handler.
// NewHandler 构建指标处理器。
func NewHandler(svcCtx *svc.ServiceContext) *Handler {
	return &Handler{svcCtx: svcCtx}
}

// Register wires routes using Go 1.22 method+path patterns.
// Register 以 Go 1.22 的方法+路径模式注册路由。
func (h *Handler) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/metrics", h.Metrics)
}

// Metrics returns aggregate counters for monitoring and evaluation.
// Metrics 返回用于监控与评估的聚合计数。
func (h *Handler) Metrics(w http.ResponseWriter, r *http.Request) {
	if h.svcCtx.Stats == nil {
		common.WriteFailure(w, http.StatusServiceUnavailable, "database not configured")
		return
	}
	// bot_id 0 aggregates every account; the dashboard always sends the selected one.
	// Malformed ids fail loudly instead of silently widening to all accounts.
	// bot_id 为 0 时汇总全部账号；看板始终发送当前选中账号。
	// 非法 bot_id 直接报错，而不是静默放大为全账号聚合。
	var botID int64
	if raw := strings.TrimSpace(r.URL.Query().Get("bot_id")); raw != "" {
		parsed, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || parsed < 0 {
			common.WriteFailure(w, http.StatusBadRequest, "invalid bot_id")
			return
		}
		botID = parsed
	}
	stats, err := h.svcCtx.Stats.Stats(r.Context(), botID)
	if err != nil {
		common.WriteFailure(w, http.StatusInternalServerError, "collect metrics failed")
		return
	}
	common.WriteSuccess(w, stats)
}
