// Admin audit middleware recording mutating management API calls.
// 记录管理 API 变更操作的审计中间件。
package middleware

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/HappyLadySauceM/Nagi/internal/audit/domain"
)

// AuditRecorder persists audit entries; nil disables recording.
// AuditRecorder 持久化审计条目；为 nil 时关闭记录。
type AuditRecorder interface {
	Record(ctx context.Context, entry domain.Entry) error
}

// Audit records mutating requests (POST/PUT/PATCH/DELETE) and any rejected
// (401) request after completion. It installs an actorBox so that an inner
// Auth middleware's identity is visible here even when Audit runs outermost,
// which keeps 401 attempts auditable.
// Audit 在请求完成后记录变更类请求（POST/PUT/PATCH/DELETE）以及所有被拒绝
// （401）的请求。它安装 actorBox，使内层 Auth 分配的身份在本层可见，
// 从而即使 Audit 位于最外层也能审计 401 尝试。
//
// Audit failures only log a warning; they never fail the request they record.
// 审计失败仅记录告警；绝不因此使被记录的请求失败。
func Audit(rec AuditRecorder, logger *slog.Logger) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			box := &actorBox{}
			rec2 := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
			next.ServeHTTP(rec2, r.WithContext(context.WithValue(r.Context(), actorBoxKey{}, box)))

			if rec == nil || !shouldAudit(r.Method, rec2.status) {
				return
			}
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			entry := domain.Entry{
				Actor:    boxActor(box, r),
				Action:   r.Method,
				Resource: r.URL.Path,
			}
			if err := rec.Record(ctx, entry); err != nil {
				logger.Warn("admin audit record failed",
					"method", r.Method, "path", r.URL.Path, "error", err)
			}
		})
	}
}

// shouldAudit reports whether a completed request leaves an audit trail:
// every mutating method, plus rejected (401) requests of any method.
// shouldAudit 判断请求是否需要审计：所有变更方法，以及任何方法上的 401 拒绝。
func shouldAudit(method string, status int) bool {
	if status == http.StatusUnauthorized {
		return true
	}
	return mutating(method)
}

// boxActor prefers the identity assigned by the inner Auth middleware.
// boxActor 优先取内层 Auth 中间件分配的身份。
func boxActor(box *actorBox, r *http.Request) string {
	if box != nil {
		box.mu.Lock()
		actor := box.actor
		box.mu.Unlock()
		if actor != "" {
			return actor
		}
	}
	return ActorFrom(r.Context())
}

func mutating(method string) bool {
	switch method {
	case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
		return true
	}
	return false
}
