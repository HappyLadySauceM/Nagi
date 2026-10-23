// Admin bearer-token authentication for the management API.
// 管理 API 的 Bearer Token 管理员认证。
package middleware

import (
	"context"
	"crypto/subtle"
	"net/http"
	"strings"
	"sync"

	"github.com/HappyLadySauceM/QQBot/cmd/app/types/common"
)

type actorKey struct{}

// actorBox lets an outer middleware read the actor assigned by an inner Auth.
// actorBox 允许外层中间件在内层 Auth 执行完成后读取其分配的身份。
type actorBox struct {
	mu    sync.Mutex
	actor string
}

type actorBoxKey struct{}

// healthProbes are unauthenticated liveness endpoints.
// healthProbes 为免认证的探活端点。
var healthProbes = map[string]bool{"/livez": true, "/readyz": true}

// isHealthProbe reports whether the request targets an exempted GET probe.
// isHealthProbe 判断请求是否为豁免的探活 GET 请求。
func isHealthProbe(r *http.Request) bool {
	return r.Method == http.MethodGet && healthProbes[r.URL.Path]
}

// Auth guards routes with a static bearer token. When no token is configured the
// API keeps working for loopback-only setups without authentication; non-loopback
// listeners with an empty token are rejected at config validation time.
// Auth 以静态 Bearer Token 保护路由。未配置 Token 时仅对回环监听保持无鉴权可用；
// 非回环监听且空 Token 的组合在配置校验阶段即拒绝启动。
func Auth(adminToken string) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if isHealthProbe(r) {
				next.ServeHTTP(w, r)
				return
			}
			if adminToken == "" {
				next.ServeHTTP(w, withActor(r, "local"))
				return
			}
			const prefix = "Bearer "
			got, ok := strings.CutPrefix(r.Header.Get("Authorization"), prefix)
			if !ok || subtle.ConstantTimeCompare([]byte(got), []byte(adminToken)) != 1 {
				common.WriteFailure(w, http.StatusUnauthorized, "unauthorized")
				return
			}
			next.ServeHTTP(w, withActor(r, "admin"))
		})
	}
}

// ActorFrom reads the identity assigned by the Auth middleware.
// ActorFrom 读取 Auth 中间件写入的身份。
func ActorFrom(ctx context.Context) string {
	actor, _ := ctx.Value(actorKey{}).(string)
	if actor == "" {
		return "unknown"
	}
	return actor
}

// withActor records the identity in the request context and in any outer
// actorBox installed by wrapping middleware.
// withActor 将身份写入请求上下文，并同步到外层中间件安装的 actorBox。
func withActor(r *http.Request, actor string) *http.Request {
	if box, ok := r.Context().Value(actorBoxKey{}).(*actorBox); ok && box != nil {
		box.mu.Lock()
		box.actor = actor
		box.mu.Unlock()
	}
	return r.WithContext(context.WithValue(r.Context(), actorKey{}, actor))
}
