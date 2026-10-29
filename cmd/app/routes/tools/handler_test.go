// Tests for the read-only tool catalog endpoint.
// 只读工具清单接口的测试。
package tools

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/HappyLadySauceM/Nagi/cmd/app/svc"
	pluginsapp "github.com/HappyLadySauceM/Nagi/internal/plugins/application"
	"github.com/HappyLadySauceM/Nagi/internal/plugins/contracts"
	"github.com/HappyLadySauceM/Nagi/pkg/config"
)

// fakeProvider supplies fixed tools for registry construction.
// fakeProvider 为构建注册表提供固定工具。
type fakeProvider struct {
	manifest contracts.Manifest
	tools    []contracts.RegisteredTool
}

func (f fakeProvider) Manifest() contracts.Manifest { return f.manifest }

func (f fakeProvider) Tools(context.Context) ([]contracts.RegisteredTool, error) {
	return f.tools, nil
}

func (f fakeProvider) Close(context.Context) error { return nil }

// catalogRegistry builds a registry with one read-only QQ tool and one
// permissionless time tool.
// catalogRegistry 构建一个注册表，包含一个只读 QQ 工具与一个无权限的时间工具。
func catalogRegistry(t *testing.T) *pluginsapp.Registry {
	t.Helper()
	registry := pluginsapp.NewRegistry([]string{"builtin.qq", "builtin.time"})
	providers := []contracts.ToolProvider{
		fakeProvider{
			manifest: contracts.Manifest{ID: "builtin.time"},
			tools: []contracts.RegisteredTool{{
				Descriptor: contracts.ToolDescriptor{
					ID: "system.current_time", Description: "time", Category: contracts.CategorySystem,
					RiskLevel: contracts.RiskRead, ReadOnly: true,
				},
				Invoke: func(context.Context, string) (string, error) { return "now", nil },
			}},
		},
		fakeProvider{
			manifest: contracts.Manifest{ID: "builtin.qq"},
			tools: []contracts.RegisteredTool{{
				Descriptor: contracts.ToolDescriptor{
					ID: "qq.get_group_info", Description: "group", Category: contracts.CategoryGroup,
					ScopeKinds: []string{contracts.ScopeKindGroup}, RiskLevel: contracts.RiskRead,
					ReadOnly: true, Permission: "qq.group.read",
				},
				Invoke: func(context.Context, string) (string, error) { return "group", nil },
			}},
		},
	}
	for _, provider := range providers {
		if err := registry.Register(context.Background(), provider); err != nil {
			t.Fatalf("register provider: %v", err)
		}
	}
	return registry
}

// riskLevelRegistry builds a registry whose descriptors omit RiskLevel, so the
// reported level can only come from ReadOnly.
// riskLevelRegistry 构建省略 RiskLevel 的描述符，返回的级别只能由 ReadOnly 推导。
func riskLevelRegistry(t *testing.T) *pluginsapp.Registry {
	t.Helper()
	registry := pluginsapp.NewRegistry([]string{"builtin.risk"})
	provider := fakeProvider{
		manifest: contracts.Manifest{ID: "builtin.risk"},
		tools: []contracts.RegisteredTool{
			{
				Descriptor: contracts.ToolDescriptor{
					ID: "risk.read_only", Description: "read", Category: contracts.CategorySystem,
					ReadOnly: true,
				},
				Invoke: func(context.Context, string) (string, error) { return "", nil },
			},
			{
				Descriptor: contracts.ToolDescriptor{
					ID: "risk.writer", Description: "write", Category: contracts.CategorySystem,
					ReadOnly: false,
				},
				Invoke: func(context.Context, string) (string, error) { return "", nil },
			},
		},
	}
	if err := registry.Register(context.Background(), provider); err != nil {
		t.Fatalf("register provider: %v", err)
	}
	return registry
}

// TestListRequiresRegistry verifies a disabled tool subsystem answers 503
// instead of an empty catalog that looks like everything was revoked.
// TestListRequiresRegistry 验证工具子系统关闭时返回 503，
// 而不是让空清单看起来像全部被撤销。
func TestListRequiresRegistry(t *testing.T) {
	handler := NewHandler(&svc.ServiceContext{Config: &config.Config{}})
	rec := httptest.NewRecorder()
	handler.List(rec, httptest.NewRequest(http.MethodGet, "/api/v1/tools", nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", rec.Code)
	}
}

// TestListReportsDefaults verifies the catalog exposes registered tools,
// their provider and the effective default grant.
// TestListReportsDefaults 验证清单返回已注册工具、所属提供方与生效的默认授予。
func TestListReportsDefaults(t *testing.T) {
	cfg := &config.Config{Tools: config.Tools{
		PermissionDefaults: map[string]bool{"qq.group.read": false},
	}}
	handler := NewHandler(&svc.ServiceContext{Config: cfg, Tools: catalogRegistry(t)})

	rec := httptest.NewRecorder()
	handler.List(rec, httptest.NewRequest(http.MethodGet, "/api/v1/tools", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var envelope struct {
		Data catalog
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode: %v", err)
	}
	data := envelope.Data
	if len(data.Providers) != 2 || data.Providers[0] != "builtin.qq" || data.Providers[1] != "builtin.time" {
		t.Fatalf("providers = %v", data.Providers)
	}
	if len(data.Tools) != 2 || data.Tools[0].ID != "qq.get_group_info" || data.Tools[1].ID != "system.current_time" {
		t.Fatalf("tools = %+v", data.Tools)
	}
	if data.Tools[0].Provider != "builtin.qq" || data.Tools[0].DefaultGranted {
		t.Fatalf("qq tool = %+v", data.Tools[0])
	}
	if !data.Tools[1].DefaultGranted {
		t.Fatalf("permissionless read-only tool must be granted: %+v", data.Tools[1])
	}
	if len(data.Permissions) != 1 || data.Permissions[0].ID != "qq.group.read" ||
		data.Permissions[0].DefaultGranted || len(data.Permissions[0].Tools) != 1 {
		t.Fatalf("permissions = %+v", data.Permissions)
	}
}

// toolByID returns one entry of a decoded catalog.
// toolByID 返回已解码清单中的单个工具。
func toolByID(t *testing.T, tools []toolInfo, id string) toolInfo {
	t.Helper()
	for _, tool := range tools {
		if tool.ID == id {
			return tool
		}
	}
	t.Fatalf("tool %q missing from catalog", id)
	return toolInfo{}
}

// TestListWithoutScopeKindTreatsEveryToolAsApplicable verifies the default
// request keeps the previous behaviour while still returning the new metadata.
// TestListWithoutScopeKindTreatsEveryToolAsApplicable 验证未带 scope_kind 时
// 保持既有行为，同时照常返回新增元数据。
func TestListWithoutScopeKindTreatsEveryToolAsApplicable(t *testing.T) {
	cfg := &config.Config{Tools: config.Tools{
		PermissionDefaults: map[string]bool{"qq.group.read": true},
	}}
	handler := NewHandler(&svc.ServiceContext{Config: cfg, Tools: catalogRegistry(t)})

	rec := httptest.NewRecorder()
	handler.List(rec, httptest.NewRequest(http.MethodGet, "/api/v1/tools", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var envelope struct {
		Data catalog
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode: %v", err)
	}
	for _, tool := range envelope.Data.Tools {
		if !tool.Applicable || tool.UnavailableReason != "" {
			t.Fatalf("tool %q must be applicable without scope_kind: %+v", tool.ID, tool)
		}
	}
	qq := toolByID(t, envelope.Data.Tools, "qq.get_group_info")
	if qq.Category != contracts.CategoryGroup || qq.RiskLevel != contracts.RiskRead ||
		len(qq.ScopeKinds) != 1 || qq.ScopeKinds[0] != contracts.ScopeKindGroup {
		t.Fatalf("qq metadata = %+v", qq)
	}
	timeTool := toolByID(t, envelope.Data.Tools, "system.current_time")
	if timeTool.Category != contracts.CategorySystem || timeTool.RiskLevel != contracts.RiskRead ||
		len(timeTool.ScopeKinds) != 0 {
		t.Fatalf("time metadata = %+v", timeTool)
	}
	if timeTool.ScopeKinds == nil {
		t.Fatalf("time ScopeKinds must serialise as [] rather than null: %+v", timeTool)
	}
}

// TestListFiltersByScopeKind verifies a group-only tool is reported as
// unavailable for private scopes while a kind-agnostic tool stays available.
// TestListFiltersByScopeKind 验证仅群聊工具在私聊作用域中被标记为不适用，
// 而不限定会话类型的工具仍然适用。
func TestListFiltersByScopeKind(t *testing.T) {
	handler := NewHandler(&svc.ServiceContext{Config: &config.Config{}, Tools: catalogRegistry(t)})

	rec := httptest.NewRecorder()
	handler.List(rec, httptest.NewRequest(http.MethodGet, "/api/v1/tools?scope_kind=private", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var envelope struct {
		Data catalog
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode: %v", err)
	}
	qq := toolByID(t, envelope.Data.Tools, "qq.get_group_info")
	if qq.Applicable || qq.UnavailableReason != unavailableScopeKindMismatch {
		t.Fatalf("private qq tool = %+v", qq)
	}
	timeTool := toolByID(t, envelope.Data.Tools, "system.current_time")
	if !timeTool.Applicable || timeTool.UnavailableReason != "" {
		t.Fatalf("private time tool = %+v", timeTool)
	}
}

// TestListRejectsInvalidScopeKind verifies unknown query values fail with the
// regular error envelope instead of silently returning an unfiltered catalog.
// TestListRejectsInvalidScopeKind 验证未知查询值以常规错误信封失败，
// 而不是静默返回未过滤清单。
func TestListRejectsInvalidScopeKind(t *testing.T) {
	handler := NewHandler(&svc.ServiceContext{Config: &config.Config{}, Tools: catalogRegistry(t)})

	rec := httptest.NewRecorder()
	handler.List(rec, httptest.NewRequest(http.MethodGet, "/api/v1/tools?scope_kind=channel", nil))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body = %s", rec.Code, rec.Body.String())
	}
	var envelope struct {
		Code    int
		Message string
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if envelope.Code != http.StatusBadRequest || !strings.Contains(envelope.Message, "scope_kind") {
		t.Fatalf("envelope = %+v", envelope)
	}
}

// TestListScopeKindBoundaries verifies the optional filter trims padding, keeps
// an explicit empty value equivalent to omitting the parameter, and rejects
// case variants instead of guessing.
// TestListScopeKindBoundaries 验证可选过滤参数会去掉两端空白、空值等价于省略参数，
// 大小写变体则被拒绝而不是猜测。
func TestListScopeKindBoundaries(t *testing.T) {
	cases := []struct {
		name             string
		query            string
		status           int
		wantQQApplicable bool
	}{
		{name: "empty equals absent", query: "", status: http.StatusOK, wantQQApplicable: true},
		{name: "padded value is trimmed", query: " private ", status: http.StatusOK, wantQQApplicable: false},
		{name: "case variant is rejected", query: "PRIVATE", status: http.StatusBadRequest},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			handler := NewHandler(&svc.ServiceContext{Config: &config.Config{}, Tools: catalogRegistry(t)})
			rec := httptest.NewRecorder()
			target := "/api/v1/tools?scope_kind=" + url.QueryEscape(tc.query)
			handler.List(rec, httptest.NewRequest(http.MethodGet, target, nil))
			if rec.Code != tc.status {
				t.Fatalf("status = %d, want %d; body = %s", rec.Code, tc.status, rec.Body.String())
			}
			if tc.status != http.StatusOK {
				return
			}
			var envelope struct {
				Data catalog
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &envelope); err != nil {
				t.Fatalf("decode: %v", err)
			}
			qq := toolByID(t, envelope.Data.Tools, "qq.get_group_info")
			if qq.Applicable != tc.wantQQApplicable {
				t.Fatalf("qq applicable = %v, want %v", qq.Applicable, tc.wantQQApplicable)
			}
		})
	}
}

// TestListDerivesRiskLevelFromReadOnly verifies descriptors that omit RiskLevel
// still report the level the policy layer actually uses, so a forgotten field
// cannot downgrade a write tool to read.
// TestListDerivesRiskLevelFromReadOnly 验证省略 RiskLevel 的描述符仍返回
// 策略层实际使用的级别，漏填字段不会把写工具降级为只读。
func TestListDerivesRiskLevelFromReadOnly(t *testing.T) {
	handler := NewHandler(&svc.ServiceContext{Config: &config.Config{}, Tools: riskLevelRegistry(t)})

	rec := httptest.NewRecorder()
	handler.List(rec, httptest.NewRequest(http.MethodGet, "/api/v1/tools", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var envelope struct {
		Data catalog
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode: %v", err)
	}
	for toolID, want := range map[string]contracts.RiskLevel{
		"risk.read_only": contracts.RiskRead,
		"risk.writer":    contracts.RiskWrite,
	} {
		if got := toolByID(t, envelope.Data.Tools, toolID).RiskLevel; got != want {
			t.Fatalf("tool %q risk level = %q, want %q", toolID, got, want)
		}
	}
}
