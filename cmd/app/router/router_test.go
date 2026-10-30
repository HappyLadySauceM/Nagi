// Tests for route registration semantics of the management API.
// 管理 API 路由注册语义的测试。
package router

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/HappyLadySauceM/Nagi/cmd/app/svc"
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
