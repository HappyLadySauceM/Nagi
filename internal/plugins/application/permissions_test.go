// Tests for permission resolution and tool exposure.
// 权限解析与工具暴露的测试。
package application

import (
	"testing"

	"github.com/HappyLadySauceM/QQBot/internal/plugins/contracts"
)

// permissionTools is the fixture: one read-only and one side-effecting tool
// that require separate permissions.
// permissionTools 是测试夹具：一个只读工具与一个带副作用工具，各自需要独立权限。
func permissionTools() []contracts.RegisteredTool {
	return []contracts.RegisteredTool{
		{Descriptor: contracts.ToolDescriptor{ID: "qq.get_group_info", ReadOnly: true, Permission: "qq.group.read"}},
		{Descriptor: contracts.ToolDescriptor{ID: "qq.send_message", Permission: "qq.message.write"}},
	}
}

// TestResolvePermissionsDefaultsToReadOnly verifies the fallback baseline: a
// read-only tool is granted without configuration and a side-effecting tool is
// denied.
// TestResolvePermissionsDefaultsToReadOnly 验证兜底基线：未配置时只读工具允许，
// 带副作用工具拒绝。
func TestResolvePermissionsDefaultsToReadOnly(t *testing.T) {
	granted := ResolvePermissions(permissionTools(), nil, nil)
	if !granted["qq.group.read"] {
		t.Fatal("read-only permission must be granted by default")
	}
	if granted["qq.message.write"] {
		t.Fatal("side-effecting permission must be denied by default")
	}
}

// TestResolvePermissionsPrecedence verifies that configured defaults beat the
// descriptor fallback and scope overrides beat both.
// TestResolvePermissionsPrecedence 验证配置默认优先于描述符兜底，
// 作用域覆盖又优先于配置默认。
func TestResolvePermissionsPrecedence(t *testing.T) {
	configDefaults := map[string]bool{"qq.group.read": false, "qq.message.write": true}
	granted := ResolvePermissions(permissionTools(), configDefaults, nil)
	if granted["qq.group.read"] || !granted["qq.message.write"] {
		t.Fatalf("configured defaults ignored: %+v", granted)
	}

	overrides := map[string]bool{"qq.group.read": true, "qq.message.write": false}
	granted = ResolvePermissions(permissionTools(), configDefaults, overrides)
	if !granted["qq.group.read"] || granted["qq.message.write"] {
		t.Fatalf("scope overrides ignored: %+v", granted)
	}
}

// TestResolvePermissionsSharedPermissionUsesStrictestFallback verifies that a
// permission shared by a read-only and a side-effecting tool is denied by
// default regardless of registration order.
// TestResolvePermissionsSharedPermissionUsesStrictestFallback 验证只读工具与
// 副作用工具共用权限时，无论注册顺序如何，默认都取最严格的拒绝。
func TestResolvePermissionsSharedPermissionUsesStrictestFallback(t *testing.T) {
	shared := "qq.group.admin"
	readFirst := []contracts.RegisteredTool{
		{Descriptor: contracts.ToolDescriptor{ID: "qq.group.info", ReadOnly: true, Permission: shared}},
		{Descriptor: contracts.ToolDescriptor{ID: "qq.group.mute", Permission: shared}},
	}
	writeFirst := []contracts.RegisteredTool{readFirst[1], readFirst[0]}

	for name, tools := range map[string][]contracts.RegisteredTool{
		"read-first":  readFirst,
		"write-first": writeFirst,
	} {
		if ResolvePermissions(tools, nil, nil)[shared] {
			t.Fatalf("%s: shared permission must be denied by default", name)
		}
	}
}

// TestResolvePermissionsIgnoresUnknownOverrides verifies that an override for a
// permission no tool declares cannot widen the granted set.
// TestResolvePermissionsIgnoresUnknownOverrides 验证没有工具声明的权限覆盖
// 不会扩大授予集合。
func TestResolvePermissionsIgnoresUnknownOverrides(t *testing.T) {
	granted := ResolvePermissions(permissionTools(), nil, map[string]bool{"qq.admin.delete": true})
	if granted["qq.admin.delete"] {
		t.Fatalf("unknown override leaked into the granted set: %+v", granted)
	}
}

// TestAllowedHonorsSideEffects verifies exposure rules: side effects stay hidden
// until enabled, and a granted permission is required for permission-gated tools.
// TestAllowedHonorsSideEffects 验证暴露规则：副作用工具在开启前始终隐藏，
// 需要权限的工具必须已授予权限。
func TestAllowedHonorsSideEffects(t *testing.T) {
	readOnly := contracts.ToolDescriptor{ID: "qq.get_group_info", ReadOnly: true, Permission: "qq.group.read"}
	writer := contracts.ToolDescriptor{ID: "qq.send_message", Permission: "qq.message.write"}
	granted := map[string]bool{"qq.group.read": true, "qq.message.write": true}

	if !Allowed(readOnly, granted, false) {
		t.Fatal("granted read-only tool must be exposed")
	}
	if Allowed(readOnly, map[string]bool{"qq.group.read": false}, false) {
		t.Fatal("revoked read-only tool must stay hidden")
	}
	if Allowed(writer, granted, false) {
		t.Fatal("side-effecting tool must stay hidden while side effects are disabled")
	}
	if !Allowed(writer, granted, true) {
		t.Fatal("side-effecting tool must be exposed once side effects are enabled and granted")
	}
	if Allowed(writer, map[string]bool{"qq.message.write": false}, true) {
		t.Fatal("side-effecting tool must stay hidden without its permission")
	}
}
