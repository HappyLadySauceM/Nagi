// Package router assembles the management API routes and middleware chain.
// Package router 组装管理 API 的路由与中间件链。
package router

import (
	"log/slog"
	"net/http"

	"github.com/HappyLadySauceM/QQBot/cmd/app/middleware"
	"github.com/HappyLadySauceM/QQBot/cmd/app/routes/system"
	"github.com/HappyLadySauceM/QQBot/cmd/app/svc"
)

// New builds the full management API handler chain.
// New 构建完整的管理 API 处理器链。
func New(svcCtx *svc.ServiceContext, logger *slog.Logger) http.Handler {
	mux := http.NewServeMux()
	system.NewHandler(svcCtx).Register(mux)
	return middleware.Chain(mux,
		middleware.SecurityHeaders,
		middleware.RequestID,
		middleware.AccessLog(logger),
		middleware.Recover(logger),
	)
}
