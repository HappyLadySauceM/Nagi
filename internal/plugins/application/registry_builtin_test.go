// Tests that the shipped built-in providers pass the registry contract checks.
// 验证二进制自带的内置提供方能够通过注册表契约校验。
package application_test

import (
	"context"
	replytool "github.com/HappyLadySauceM/Nagi/internal/agent/infrastructure/replytool"
	scopedomain "github.com/HappyLadySauceM/Nagi/internal/scope/domain"
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
	registry := application.NewRegistry()
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

func TestCompletePublicCatalogPreservesExistingTools(t *testing.T) {
	registry := application.NewRegistry()
	defer registry.Close(context.Background())
	providers := []contracts.ToolProvider{
		builtin.NewTimeProvider(),
		builtin.WithoutPublicDuplicates(builtin.NewQQProvider(nil)),
		builtin.WithoutPublicDuplicates(builtin.NewQQMemberProvider(nil)),
		builtin.WithoutPublicDuplicates(builtin.NewQQAdminProvider(nil)),
		builtin.WithoutPublicDuplicates(builtin.NewQQProfileProvider(nil)),
		builtin.NewPublicProvider(builtin.PublicOptions{}),
		replytool.NewProvider(replytool.Options{}),
	}
	for _, provider := range providers {
		if err := registry.Register(context.Background(), provider); err != nil {
			t.Fatal(err)
		}
	}
	tools := registry.Snapshot()
	if len(tools) != 43 {
		t.Fatalf("tools=%d", len(tools))
	}
	byID := map[string]contracts.ToolDescriptor{}
	grants := map[string]bool{}
	for _, tool := range tools {
		d := tool.Descriptor
		byID[d.ID] = d
		if d.Permission != "" {
			grants[d.Permission] = true
		}
		for _, permission := range d.AdditionalPermissions {
			grants[permission] = true
		}
	}
	for id, permission := range map[string]string{"qq.send_current": "qq.reply.current", "qq.get_group_info": "qq.group.read", "qq.get_group_member_info": "qq.group.member.read", "qq.mute_group_member": "qq.group.admin.mute", "qq.set_group_whole_ban": "qq.group.admin.wholeban", "qq.set_group_card": "qq.group.admin.card", "qq.kick_group_member": "qq.group.admin.kick", "qq.leave_current_group": "qq.group.admin.leave", "system.current_time": "system.time.read"} {
		if byID[id].Permission != permission {
			t.Fatalf("legacy permission changed: %s", id)
		}
	}
	policy := scopedomain.Policy{ScopeType: scopedomain.ScopeGroup, ScopeID: 1, ReplyMode: scopedomain.ModeMentionOnly, ToolPermissions: grants}
	if err := policy.Validate(); err != nil {
		t.Fatalf("cannot grant full catalog (%d permissions): %v", len(grants), err)
	}
}
