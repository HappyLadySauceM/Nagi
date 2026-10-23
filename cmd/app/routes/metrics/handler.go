// Package metrics serves aggregate runtime metrics.
// Package metrics 提供聚合运行指标接口。
package metrics

import (
	"context"
	"net/http"

	"github.com/HappyLadySauceM/QQBot/cmd/app/svc"
	"github.com/HappyLadySauceM/QQBot/cmd/app/types/common"
)

// StatsProvider supplies aggregate counters; it never exposes message content.
// StatsProvider 提供聚合计数；绝不暴露消息内容。
type StatsProvider interface {
	Stats(ctx context.Context) (map[string]any, error)
}

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
	stats, err := h.svcCtx.Stats.Stats(r.Context())
	if err != nil {
		common.WriteFailure(w, http.StatusInternalServerError, "collect metrics failed")
		return
	}
	common.WriteSuccess(w, stats)
}
