// Tests that the shipped built-in providers pass the registry contract checks.
// 验证二进制自带的内置提供方能够通过注册表契约校验。
package application_test

import (
	"context"
	"strings"
	"testing"

	scopedomain "github.com/HappyLadySauceM/Nagi/internal/scope/domain"

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
	}
	for _, provider := range providers {
		if err := registry.Register(context.Background(), provider); err != nil {
			t.Fatal(err)
		}
	}
	tools := registry.Snapshot()
	if len(tools) != 35 {
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
	for id, permission := range map[string]string{"qq.send_msg": "qq.reply.current", "qq.get_group_info": "qq.group.read", "qq.get_group_member_info": "qq.group.member.read", "qq.set_group_ban": "qq.group.admin.mute", "qq.set_group_whole_ban": "qq.group.admin.wholeban", "qq.set_group_card": "qq.group.admin.card", "qq.set_group_kick": "qq.group.admin.kick", "qq.set_group_leave": "qq.group.admin.leave", "system.current_time": "system.time.read"} {
		if byID[id].Permission != permission {
			t.Fatalf("legacy permission changed: %s", id)
		}
	}
	for _, obsolete := range []string{"qq.kick_group_member", "qq.mute_group_member", "qq.leave_current_group"} {
		if _, ok := byID[obsolete]; ok {
			t.Fatalf("duplicate tool still registered: %s", obsolete)
		}
	}
	counts := map[string]int{}
	for _, tool := range tools {
		counts[strings.TrimPrefix(tool.Descriptor.ID, "qq.")]++
	}
	for _, action := range []string{"set_group_kick", "set_group_ban", "set_group_leave"} {
		if counts[action] != 1 {
			t.Fatalf("action %s registered %d times", action, counts[action])
		}
	}
	for id, role := range map[string]string{"qq.set_group_kick": "admin", "qq.set_group_ban": "admin", "qq.set_group_whole_ban": "admin", "qq.set_group_card": "admin", "qq.set_group_name": "admin", "qq.set_group_anonymous": "admin", "qq.set_group_admin": "owner", "qq.set_group_special_title": "owner"} {
		if byID[id].RequiresGroupRole != role {
			t.Fatalf("role requirement %s = %q, want %q", id, byID[id].RequiresGroupRole, role)
		}
	}
	policy := scopedomain.Policy{ScopeType: scopedomain.ScopeGroup, ScopeID: 1, ReplyMode: scopedomain.ModeMentionOnly, ToolPermissions: grants}
	if err := policy.Validate(); err != nil {
		t.Fatalf("cannot grant full catalog (%d permissions): %v", len(grants), err)
	}
}
