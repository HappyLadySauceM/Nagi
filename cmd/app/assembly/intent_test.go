// Tests for the tool resolver wiring: permission filtering, call budget and
// run reporting happen in one place, so the assembly is worth testing directly.
// 工具解析装配的测试：权限过滤、调用预算与运行上报集中在一处，因此直接测试装配层。
package assembly

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/cloudwego/eino/components/tool"

	agentcontracts "github.com/HappyLadySauceM/QQBot/internal/agent/contracts"
	pluginsapp "github.com/HappyLadySauceM/QQBot/internal/plugins/application"
	plugincontracts "github.com/HappyLadySauceM/QQBot/internal/plugins/contracts"
	"github.com/HappyLadySauceM/QQBot/pkg/config"
)

// fakeProvider exposes canned tools without touching any external system.
// fakeProvider 暴露预置工具，不接触任何外部系统。
type fakeProvider struct {
	tools []plugincontracts.RegisteredTool
}

func (fakeProvider) Manifest() plugincontracts.Manifest {
	return plugincontracts.Manifest{ID: "test.fake", Version: "1"}
}

func (p fakeProvider) Tools(context.Context) ([]plugincontracts.RegisteredTool, error) {
	return p.tools, nil
}

func (fakeProvider) Close(context.Context) error { return nil }

// TestBuildToolsResolverFiltersAndBudgets verifies that a revoked permission
// hides its tool, that an allowed tool reports its run, and that the per-run
// budget stops further calls.
// TestBuildToolsResolverFiltersAndBudgets 验证被撤销的权限隐藏对应工具、
// 允许的工具上报运行，且单次运行预算会阻止继续调用。
func TestBuildToolsResolverFiltersAndBudgets(t *testing.T) {
	registry := pluginsapp.NewRegistry([]string{"test.fake"})
	calls := 0
	provider := fakeProvider{tools: []plugincontracts.RegisteredTool{
		{
			Descriptor: plugincontracts.ToolDescriptor{
				ID: "test.read", Description: "读取", ReadOnly: true, Permission: "test.read",
			},
			Invoke: func(context.Context, string) (string, error) {
				calls++
				return "ok", nil
			},
		},
		{
			Descriptor: plugincontracts.ToolDescriptor{
				ID: "test.admin", Description: "管理", ReadOnly: true, Permission: "test.admin",
			},
			Invoke: func(context.Context, string) (string, error) {
				calls++
				return "admin", nil
			},
		},
	}}
	if err := registry.Register(context.Background(), provider); err != nil {
		t.Fatalf("register: %v", err)
	}
	t.Cleanup(func() { _ = registry.Close(context.Background()) })

	cfg := &config.Config{
		Agent: config.Agent{MaxToolCalls: 1},
		Tools: config.Tools{
			Timeout:        config.Duration(time.Second),
			MaxResultBytes: 1024,
		},
	}
	resolver := buildToolsResolver(cfg, registry)

	var runs []agentcontracts.ToolRun
	tools := resolver(context.Background(), agentcontracts.Request{
		PermissionOverrides: map[string]bool{"test.admin": false},
		OnToolRun: func(run agentcontracts.ToolRun) {
			runs = append(runs, run)
		},
	})
	if len(tools) != 1 {
		t.Fatalf("exposed tools = %d, want 1 after revoking test.admin", len(tools))
	}
	invokable, ok := tools[0].(tool.InvokableTool)
	if !ok {
		t.Fatalf("tool does not implement InvokableTool: %T", tools[0])
	}

	if _, err := invokable.InvokableRun(context.Background(), "{}"); err != nil {
		t.Fatalf("first call: %v", err)
	}
	out, err := invokable.InvokableRun(context.Background(), "{}")
	if err != nil {
		t.Fatalf("budget call must not fail: %v", err)
	}
	if !strings.Contains(out, "上限") {
		t.Fatalf("budget call output = %q", out)
	}
	if calls != 1 {
		t.Fatalf("tool invoked %d times, want 1 within the budget", calls)
	}
	if len(runs) != 1 || runs[0].ToolID != "test.read" || runs[0].Status != pluginsapp.StatusOK {
		t.Fatalf("reported runs = %+v", runs)
	}
}
