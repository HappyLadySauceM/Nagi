// Tests for scope policy request validation.
// 作用域策略请求校验的测试。
package scope

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/HappyLadySauceM/Nagi/cmd/app/svc"
	models "github.com/HappyLadySauceM/Nagi/internal/models/contracts"
	scopeapp "github.com/HappyLadySauceM/Nagi/internal/scope/application"
	scopedomain "github.com/HappyLadySauceM/Nagi/internal/scope/domain"
)

// TestUnknownPermissions verifies that overrides outside the registered
// permission set are rejected, while an unavailable registry validates nothing.
// TestUnknownPermissions 验证已注册权限集合之外的覆盖项会被拒绝，
// 注册表不可用时则不做校验。
func TestUnknownPermissions(t *testing.T) {
	known := map[string]bool{"qq.group.read": true}

	if got := unknownPermissions(map[string]bool{"qq.group.read": false}, known); len(got) != 0 {
		t.Fatalf("known permission rejected: %v", got)
	}
	got := unknownPermissions(map[string]bool{"qq.admin.delete": true, "qq.group.read": true}, known)
	if !reflect.DeepEqual(got, []string{"qq.admin.delete"}) {
		t.Fatalf("unknown = %v", got)
	}
	if got := unknownPermissions(map[string]bool{"qq.admin.delete": true}, nil); got != nil {
		t.Fatalf("missing registry must skip validation, got %v", got)
	}
}

// TestRejectablePermissionsAllowsStoredKeys verifies that a permission removed
// from the catalogue can still be written back, while a brand-new unknown key
// is rejected.
// TestRejectablePermissionsAllowsStoredKeys 验证已从清单移除的权限仍可写回，
// 而全新的未知键仍被拒绝。
func TestRejectablePermissionsAllowsStoredKeys(t *testing.T) {
	unknown := []string{"qq.group.read", "qq.admin.delete"}
	stored := map[string]bool{"qq.group.read": false}

	got := rejectablePermissions(unknown, stored)
	if !reflect.DeepEqual(got, []string{"qq.admin.delete"}) {
		t.Fatalf("rejectable = %v", got)
	}
	if got := rejectablePermissions(unknown, nil); len(got) != len(unknown) {
		t.Fatalf("a missing stored policy must reject every unknown key, got %v", got)
	}
}

// TestPolicyUpdateMemoryReadDefaultsOff verifies an older client that omits the
// new allow_memory_read field keeps long-term memory disabled.
// TestPolicyUpdateMemoryReadDefaultsOff 验证旧客户端省略 allow_memory_read 时保持长期记忆关闭。
func TestPolicyUpdateMemoryReadDefaultsOff(t *testing.T) {
	var body policyUpdate
	if err := json.Unmarshal([]byte(`{"enabled":true,"reply_mode":"mention_only"}`), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.AllowMemoryRead {
		t.Fatal("missing allow_memory_read must default to false")
	}
	if err := json.Unmarshal([]byte(`{"allow_memory_read":true}`), &body); err != nil {
		t.Fatalf("decode explicit: %v", err)
	}
	if !body.AllowMemoryRead {
		t.Fatal("explicit allow_memory_read=true was lost")
	}
}

// memScopeRepo is a minimal in-memory scope repository for HTTP tests.
// memScopeRepo 是 HTTP 测试使用的最小内存作用域仓库。
type memScopeRepo struct{ policies map[string]scopedomain.Policy }

func (m *memScopeRepo) key(botID int64, scopeType scopedomain.ScopeType, scopeID int64) string {
	return fmt.Sprintf("%d:%s:%d", botID, scopeType, scopeID)
}

func (m *memScopeRepo) Get(_ context.Context, botID int64, scopeType scopedomain.ScopeType, scopeID int64) (scopedomain.Policy, bool, error) {
	policy, ok := m.policies[m.key(botID, scopeType, scopeID)]
	return policy, ok, nil
}

func (m *memScopeRepo) List(context.Context, *int64) ([]scopedomain.Policy, error) {
	out := make([]scopedomain.Policy, 0, len(m.policies))
	for _, policy := range m.policies {
		out = append(out, policy)
	}
	return out, nil
}

func (m *memScopeRepo) Upsert(_ context.Context, botID int64, policy scopedomain.Policy) error {
	m.policies[m.key(botID, policy.ScopeType, policy.ScopeID)] = policy
	return nil
}

func (m *memScopeRepo) Delete(_ context.Context, botID int64, scopeType scopedomain.ScopeType, scopeID int64) (bool, error) {
	key := m.key(botID, scopeType, scopeID)
	_, ok := m.policies[key]
	delete(m.policies, key)
	return ok, nil
}

func putScopeRequest(mux *http.ServeMux, body string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/api/v1/scopes/private/1552089234?bot_id=1", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	mux.ServeHTTP(rec, req)
	return rec
}

type httpGateModels struct{ allowed map[string]bool }

func (m httpGateModels) ResolveModel(_ context.Context, usage string, _ int64, _ string, _ int64) (models.Resolved, error) {
	if !m.allowed[usage] {
		return models.Resolved{}, fmt.Errorf("not verified")
	}
	return models.Resolved{}, nil
}

func (httpGateModels) BilibiliCredential(context.Context) (string, error) { return "", nil }

// productionScopeService mirrors the assembly used by the server: generation
// is tested, the default transcription grant is true, and ASR is unavailable.
// productionScopeService 模拟服务端装配：生成模型已测试，默认转写授权为 true，ASR 不可用。
func productionScopeService(t *testing.T, repo scopeapp.Repository) *scopeapp.Service {
	t.Helper()
	scopeService, err := scopeapp.NewService(repo)
	if err != nil {
		t.Fatal(err)
	}
	scopeService.SetModels(httpGateModels{allowed: map[string]bool{"generation": true}})
	scopeService.SetPermissionResolver(func(overrides map[string]bool) scopeapp.PermissionState {
		defaults := map[string]bool{"media.voice.transcribe": true, "media.voice.send": false}
		effective := map[string]bool{}
		for key, value := range defaults {
			effective[key] = value
		}
		for key, value := range overrides {
			effective[key] = value
		}
		return scopeapp.PermissionState{Defaults: defaults, Effective: effective}
	})
	return scopeService
}

func TestPutFirstEnableReturnsOK(t *testing.T) {
	repo := &memScopeRepo{policies: map[string]scopedomain.Policy{}}
	scopeService := productionScopeService(t, repo)
	mux := http.NewServeMux()
	NewHandler(&svc.ServiceContext{Scope: scopeService}).Register(mux)
	body := `{"max_messages_per_run":0,"max_chars_per_message":4500,"max_total_chars_per_run":0,"message_interval_ms":700,"enabled":true,"reply_mode":"mention_only","store_messages":true,"allow_history_read":false,"allow_memory_read":false,"allow_memory_write":false,"min_reply_interval_ms":10000,"max_replies_per_hour":0,"tool_permissions":{}}`
	rec := putScopeRequest(mux, body)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	var envelope struct {
		Data struct {
			Enabled   bool
			ScopeID   int64
			ScopeType string
		}
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !envelope.Data.Enabled || envelope.Data.ScopeID != 1552089234 || envelope.Data.ScopeType != "private" {
		t.Fatalf("policy = %+v", envelope.Data)
	}
}

func TestPutExplicitVoiceGrantWithoutModelRejected(t *testing.T) {
	repo := &memScopeRepo{policies: map[string]scopedomain.Policy{}}
	scopeService := productionScopeService(t, repo)
	mux := http.NewServeMux()
	NewHandler(&svc.ServiceContext{Scope: scopeService}).Register(mux)
	body := `{"enabled":true,"reply_mode":"mention_only","store_messages":true,"tool_permissions":{"media.voice.transcribe":true}}`
	rec := putScopeRequest(mux, body)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
}

func TestPutRejectsInvalidPolicy(t *testing.T) {
	repo := &memScopeRepo{policies: map[string]scopedomain.Policy{}}
	scopeService, err := scopeapp.NewService(repo)
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	NewHandler(&svc.ServiceContext{Scope: scopeService}).Register(mux)
	body := `{"enabled":true,"reply_mode":"not-a-mode"}`
	rec := putScopeRequest(mux, body)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
}
