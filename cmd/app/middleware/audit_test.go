// Tests for the admin audit middleware chain ordering and 401 auditing.
// 审计中间件链序与 401 审计的测试。
package middleware

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	auditdomain "github.com/HappyLadySauceM/QQBot/internal/audit/domain"
)

// discardLogger returns a logger writing nowhere, for middleware tests.
// discardLogger 返回丢弃输出的日志器，供中间件测试使用。
func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

type fakeRecorder struct {
	entries []auditdomain.Entry
}

func (f *fakeRecorder) Record(_ context.Context, entry auditdomain.Entry) error {
	f.entries = append(f.entries, entry)
	return nil
}

// TestAuditOutermostRecords401 verifies that 401 attempts leave audit entries and
// that successful requests carry the admin identity assigned by the inner Auth.
// TestAuditOutermostRecords401 验证 401 尝试会产生审计记录，且成功请求携带内层
// Auth 分配的 admin 身份。
func TestAuditOutermostRecords401(t *testing.T) {
	rec := &fakeRecorder{}
	handler := Audit(rec, discardLogger())(Auth("secret")(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		})))

	// Unauthorized mutating attempt must be audited with an unknown actor.
	req := httptest.NewRequest(http.MethodPost, "/api/v1/scopes", nil)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusUnauthorized {
		t.Fatalf("status=%d, want 401", res.Code)
	}
	if len(rec.entries) != 1 || rec.entries[0].Action != "POST" || rec.entries[0].Resource != "/api/v1/scopes" {
		t.Fatalf("unexpected 401 audit entries: %+v", rec.entries)
	}

	// Authorized mutating request carries the admin actor.
	rec.entries = nil
	req = httptest.NewRequest(http.MethodPost, "/api/v1/scopes", nil)
	req.Header.Set("Authorization", "Bearer secret")
	res = httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusOK || len(rec.entries) != 1 || rec.entries[0].Actor != "admin" {
		t.Fatalf("status=%d entries=%+v", res.Code, rec.entries)
	}

	// Authorized GETs and exempted health probes are not audited.
	rec.entries = nil
	getStatus := httptest.NewRequest(http.MethodGet, "/api/v1/status", nil)
	getStatus.Header.Set("Authorization", "Bearer secret")
	handler.ServeHTTP(httptest.NewRecorder(), getStatus)
	handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/livez", nil))
	if len(rec.entries) != 0 {
		t.Fatalf("authorized GET and health probe must not be audited: %+v", rec.entries)
	}
}

// TestAuditUnauthorizedGETIsRecorded verifies that even read attempts are audited
// when they are rejected with 401.
// TestAuditUnauthorizedGETIsRecorded 验证读取类请求被 401 拒绝时同样入审计。
func TestAuditUnauthorizedGETIsRecorded(t *testing.T) {
	rec := &fakeRecorder{}
	handler := Audit(rec, discardLogger())(Auth("secret")(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})))
	handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/api/v1/messages", nil))
	if len(rec.entries) != 1 || rec.entries[0].Resource != "/api/v1/messages" {
		t.Fatalf("unexpected entries: %+v", rec.entries)
	}
}
