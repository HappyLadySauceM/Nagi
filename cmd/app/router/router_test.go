// Tests for route registration semantics of the management API.
// 管理 API 路由注册语义的测试。
package router

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/HappyLadySauceM/Nagi/cmd/app/svc"
	v1 "github.com/HappyLadySauceM/Nagi/cmd/app/types/v1"
	botapp "github.com/HappyLadySauceM/Nagi/internal/bot/application"
	"github.com/HappyLadySauceM/Nagi/pkg/config"
)

func testContext() *svc.ServiceContext {
	return &svc.ServiceContext{
		Config: &config.Config{Server: config.Server{Listen: "127.0.0.1:5080", Enabled: true}},
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
}

// TestSettingsRouteExistsWithoutDatabase verifies the settings endpoint is
// registered even when the database is absent, so callers can tell "not ready"
// (503) from "this build has no such route" (404).
// TestSettingsRouteExistsWithoutDatabase 验证未配置数据库时配置接口仍然注册，
// 调用方因此能区分「未就绪」(503) 与「该构建没有这个路由」(404)。
func TestSettingsRouteExistsWithoutDatabase(t *testing.T) {
	handler := New(testContext(), testContext().Logger)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/settings", nil))

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d body = %s, want 503", rec.Code, rec.Body.String())
	}
	var envelope struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("response must be a JSON envelope: %v (%s)", err, rec.Body.String())
	}
	if envelope.Code != http.StatusServiceUnavailable || envelope.Message != "database not configured" {
		t.Fatalf("envelope = %+v", envelope)
	}
}

// TestUnknownRouteStaysNotFound verifies the unconditional settings route did
// not turn every unknown path into a handler response.
// TestUnknownRouteStaysNotFound 验证无条件注册配置路由没有把未知路径变成处理器响应。
func TestUnknownRouteStaysNotFound(t *testing.T) {
	ctx := testContext()
	handler := New(ctx, ctx.Logger)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/not-a-route", nil))

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}

func TestIngressRouteRequiresAuthorizationAndExplicitPause(t *testing.T) {
	ctx := testContext()
	ctx.MessageIngress = &botapp.MessageIngress{}
	ctx.Config.Server.AdminToken = "synthetic-test-token"
	handler := New(ctx, ctx.Logger)
	for _, tc := range []struct {
		name, method, token, body string
		status                    int
		paused                    bool
	}{
		{name: "default", method: http.MethodGet, token: "synthetic-test-token", status: http.StatusOK},
		{name: "unauthorized pause", method: http.MethodPut, body: `{"paused":true}`, status: http.StatusUnauthorized},
		{name: "pause", method: http.MethodPut, token: "synthetic-test-token", body: `{"paused":true}`, status: http.StatusOK, paused: true},
		{name: "missing", method: http.MethodPut, token: "synthetic-test-token", body: `{}`, status: http.StatusBadRequest, paused: true},
		{name: "null", method: http.MethodPut, token: "synthetic-test-token", body: `{"paused":null}`, status: http.StatusBadRequest, paused: true},
		{name: "non-boolean", method: http.MethodPut, token: "synthetic-test-token", body: `{"paused":"false"}`, status: http.StatusBadRequest, paused: true},
		{name: "unknown field", method: http.MethodPut, token: "synthetic-test-token", body: `{"paused":false,"bot_id":1}`, status: http.StatusBadRequest, paused: true},
		{name: "multiple objects", method: http.MethodPut, token: "synthetic-test-token", body: `{"paused":false}{}`, status: http.StatusBadRequest, paused: true},
		{name: "oversized body", method: http.MethodPut, token: "synthetic-test-token", body: `{"paused":false}` + strings.Repeat(" ", 1024), status: http.StatusBadRequest, paused: true},
		{name: "unauthorized read", method: http.MethodGet, status: http.StatusUnauthorized, paused: true},
		{name: "read paused", method: http.MethodGet, token: "synthetic-test-token", status: http.StatusOK, paused: true},
		{name: "resume", method: http.MethodPut, token: "synthetic-test-token", body: `{"paused":false}`, status: http.StatusOK},
		{name: "read resumed", method: http.MethodGet, token: "synthetic-test-token", status: http.StatusOK},
	} {
		t.Run(tc.name, func(t *testing.T) {
			request := httptest.NewRequest(tc.method, "/api/v1/ingress", strings.NewReader(tc.body))
			if tc.token != "" {
				request.Header.Set("Authorization", "Bearer "+tc.token)
			}
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, request)
			if rec.Code != tc.status || ctx.MessageIngress.Paused() != tc.paused {
				t.Fatalf("status=%d paused=%t body=%s", rec.Code, ctx.MessageIngress.Paused(), rec.Body.String())
			}
			if rec.Code == http.StatusOK {
				var envelope struct {
					Data *v1.IngressView `json:"data"`
				}
				if err := json.Unmarshal(rec.Body.Bytes(), &envelope); err != nil || envelope.Data == nil || envelope.Data.Paused != tc.paused || envelope.Data.Dispatching != 0 {
					t.Fatalf("invalid ingress state: %s", rec.Body.String())
				}
			}
		})
	}
}
