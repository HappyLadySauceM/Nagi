// Package router assembles the management API routes and middleware chain.
// Package router 组装管理 API 的路由与中间件链。
package router

import (
	"log/slog"
	"net/http"

	"github.com/HappyLadySauceM/Nagi/cmd/app/middleware"
	"github.com/HappyLadySauceM/Nagi/cmd/app/routes/directory"
	intentroutes "github.com/HappyLadySauceM/Nagi/cmd/app/routes/intent"
	memoryroutes "github.com/HappyLadySauceM/Nagi/cmd/app/routes/memory"
	metricsroutes "github.com/HappyLadySauceM/Nagi/cmd/app/routes/metrics"
	"github.com/HappyLadySauceM/Nagi/cmd/app/routes/scope"
	"github.com/HappyLadySauceM/Nagi/cmd/app/routes/system"
	toolsroutes "github.com/HappyLadySauceM/Nagi/cmd/app/routes/tools"
	"github.com/HappyLadySauceM/Nagi/cmd/app/svc"
)

// New builds the full management API handler chain.
// New 构建完整的管理 API 处理器链。
func New(svcCtx *svc.ServiceContext, logger *slog.Logger) http.Handler {
	mux := http.NewServeMux()
	system.NewHandler(svcCtx).Register(mux)
	// Database-backed management routes only exist with a configured database.
	// 依赖数据库的管理路由仅在已配置数据库时注册。
	if svcCtx.Directory != nil {
		directory.NewHandler(svcCtx).Register(mux)
	}
	if svcCtx.Scope != nil {
		scope.NewHandler(svcCtx).Register(mux)
	}
	if svcCtx.Memory != nil {
		memoryroutes.NewHandler(svcCtx).Register(mux)
	}
	if svcCtx.Stats != nil {
		metricsroutes.NewHandler(svcCtx).Register(mux)
	}
	if svcCtx.Tools != nil {
		toolsroutes.NewHandler(svcCtx).Register(mux)
	}
	if svcCtx.Decisions != nil {
		intentroutes.NewHandler(svcCtx).Register(mux)
	}
	return middleware.Chain(mux,
		middleware.SecurityHeaders,
		middleware.RequestID,
		middleware.AccessLog(logger),
		// Audit wraps Auth so rejected (401) attempts are audited too.
		// Audit 位于 Auth 外层，使 401 拒绝尝试也进入审计。
		middleware.Audit(svcCtx.Audit, logger),
		middleware.Auth(svcCtx.Config.Server.AdminToken),
		middleware.Recover(logger),
	)
}
