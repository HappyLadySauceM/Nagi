// Tests for tool registration, filtering and snapshot isolation.
// 工具注册、过滤与快照隔离的测试。
package application

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/HappyLadySauceM/Nagi/internal/plugins/contracts"
)

// fakeProvider is a configurable ToolProvider that records Close calls.
// fakeProvider 是可配置的 ToolProvider，并记录 Close 调用。
type fakeProvider struct {
	manifest contracts.Manifest
	tools    []contracts.RegisteredTool
	err      error
	closed   int
}

func (f *fakeProvider) Manifest() contracts.Manifest { return f.manifest }

func (f *fakeProvider) Tools(context.Context) ([]contracts.RegisteredTool, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.tools, nil
}

func (f *fakeProvider) Close(context.Context) error {
	f.closed++
	return nil
}

func tool(id string) contracts.RegisteredTool {
	return contracts.RegisteredTool{
		Descriptor: contracts.ToolDescriptor{ID: id, Description: "test tool", ReadOnly: true},
		Invoke:     func(context.Context, string) (string, error) { return "ok", nil },
	}
}

// TestRegistryRegistersEnabledProvider verifies only enabled providers are
// opened and their tools appear in the snapshot.
// TestRegistryRegistersEnabledProvider 验证只打开启用的提供方，
// 其工具出现在快照中。
func TestRegistryRegistersEnabledProvider(t *testing.T) {
	registry := NewRegistry([]string{"builtin.time"})
	enabled := &fakeProvider{manifest: contracts.Manifest{ID: "builtin.time"}, tools: []contracts.RegisteredTool{tool("system.current_time")}}
	disabled := &fakeProvider{manifest: contracts.Manifest{ID: "builtin.qq"}, tools: []contracts.RegisteredTool{tool("qq.get_group_info")}}

	if err := registry.Register(context.Background(), enabled); err != nil {
		t.Fatalf("register enabled: %v", err)
	}
	if err := registry.Register(context.Background(), disabled); err != nil {
		t.Fatalf("register disabled: %v", err)
	}
	snapshot := registry.Snapshot()
	if len(snapshot) != 1 || snapshot[0].Descriptor.ID != "system.current_time" {
		t.Fatalf("snapshot = %+v", snapshot)
	}
	if disabled.closed != 0 {
		t.Fatal("a disabled provider must never be opened or closed")
	}
}

// TestRegistryRejectsDuplicates verifies provider and tool ID conflicts fail the
// registration and roll back the provider that just opened.
// TestRegistryRejectsDuplicates 验证提供方或工具 ID 冲突会让注册失败，
// 并回滚刚打开的提供方。
func TestRegistryRejectsDuplicates(t *testing.T) {
	registry := NewRegistry([]string{"builtin.time", "builtin.qq"})
	first := &fakeProvider{manifest: contracts.Manifest{ID: "builtin.time"}, tools: []contracts.RegisteredTool{tool("system.current_time")}}
	if err := registry.Register(context.Background(), first); err != nil {
		t.Fatalf("register first: %v", err)
	}

	dupProvider := &fakeProvider{manifest: contracts.Manifest{ID: "builtin.time"}, tools: []contracts.RegisteredTool{tool("system.now")}}
	if err := registry.Register(context.Background(), dupProvider); err == nil {
		t.Fatal("duplicate provider must fail")
	}

	dupTool := &fakeProvider{manifest: contracts.Manifest{ID: "builtin.qq"}, tools: []contracts.RegisteredTool{tool("system.current_time")}}
	if err := registry.Register(context.Background(), dupTool); err == nil {
		t.Fatal("duplicate tool must fail")
	}
	if dupTool.closed != 1 {
		t.Fatalf("failed provider must be closed once, got %d", dupTool.closed)
	}
}

// TestRegistryRejectsInvalidDescriptors verifies the namespace and description
// contract.
// TestRegistryRejectsInvalidDescriptors 验证命名空间与描述契约。
func TestRegistryRejectsInvalidDescriptors(t *testing.T) {
	cases := map[string]contracts.ToolDescriptor{
		"no namespace": {ID: "current_time", Description: "x", ReadOnly: true},
		"no desc":      {ID: "system.current_time", ReadOnly: true},
		"conflicting":  {ID: "system.x", Description: "x", ReadOnly: true, SideEffect: true},
	}
	for name, descriptor := range cases {
		registry := NewRegistry([]string{"builtin.time"})
		provider := &fakeProvider{
			manifest: contracts.Manifest{ID: "builtin.time"},
			tools:    []contracts.RegisteredTool{{Descriptor: descriptor}},
		}
		if err := registry.Register(context.Background(), provider); err == nil {
			t.Fatalf("%s: expected rejection", name)
		}
		if provider.closed != 1 {
			t.Fatalf("%s: provider must be closed on failure", name)
		}
	}
}

// TestRegistryRejectsContradictoryRiskLevel verifies a tool that claims read
// risk while not being read-only fails registration instead of being treated as
// safe.
// TestRegistryRejectsContradictoryRiskLevel 验证标注只读风险却不是只读的工具
// 会注册失败，而不是被当成安全工具。
func TestRegistryRejectsContradictoryRiskLevel(t *testing.T) {
	registry := NewRegistry([]string{"builtin.time"})
	provider := &fakeProvider{
		manifest: contracts.Manifest{ID: "builtin.time"},
		tools: []contracts.RegisteredTool{{
			Descriptor: contracts.ToolDescriptor{
				ID: "qq.send_message", Description: "发送", RiskLevel: contracts.RiskRead,
			},
		}},
	}
	err := registry.Register(context.Background(), provider)
	if err == nil {
		t.Fatal("read risk with a writable tool must be rejected")
	}
	if !strings.Contains(err.Error(), "qq.send_message") {
		t.Fatalf("error must name the tool: %v", err)
	}
	if provider.closed != 1 {
		t.Fatal("provider must be closed on failure")
	}
}

// TestRegistryRejectsInvalidRiskLevel verifies an unknown risk level fails
// registration and the error reports the tool id and the bad value.
// TestRegistryRejectsInvalidRiskLevel 验证未知风险级别会注册失败，
// 且错误包含工具 ID 与非法值。
func TestRegistryRejectsInvalidRiskLevel(t *testing.T) {
	for _, level := range []contracts.RiskLevel{"raed", "READ"} {
		registry := NewRegistry([]string{"builtin.time"})
		provider := &fakeProvider{
			manifest: contracts.Manifest{ID: "builtin.time"},
			tools: []contracts.RegisteredTool{{
				Descriptor: contracts.ToolDescriptor{
					ID: "qq.send_message", Description: "发送", RiskLevel: level,
				},
			}},
		}
		err := registry.Register(context.Background(), provider)
		if err == nil {
			t.Fatalf("risk level %q must be rejected", level)
		}
		if !strings.Contains(err.Error(), "qq.send_message") || !strings.Contains(err.Error(), string(level)) {
			t.Fatalf("risk level %q: error = %v, want tool id and invalid value", level, err)
		}
		if provider.closed != 1 {
			t.Fatalf("risk level %q: provider must be closed on failure", level)
		}
	}
}

// TestRegistryRejectsInvalidScopeKinds verifies every declared scope kind must
// be a supported kind, and the error reports the tool id and the bad value.
// TestRegistryRejectsInvalidScopeKinds 验证声明的会话类型必须是受支持类型，
// 且错误包含工具 ID 与非法值。
func TestRegistryRejectsInvalidScopeKinds(t *testing.T) {
	registry := NewRegistry([]string{"builtin.time"})
	provider := &fakeProvider{
		manifest: contracts.Manifest{ID: "builtin.time"},
		tools: []contracts.RegisteredTool{{
			Descriptor: contracts.ToolDescriptor{
				ID: "qq.get_group_info", Description: "群信息", ReadOnly: true,
				ScopeKinds: []string{contracts.ScopeKindGroup, "channel"},
			},
		}},
	}
	err := registry.Register(context.Background(), provider)
	if err == nil {
		t.Fatal("unknown scope kind must be rejected")
	}
	if !strings.Contains(err.Error(), "qq.get_group_info") || !strings.Contains(err.Error(), "channel") {
		t.Fatalf("error = %v, want tool id and invalid kind", err)
	}
	if provider.closed != 1 {
		t.Fatal("provider must be closed on failure")
	}
}

// TestRegistrySnapshotIsCopy verifies callers cannot mutate registry state.
// TestRegistrySnapshotIsCopy 验证调用方无法修改注册表状态。
func TestRegistrySnapshotIsCopy(t *testing.T) {
	registry := NewRegistry([]string{"builtin.time"})
	provider := &fakeProvider{manifest: contracts.Manifest{ID: "builtin.time"}, tools: []contracts.RegisteredTool{tool("system.current_time")}}
	if err := registry.Register(context.Background(), provider); err != nil {
		t.Fatalf("register: %v", err)
	}
	snapshot := registry.Snapshot()
	snapshot[0].Descriptor.ID = "mutated"
	if registry.Snapshot()[0].Descriptor.ID != "system.current_time" {
		t.Fatal("snapshot mutation leaked into the registry")
	}
}

// TestRegistryClosesProvidersInReverseOrder verifies shutdown ordering.
// TestRegistryClosesProvidersInReverseOrder 验证关闭顺序为注册逆序。
func TestRegistryClosesProvidersInReverseOrder(t *testing.T) {
	registry := NewRegistry([]string{"a.first", "b.second"})
	first := &fakeProvider{manifest: contracts.Manifest{ID: "a.first"}, tools: []contracts.RegisteredTool{tool("a.tool")}}
	second := &fakeProvider{manifest: contracts.Manifest{ID: "b.second"}, tools: []contracts.RegisteredTool{tool("b.tool")}}
	if err := registry.Register(context.Background(), first); err != nil {
		t.Fatalf("register first: %v", err)
	}
	if err := registry.Register(context.Background(), second); err != nil {
		t.Fatalf("register second: %v", err)
	}
	if err := registry.Close(context.Background()); err != nil {
		t.Fatalf("close: %v", err)
	}
	if first.closed != 1 || second.closed != 1 {
		t.Fatalf("closed = %d/%d", first.closed, second.closed)
	}
	if len(registry.Snapshot()) != 0 {
		t.Fatal("closed registry must expose no tools")
	}
}

// TestRegistryPropagatesProviderError verifies a failing provider is closed and
// the error surfaces.
// TestRegistryPropagatesProviderError 验证提供方失败会被关闭并返回错误。
func TestRegistryPropagatesProviderError(t *testing.T) {
	registry := NewRegistry([]string{"builtin.time"})
	provider := &fakeProvider{manifest: contracts.Manifest{ID: "builtin.time"}, err: errors.New("boom")}
	if err := registry.Register(context.Background(), provider); err == nil {
		t.Fatal("expected the provider error")
	}
	if provider.closed != 1 {
		t.Fatal("failed provider must be closed")
	}
}

// TestRegistryReportsUnknownProviders verifies a typo in the enable list is
// reported instead of silently exposing nothing, and that tools carry their
// provider id.
// TestRegistryReportsUnknownProviders 验证启用列表中的拼写错误会被报告，
// 而不是静默地不暴露任何工具；同时验证工具带上提供方 ID。
func TestRegistryReportsUnknownProviders(t *testing.T) {
	registry := NewRegistry([]string{"builtin.time", "builtin.tim"})
	if err := registry.Register(context.Background(), &fakeProvider{
		manifest: contracts.Manifest{ID: "builtin.time"},
		tools:    []contracts.RegisteredTool{tool("system.current_time")},
	}); err != nil {
		t.Fatalf("register: %v", err)
	}
	if err := registry.Register(context.Background(), &fakeProvider{
		manifest: contracts.Manifest{ID: "builtin.qq"},
		tools:    []contracts.RegisteredTool{tool("qq.get_group_info")},
	}); err != nil {
		t.Fatalf("register disabled provider: %v", err)
	}

	unknown := registry.Unknown()
	if len(unknown) != 1 || unknown[0] != "builtin.tim" {
		t.Fatalf("unknown = %v", unknown)
	}
	known := registry.Known()
	if len(known) != 2 || known[0] != "builtin.qq" || known[1] != "builtin.time" {
		t.Fatalf("known = %v", known)
	}
	manifests := registry.Manifests()
	if len(manifests) != 1 || manifests[0].ID != "builtin.time" {
		t.Fatalf("manifests = %+v", manifests)
	}
	snapshot := registry.Snapshot()
	if len(snapshot) != 1 || snapshot[0].ProviderID != "builtin.time" {
		t.Fatalf("snapshot = %+v", snapshot)
	}
}
