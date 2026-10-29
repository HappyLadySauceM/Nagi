// Tests that the shipped built-in providers pass the registry contract checks.
// 验证二进制自带的内置提供方能够通过注册表契约校验。
package application_test

import (
	"context"
	"testing"

	"github.com/HappyLadySauceM/Nagi/internal/plugins/application"
	"github.com/HappyLadySauceM/Nagi/internal/plugins/contracts"
	"github.com/HappyLadySauceM/Nagi/internal/plugins/infrastructure/builtin"
)

// TestRegistryRegistersBuiltinProviders verifies the descriptors shipped with
// the binary satisfy the namespace, risk and scope checks, so a stricter
// contract cannot silently reject them at startup.
// TestRegistryRegistersBuiltinProviders 验证随二进制发布的内置描述符满足
// 命名空间、风险级别与会话类型校验，避免更严格的契约在启动时静默拒绝它们。
func TestRegistryRegistersBuiltinProviders(t *testing.T) {
	registry := application.NewRegistry([]string{"builtin.qq", "builtin.time"})
	t.Cleanup(func() { _ = registry.Close(context.Background()) })

	providers := []contracts.ToolProvider{
		builtin.NewTimeProvider(),
		builtin.NewQQProvider(nil),
	}
	for _, provider := range providers {
		if err := registry.Register(context.Background(), provider); err != nil {
			t.Fatalf("register %s: %v", provider.Manifest().ID, err)
		}
	}

	if manifests := registry.Manifests(); len(manifests) != 2 {
		t.Fatalf("manifests = %+v, want 2 providers", manifests)
	}
	snapshot := registry.Snapshot()
	if len(snapshot) != 2 {
		t.Fatalf("registered tools = %d, want 2", len(snapshot))
	}
	byID := make(map[string]contracts.RegisteredTool, len(snapshot))
	for _, registered := range snapshot {
		byID[registered.Descriptor.ID] = registered
	}

	timeTool, ok := byID["system.current_time"]
	if !ok {
		t.Fatalf("snapshot = %+v, want system.current_time", snapshot)
	}
	if timeTool.ProviderID != "builtin.time" || timeTool.Descriptor.Category != contracts.CategorySystem ||
		timeTool.Descriptor.RiskLevel != contracts.RiskRead || !timeTool.Descriptor.ReadOnly ||
		len(timeTool.Descriptor.ScopeKinds) != 0 {
		t.Fatalf("time descriptor = %+v", timeTool)
	}

	qqTool, ok := byID["qq.get_group_info"]
	if !ok {
		t.Fatalf("snapshot = %+v, want qq.get_group_info", snapshot)
	}
	if qqTool.ProviderID != "builtin.qq" || qqTool.Descriptor.Category != contracts.CategoryGroup ||
		qqTool.Descriptor.RiskLevel != contracts.RiskRead || !qqTool.Descriptor.ReadOnly ||
		qqTool.Descriptor.Permission != "qq.group.read" ||
		len(qqTool.Descriptor.ScopeKinds) != 1 || qqTool.Descriptor.ScopeKinds[0] != contracts.ScopeKindGroup {
		t.Fatalf("qq descriptor = %+v", qqTool)
	}
}
