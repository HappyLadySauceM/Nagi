// Tests for the settings endpoints: contract, validation and conflicts.
// 配置接口的测试：契约、校验与冲突。
package settings

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/HappyLadySauceM/Nagi/cmd/app/svc"
	v1 "github.com/HappyLadySauceM/Nagi/cmd/app/types/v1"
	settingsapp "github.com/HappyLadySauceM/Nagi/internal/settings/application"
	settingsdomain "github.com/HappyLadySauceM/Nagi/internal/settings/domain"
	"github.com/HappyLadySauceM/Nagi/pkg/config"
)

type memoryRepo struct {
	revision     settingsdomain.Revision
	exists       bool
	beforeUpdate func()
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
	if before := m.beforeUpdate; before != nil {
		m.beforeUpdate = nil
		before()
	}
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
		Role:    settingsdomain.Role{BotName: "Nagi"},
		Context: settingsdomain.Context{HistoryLimit: 30},
		AI:      settingsdomain.AI{MaxOutputTokens: 400, GenerationTimeoutSeconds: 25},
		Intent: settingsdomain.Intent{
			Provider: "jev", Endpoint: "https://api.example/v1/systemone", Model: "jev",
			TimeoutSeconds: 3, ContinuationThreshold: 0.8, InterjectionThreshold: 0.95,
			AddressedOtherMax: 0.6,
		},
		Agent: settingsdomain.Agent{
			MaxSteps: 6, MaxToolCalls: 5, MaxRunDurationSeconds: 30,
			Sending: settingsdomain.Sending{MaxMessagesPerRun: 0, MaxCharsPerMessage: 4500, MaxTotalCharsPerRun: 0},
		},
		Messages: settingsdomain.Messages{RetentionDays: 30},
		Tools: settingsdomain.Tools{
			TimeoutSeconds: 5, MaxResultBytes: 8192,
			PermissionDefaults: map[string]bool{},
		},
	}
}

func newTestHandler(t *testing.T) (*Handler, *memoryRepo) {
	t.Helper()
	repo := &memoryRepo{}
	return newTestReplica(t, repo), repo
}

func newTestReplica(t *testing.T, repo *memoryRepo) *Handler {
	t.Helper()
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
	})
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
	body, _ := json.Marshal(v1.UpdateRequest{Settings: invalid, Version: 1})
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
	body, _ := json.Marshal(v1.UpdateRequest{Settings: defaults(), Version: 42})
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
	body, _ := json.Marshal(v1.UpdateRequest{Settings: next, Version: 1})
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

func TestGetExposesRoleAndContextWithoutLegacyIntentFields(t *testing.T) {
	// 新模型必须返回 Role/Context，且 Intent 不再暴露旧 BotName/HistoryLimit。
	handler, _ := newTestHandler(t)
	rec := httptest.NewRecorder()
	handler.Get(rec, httptest.NewRequest(http.MethodGet, "/api/v1/settings", nil))
	var envelope struct {
		Data struct {
			Settings map[string]any
		}
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode: %v", err)
	}
	settings := envelope.Data.Settings
	role, _ := settings["Role"].(map[string]any)
	contextView, _ := settings["Context"].(map[string]any)
	intent, _ := settings["Intent"].(map[string]any)
	if role["BotName"] != "Nagi" || contextView["HistoryLimit"] != float64(30) {
		t.Fatalf("role/context = %+v/%+v", role, contextView)
	}
	if _, exists := intent["BotName"]; exists {
		t.Fatalf("legacy Intent.BotName leaked: %+v", intent)
	}
	if _, exists := intent["HistoryLimit"]; exists {
		t.Fatalf("legacy Intent.HistoryLimit leaked: %+v", intent)
	}
}

func TestPatchSettingsUpdatesRoleAndContext(t *testing.T) {
	// 部分更新入口必须接受新 Role/Context 分区并持久化。
	handler, _ := newTestHandler(t)
	body, _ := json.Marshal(map[string]any{
		"Version": 1,
		"Sections": map[string]any{
			"Role":    map[string]any{"BotName": "新名字"},
			"Context": map[string]any{"HistoryLimit": 42},
		},
	})
	rec := httptest.NewRecorder()
	handler.patchSettings(rec, httptest.NewRequest(http.MethodPatch, "/api/v1/settings", bytes.NewReader(body)))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
	}
	var envelope struct {
		Data settingsapp.View
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if envelope.Data.Settings.Role.BotName != "新名字" || envelope.Data.Settings.Context.HistoryLimit != 42 {
		t.Fatalf("settings = %+v", envelope.Data.Settings)
	}
}

func TestPatchRoleCallNamesReplacesBindingsAndOmissionPreservesThem(t *testing.T) {
	handler, _ := newTestHandler(t)
	for i, tc := range []struct {
		role  string
		want  int
		botID int64
		name  string
	}{
		{`{"CallNames":{"1":" Alpha ","10":"Beta"}}`, 2, 1, "Alpha"},
		{`{"BotName":"Renamed"}`, 2, 10, "Beta"},
		{`{"CallNames":{"10":"Shared"}}`, 1, 10, "Shared"},
		{`{"CallNames":{}}`, 0, 0, ""},
	} {
		body := fmt.Sprintf(`{"Version":%d,"Sections":{"Role":%s}}`, i+1, tc.role)
		rec := httptest.NewRecorder()
		handler.patchSettings(rec, httptest.NewRequest(http.MethodPatch, "/api/v1/settings", strings.NewReader(body)))
		var envelope struct{ Data settingsapp.View }
		if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &envelope) != nil {
			t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
		}
		got := envelope.Data.Settings.Role.CallNames
		if len(got) != tc.want || got[tc.botID] != tc.name {
			t.Fatalf("bindings = %v", got)
		}
	}
}

func TestPutSettingsPreservesOmittedCallNamesAndAllowsExplicitClear(t *testing.T) {
	handler, repo := newTestHandler(t)
	patch := httptest.NewRecorder()
	handler.patchSettings(patch, httptest.NewRequest(http.MethodPatch, "/api/v1/settings", strings.NewReader(
		`{"Version":1,"Sections":{"Role":{"CallNames":{"1":"Alpha"}}}}`,
	)))
	if patch.Code != http.StatusOK || repo.revision.Settings.Role.CallNames[1] != "Alpha" {
		t.Fatalf("patch status=%d persisted bindings=%v", patch.Code, repo.revision.Settings.Role.CallNames)
	}
	for _, tc := range []struct {
		version    int64
		clear      bool
		wantStatus int
		wantName   string
	}{
		{1, false, http.StatusConflict, "Alpha"},
		{2, false, http.StatusOK, "Alpha"},
		{3, true, http.StatusOK, ""},
	} {
		incoming := repo.revision.Settings.Clone()
		incoming.Agent.MaxSteps++
		incoming.Role.CallNames = nil
		body, err := json.Marshal(incoming)
		if err != nil {
			t.Fatal(err)
		}
		if tc.clear {
			body = bytes.Replace(body, []byte(`"Role":{`), []byte(`"Role":{"CallNames":{},`), 1)
		}
		req := fmt.Sprintf(`{"Version":%d,"Settings":%s}`, tc.version, body)
		rec := httptest.NewRecorder()
		handler.Put(rec, httptest.NewRequest(http.MethodPut, "/api/v1/settings", strings.NewReader(req)))
		if rec.Code != tc.wantStatus || repo.revision.Settings.Role.CallNames[1] != tc.wantName {
			t.Fatalf("PUT status=%d persisted bindings=%v", rec.Code, repo.revision.Settings.Role.CallNames)
		}
	}
}

func TestPutSettingsUsesPersistedRevisionAcrossReplicas(t *testing.T) {
	writer, repo := newTestHandler(t)
	patch := func(version int64, name string) {
		t.Helper()
		body := fmt.Sprintf(`{"Version":%d,"Sections":{"Role":{"CallNames":{"1":%q}}}}`, version, name)
		rec := httptest.NewRecorder()
		writer.patchSettings(rec, httptest.NewRequest(http.MethodPatch, "/api/v1/settings", strings.NewReader(body)))
		if rec.Code != http.StatusOK {
			t.Fatalf("PATCH status=%d body=%s", rec.Code, rec.Body.String())
		}
	}
	patch(1, "Alpha")
	lagged := newTestReplica(t, repo)
	patch(2, "\ufeff")
	if cached := lagged.svcCtx.Settings.View(); cached.Version != 2 || cached.Settings.Role.CallNames[1] != "Alpha" {
		t.Fatalf("lagged cache version=%d names=%v", cached.Version, cached.Settings.Role.CallNames)
	}
	if repo.revision.Version != 3 || repo.revision.Settings.Role.CallNames[1] != "\ufeff" {
		t.Fatalf("PATCH did not persist the boundary value: %+v", repo.revision.Settings.Role.CallNames)
	}
	put := func(version int64, nameField string) int {
		t.Helper()
		incoming := repo.revision.Settings.Clone()
		incoming.Agent.MaxSteps++
		incoming.Role.CallNames = nil
		body, err := json.Marshal(incoming)
		if err != nil {
			t.Fatal(err)
		}
		if nameField != "" {
			body = bytes.Replace(body, []byte(`"Role":{`), []byte(`"Role":{`+nameField+`,`), 1)
		}
		req := fmt.Sprintf(`{"Version":%d,"Settings":%s}`, version, body)
		rec := httptest.NewRecorder()
		lagged.Put(rec, httptest.NewRequest(http.MethodPut, "/api/v1/settings", strings.NewReader(req)))
		return rec.Code
	}
	if status := put(2, ""); status != http.StatusConflict || repo.revision.Version != 3 {
		t.Fatalf("stale PUT status=%d stored version=%d", status, repo.revision.Version)
	}
	if status := put(3, ""); status != http.StatusOK || repo.revision.Version != 4 ||
		repo.revision.Settings.Role.CallNames[1] != "\ufeff" || repo.revision.Settings.Agent.MaxSteps != 7 {
		t.Fatalf("current PUT status=%d stored version=%d names=%q max steps=%d", status,
			repo.revision.Version, repo.revision.Settings.Role.CallNames[1], repo.revision.Settings.Agent.MaxSteps)
	}
	writer = newTestReplica(t, repo)
	repo.beforeUpdate = func() { patch(4, "Next") }
	if status := put(4, ""); status != http.StatusConflict || repo.revision.Version != 5 ||
		repo.revision.Settings.Role.CallNames[1] != "Next" || repo.revision.Settings.Agent.MaxSteps != 7 {
		t.Fatalf("racing PUT status=%d stored version=%d names=%v max steps=%d", status,
			repo.revision.Version, repo.revision.Settings.Role.CallNames, repo.revision.Settings.Agent.MaxSteps)
	}
	if status := put(5, `"CallNames":{}`); status != http.StatusOK || repo.revision.Version != 6 ||
		len(repo.revision.Settings.Role.CallNames) != 0 || repo.revision.Settings.Agent.MaxSteps != 8 {
		t.Fatalf("explicit clear status=%d stored version=%d names=%v max steps=%d", status,
			repo.revision.Version, repo.revision.Settings.Role.CallNames, repo.revision.Settings.Agent.MaxSteps)
	}
}
