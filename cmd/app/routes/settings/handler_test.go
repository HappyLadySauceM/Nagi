// Tests for the settings endpoints: contract, validation and conflicts.
// 配置接口的测试：契约、校验与冲突。
package settings

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/HappyLadySauceM/Nagi/cmd/app/svc"
	settingsapp "github.com/HappyLadySauceM/Nagi/internal/settings/application"
	settingsdomain "github.com/HappyLadySauceM/Nagi/internal/settings/domain"
	"github.com/HappyLadySauceM/Nagi/pkg/config"
)

type memoryRepo struct {
	revision settingsdomain.Revision
	exists   bool
}

func (m *memoryRepo) Load(context.Context) (settingsdomain.Revision, bool, error) {
	return m.revision, m.exists, nil
}

func (m *memoryRepo) Insert(_ context.Context, settings settingsdomain.Settings, actor string) (settingsdomain.Revision, error) {
	m.exists = true
	m.revision = settingsdomain.Revision{Settings: settings, Version: 1, UpdatedAt: time.Now(), UpdatedBy: actor}
	return m.revision, nil
}

func (m *memoryRepo) Update(_ context.Context, settings settingsdomain.Settings, expected int64, actor string) (settingsdomain.Revision, bool, error) {
	if !m.exists || m.revision.Version != expected {
		return settingsdomain.Revision{}, false, nil
	}
	m.revision = settingsdomain.Revision{
		Settings: settings, Version: expected + 1, UpdatedAt: time.Now(), UpdatedBy: actor,
	}
	return m.revision, true, nil
}

func (m *memoryRepo) Version(context.Context) (int64, bool, error) {
	return m.revision.Version, m.exists, nil
}

func defaults() settingsdomain.Settings {
	return settingsdomain.Settings{
		AI: settingsdomain.AI{MaxOutputTokens: 400, GenerationTimeoutSeconds: 25},
		Intent: settingsdomain.Intent{
			Provider: "jev", Endpoint: "https://api.example/v1/systemone", Model: "jev",
			TimeoutSeconds: 3, ContinuationThreshold: 0.8, InterjectionThreshold: 0.95,
			AddressedOtherMax: 0.6, HistoryLimit: 30,
		},
		Agent: settingsdomain.Agent{
			MaxSteps: 6, MaxToolCalls: 5, MaxRunDurationSeconds: 30,
			Sending: settingsdomain.Sending{MaxMessagesPerRun: 3, MaxCharsPerMessage: 500, MaxTotalCharsPerRun: 1500},
		},
		Tools: settingsdomain.Tools{
			TimeoutSeconds: 5, MaxResultBytes: 8192,
			PermissionDefaults: map[string]bool{},
		},
	}
}

func newTestHandler(t *testing.T) (*Handler, *memoryRepo) {
	t.Helper()
	repo := &memoryRepo{}
	provider := settingsapp.NewProvider(settingsapp.Snapshot{Settings: defaults()})
	service, err := settingsapp.NewService(settingsapp.Options{
		Repository: repo,
		Provider:   provider,
		Defaults:   defaults(),
		Providers:  []string{"builtin.time"},
		Logger:     slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	if err != nil {
		t.Fatalf("new service: %v", err)
	}
	if err := service.Ensure(context.Background()); err != nil {
		t.Fatalf("ensure: %v", err)
	}
	return NewHandler(&svc.ServiceContext{
		Config:   &config.Config{},
		Logger:   slog.New(slog.NewTextHandler(io.Discard, nil)),
		Settings: service,
	}), repo
}

func TestGetReturnsRevisionAndBaseline(t *testing.T) {
	// 读取返回当前修订、默认基线与提供方列表。
	handler, _ := newTestHandler(t)
	rec := httptest.NewRecorder()
	handler.Get(rec, httptest.NewRequest(http.MethodGet, "/api/v1/settings", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
	}
	var envelope struct {
		Data settingsapp.View
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if envelope.Data.Version != 1 || !envelope.Data.Persisted {
		t.Fatalf("view = %+v", envelope.Data)
	}
	if len(envelope.Data.Providers) != 1 || envelope.Data.Providers[0] != "builtin.time" {
		t.Fatalf("providers = %v", envelope.Data.Providers)
	}
}

func TestPutRejectsInvalidSettingsWithFieldErrors(t *testing.T) {
	// 校验失败返回 400，并带上字段级错误。
	handler, _ := newTestHandler(t)
	invalid := defaults()
	invalid.Agent.MaxSteps = 0
	body, _ := json.Marshal(updateRequest{Settings: invalid, Version: 1})
	rec := httptest.NewRecorder()
	handler.Put(rec, httptest.NewRequest(http.MethodPut, "/api/v1/settings", bytes.NewReader(body)))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
	}
	var envelope struct {
		Data struct {
			Errors map[string]string
		}
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if envelope.Data.Errors["Agent.MaxSteps"] == "" {
		t.Fatalf("errors = %+v", envelope.Data.Errors)
	}
}

func TestPutRejectsStaleRevision(t *testing.T) {
	// 版本冲突返回 409，提示刷新后重试。
	handler, _ := newTestHandler(t)
	body, _ := json.Marshal(updateRequest{Settings: defaults(), Version: 42})
	rec := httptest.NewRecorder()
	handler.Put(rec, httptest.NewRequest(http.MethodPut, "/api/v1/settings", bytes.NewReader(body)))
	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
	}
}

func TestPutStoresNewRevision(t *testing.T) {
	// 成功保存返回新修订，后续读取看到同一版本。
	handler, _ := newTestHandler(t)
	next := defaults()
	next.Agent.Sending.MaxMessagesPerRun = 5
	body, _ := json.Marshal(updateRequest{Settings: next, Version: 1})
	rec := httptest.NewRecorder()
	handler.Put(rec, httptest.NewRequest(http.MethodPut, "/api/v1/settings", bytes.NewReader(body)))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
	}
	var envelope struct {
		Data settingsapp.View
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if envelope.Data.Version != 2 || envelope.Data.Settings.Agent.Sending.MaxMessagesPerRun != 5 {
		t.Fatalf("view = %+v", envelope.Data)
	}
}

func TestResetRestoresDefaults(t *testing.T) {
	// 恢复默认写回基线并递增版本。
	handler, _ := newTestHandler(t)
	rec := httptest.NewRecorder()
	handler.Reset(rec, httptest.NewRequest(http.MethodPost, "/api/v1/settings/reset", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
	}
	var envelope struct {
		Data settingsapp.View
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if envelope.Data.Version != 2 {
		t.Fatalf("version = %d, want 2", envelope.Data.Version)
	}
}

func TestEndpointsRequireService(t *testing.T) {
	// 未配置数据库时返回 503，而不是空配置。
	handler := NewHandler(&svc.ServiceContext{
		Config: &config.Config{},
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	for name, call := range map[string]http.HandlerFunc{
		"get":   handler.Get,
		"put":   handler.Put,
		"reset": handler.Reset,
	} {
		rec := httptest.NewRecorder()
		call(rec, httptest.NewRequest(http.MethodGet, "/api/v1/settings", nil))
		if rec.Code != http.StatusServiceUnavailable {
			t.Fatalf("%s status = %d, want 503", name, rec.Code)
		}
	}
}
