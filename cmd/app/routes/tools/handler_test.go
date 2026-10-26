// Tests for the read-only tool catalog endpoint.
// 只读工具清单接口的测试。
package tools

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
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
				Descriptor: contracts.ToolDescriptor{ID: "system.current_time", Description: "time", ReadOnly: true},
				Invoke:     func(context.Context, string) (string, error) { return "now", nil },
			}},
		},
		fakeProvider{
			manifest: contracts.Manifest{ID: "builtin.qq"},
			tools: []contracts.RegisteredTool{{
				Descriptor: contracts.ToolDescriptor{
					ID: "qq.get_group_info", Description: "group", ReadOnly: true, Permission: "qq.group.read",
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
