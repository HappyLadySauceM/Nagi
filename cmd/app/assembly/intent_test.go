// Tests for the tool resolver wiring: scope and permission filtering, call
// budget and run reporting happen in one place, so the assembly is worth
// testing directly.
// 工具解析装配的测试：会话类型与权限过滤、调用预算与运行上报集中在一处，
// 因此直接测试装配层。
package assembly

import (
	"context"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/cloudwego/eino/components/tool"

	agentcontracts "github.com/HappyLadySauceM/Nagi/internal/agent/contracts"
	conversationdomain "github.com/HappyLadySauceM/Nagi/internal/conversation/domain"
	pluginsapp "github.com/HappyLadySauceM/Nagi/internal/plugins/application"
	plugincontracts "github.com/HappyLadySauceM/Nagi/internal/plugins/contracts"
	scopedomain "github.com/HappyLadySauceM/Nagi/internal/scope/domain"
	"github.com/HappyLadySauceM/Nagi/pkg/config"
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
	resolver := buildToolsResolver(cfg, registry, nil)

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

// exposedToolNames returns the model-facing names of the resolved tools.
// exposedToolNames 返回解析后工具在模型侧的命名。
func exposedToolNames(t *testing.T, exposed []tool.BaseTool) []string {
	t.Helper()
	names := make([]string, 0, len(exposed))
	for _, base := range exposed {
		info, err := base.Info(context.Background())
		if err != nil {
			t.Fatalf("tool info: %v", err)
		}
		names = append(names, info.Name)
	}
	sort.Strings(names)
	return names
}

// TestToolScopeKindsMatchDomainKinds pins the plugin scope kinds to the
// conversation and scope domain values they are compared against at runtime.
// TestToolScopeKindsMatchDomainKinds 固定插件会话类型与运行时比较的
// conversation/scope 领域取值一致。
func TestToolScopeKindsMatchDomainKinds(t *testing.T) {
	if plugincontracts.ScopeKindPrivate != string(conversationdomain.KindPrivate) ||
		plugincontracts.ScopeKindGroup != string(conversationdomain.KindGroup) {
		t.Fatalf("plugin kinds %q/%q do not match conversation kinds %q/%q",
			plugincontracts.ScopeKindPrivate, plugincontracts.ScopeKindGroup,
			conversationdomain.KindPrivate, conversationdomain.KindGroup)
	}
	if plugincontracts.ScopeKindPrivate != string(scopedomain.ScopePrivate) ||
		plugincontracts.ScopeKindGroup != string(scopedomain.ScopeGroup) {
		t.Fatalf("plugin kinds %q/%q do not match scope kinds %q/%q",
			plugincontracts.ScopeKindPrivate, plugincontracts.ScopeKindGroup,
			scopedomain.ScopePrivate, scopedomain.ScopeGroup)
	}
}

// TestBuildToolsResolverFiltersByConversationKind verifies a group-only tool is
// hidden from private runs while tools without ScopeKinds keep their previous
// visibility in both kinds. An empty kind stays fail-closed for restricted
// tools.
// TestBuildToolsResolverFiltersByConversationKind 验证仅群聊可用的工具在私聊运行中
// 被隐藏，而省略 ScopeKinds 的工具在两种会话中都保持原有可见性；
// 空会话类型对限定范围的工具保持关闭式失败。
func TestBuildToolsResolverFiltersByConversationKind(t *testing.T) {
	registry := pluginsapp.NewRegistry([]string{"test.fake"})
	provider := fakeProvider{tools: []plugincontracts.RegisteredTool{
		{
			Descriptor: plugincontracts.ToolDescriptor{
				ID: "system.current_time", Description: "时间", ReadOnly: true,
			},
			Invoke: func(context.Context, string) (string, error) { return "ok", nil },
		},
		{
			Descriptor: plugincontracts.ToolDescriptor{
				ID: "qq.get_group_info", Description: "群信息", ReadOnly: true,
				Permission: "qq.group.read",
				ScopeKinds: []string{plugincontracts.ScopeKindGroup},
			},
			Invoke: func(context.Context, string) (string, error) { return "group", nil },
		},
	}}
	if err := registry.Register(context.Background(), provider); err != nil {
		t.Fatalf("register: %v", err)
	}
	t.Cleanup(func() { _ = registry.Close(context.Background()) })

	cfg := &config.Config{}
	resolver := buildToolsResolver(cfg, registry, nil)

	cases := []struct {
		name string
		kind string
		want []string
	}{
		{
			name: "private hides group tools",
			kind: plugincontracts.ScopeKindPrivate,
			want: []string{"system_current_time"},
		},
		{
			name: "group exposes group tools",
			kind: plugincontracts.ScopeKindGroup,
			want: []string{"qq_get_group_info", "system_current_time"},
		},
		{
			name: "empty kind hides group tools",
			kind: "",
			want: []string{"system_current_time"},
		},
	}
	for _, tc := range cases {
		got := exposedToolNames(t, resolver(context.Background(), agentcontracts.Request{Kind: tc.kind}))
		if !reflect.DeepEqual(got, tc.want) {
			t.Fatalf("%s: exposed tools = %v, want %v", tc.name, got, tc.want)
		}
	}
}

// TestReplyToolRequiresDedicatedSwitch verifies the send tool stays hidden when
// only the generic side-effect flag is on, and becomes visible once the
// dedicated switch and the scope grant are both present.
// TestReplyToolRequiresDedicatedSwitch 验证只打开通用副作用开关时发送工具仍不可见，
// 只有专用开关与作用域授权同时具备才暴露。
func TestReplyToolRequiresDedicatedSwitch(t *testing.T) {
	registry := pluginsapp.NewRegistry([]string{"test.fake"})
	provider := fakeProvider{tools: []plugincontracts.RegisteredTool{{
		Descriptor: plugincontracts.ToolDescriptor{
			ID: "qq.send_current", Description: "发送", ReadOnly: false, SideEffect: true,
			RiskLevel: plugincontracts.RiskWrite, Permission: "qq.reply.current",
		},
		Invoke: func(context.Context, string) (string, error) { return "sent", nil },
	}}}
	if err := registry.Register(context.Background(), provider); err != nil {
		t.Fatalf("register: %v", err)
	}
	t.Cleanup(func() { _ = registry.Close(context.Background()) })

	overrides := map[string]bool{"qq.reply.current": true}
	sideEffectsOnly := &config.Config{}
	sideEffectsOnly.Tools.AllowSideEffects = true
	if got := exposedToolNames(t, buildToolsResolver(sideEffectsOnly, registry, nil)(
		context.Background(), agentcontracts.Request{Kind: plugincontracts.ScopeKindGroup, PermissionOverrides: overrides},
	)); len(got) != 0 {
		t.Fatalf("side effects alone exposed %v, want none", got)
	}

	full := &config.Config{}
	full.Tools.AllowSideEffects = true
	full.Tools.AllowSendCurrent = true
	if got := exposedToolNames(t, buildToolsResolver(full, registry, nil)(
		context.Background(), agentcontracts.Request{Kind: plugincontracts.ScopeKindGroup, PermissionOverrides: overrides},
	)); !reflect.DeepEqual(got, []string{"qq_send_current"}) {
		t.Fatalf("exposed = %v, want qq_send_current", got)
	}

	// Without the scope grant the tool stays denied even with both switches on.
	if got := exposedToolNames(t, buildToolsResolver(full, registry, nil)(
		context.Background(), agentcontracts.Request{Kind: plugincontracts.ScopeKindGroup},
	)); len(got) != 0 {
		t.Fatalf("missing grant exposed %v, want none", got)
	}
}

// TestSendingPolicyVetoesReplyTool verifies a per-scope policy row can hide the
// reply tool even when the global switch is on.
// TestSendingPolicyVetoesReplyTool 验证按作用域的策略行能在全局开关打开时隐藏回复工具。
func TestSendingPolicyVetoesReplyTool(t *testing.T) {
	registry := pluginsapp.NewRegistry([]string{"test.fake"})
	provider := fakeProvider{tools: []plugincontracts.RegisteredTool{{
		Descriptor: plugincontracts.ToolDescriptor{
			ID: "qq.send_current", Description: "发送", ReadOnly: false, SideEffect: true,
			RiskLevel: plugincontracts.RiskWrite, Permission: "qq.reply.current",
		},
		Invoke: func(context.Context, string) (string, error) { return "sent", nil },
	}}}
	if err := registry.Register(context.Background(), provider); err != nil {
		t.Fatalf("register: %v", err)
	}
	t.Cleanup(func() { _ = registry.Close(context.Background()) })

	cfg := &config.Config{}
	cfg.Tools.AllowSideEffects = true
	cfg.Tools.AllowSendCurrent = true
	req := agentcontracts.Request{Kind: plugincontracts.ScopeKindGroup,
		PermissionOverrides: map[string]bool{"qq.reply.current": true}}

	veto := func(context.Context, int64, string, int64) (bool, bool) { return false, true }
	if got := exposedToolNames(t, buildToolsResolver(cfg, registry, veto)(context.Background(), req)); len(got) != 0 {
		t.Fatalf("policy veto exposed %v, want none", got)
	}
	allow := func(context.Context, int64, string, int64) (bool, bool) { return true, true }
	if got := exposedToolNames(t, buildToolsResolver(cfg, registry, allow)(context.Background(), req)); !reflect.DeepEqual(got, []string{"qq_send_current"}) {
		t.Fatalf("policy allow exposed %v", got)
	}
}
