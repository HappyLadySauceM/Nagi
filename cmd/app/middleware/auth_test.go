// Tests for the admin bearer-token authentication middleware.
// 管理 Bearer Token 认证中间件的测试。
package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// probeHandler records whether it ran and the actor it received.
// probeHandler 记录是否被调用及其收到的身份。
type probeHandler struct {
	called bool
	actor  string
}

func (p *probeHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	p.called = true
	p.actor = ActorFrom(r.Context())
}

// TestAuthRejectsMissingOrWrongToken verifies 401 responses without or with a wrong
// bearer token when auth is configured.
// TestAuthRejectsMissingOrWrongToken 验证配置鉴权后缺少或错误的 Token 返回 401。
func TestAuthRejectsMissingOrWrongToken(t *testing.T) {
	for _, token := range []string{"", "Bearer wrong"} {
		probe := &probeHandler{}
		srv := Auth("secret")(probe)
		req := httptest.NewRequest(http.MethodGet, "/api/v1/scopes", nil)
		req.Header.Set("Authorization", token)
		rec := httptest.NewRecorder()
		srv.ServeHTTP(rec, req)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("token %q: status = %d, want 401", token, rec.Code)
		}
		if probe.called {
			t.Fatalf("token %q: next handler must not run", token)
		}
	}
}

// TestAuthAcceptsValidToken verifies 200 with actor admin for the correct token.
// TestAuthAcceptsValidToken 验证正确 Token 返回 200 且身份为 admin。
func TestAuthAcceptsValidToken(t *testing.T) {
	probe := &probeHandler{}
	srv := Auth("secret")(probe)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/scopes", nil)
	req.Header.Set("Authorization", "Bearer secret")
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !probe.called || probe.actor != "admin" {
		t.Fatalf("status=%d called=%v actor=%q", rec.Code, probe.called, probe.actor)
	}
}

// TestAuthWithoutConfigurationAllowsLocalAccess verifies that an empty token keeps
// the API open and assigns the local actor.
// TestAuthWithoutConfigurationAllowsLocalAccess 验证未配置 Token 时 API 开放且身份为 local。
func TestAuthWithoutConfigurationAllowsLocalAccess(t *testing.T) {
	probe := &probeHandler{}
	srv := Auth("")(probe)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/status", nil))
	if rec.Code != http.StatusOK || !probe.called || probe.actor != "local" {
		t.Fatalf("status=%d called=%v actor=%q", rec.Code, probe.called, probe.actor)
	}
}

// TestAuthExemptsHealthProbes verifies that GET /livez and GET /readyz pass
// without a token while other methods and paths stay protected.
// TestAuthExemptsHealthProbes 验证 GET /livez 与 /readyz 免认证，其余路径与方法仍受保护。
func TestAuthExemptsHealthProbes(t *testing.T) {
	for _, tc := range []struct {
		method string
		path   string
		wantOK bool
	}{
		{http.MethodGet, "/livez", true},
		{http.MethodGet, "/readyz", true},
		{http.MethodPost, "/livez", false},
		{http.MethodGet, "/api/v1/status", false},
		{http.MethodGet, "/livez/../secrets", false},
	} {
		probe := &probeHandler{}
		srv := Auth("secret")(probe)
		req := httptest.NewRequest(tc.method, tc.path, nil)
		rec := httptest.NewRecorder()
		srv.ServeHTTP(rec, req)
		if probe.called != tc.wantOK {
			t.Fatalf("%s %s: called=%v, want %v", tc.method, tc.path, probe.called, tc.wantOK)
		}
		if want := http.StatusOK; tc.wantOK && rec.Code != want {
			t.Fatalf("%s %s: status=%d, want %d", tc.method, tc.path, rec.Code, want)
		}
	}
}
