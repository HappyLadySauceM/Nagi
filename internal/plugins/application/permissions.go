package application

import (
	"github.com/HappyLadySauceM/QQBot/internal/plugins/contracts"
)

// ResolvePermissions computes the granted permission set for one run.
// Precedence is scope override, then configured default, then the descriptor
// fallback: read-only tools are allowed and everything else is denied. When
// several tools share a permission, the fallback is the strictest one, so the
// registration order can never widen what a permission grants.
// ResolvePermissions 计算单次运行的授予权限集合。
// 优先级为作用域覆盖、配置默认、描述符兜底：只读工具允许，其余默认拒绝。
// 多个工具共用一个权限时取最严格的兜底，注册顺序因此不会放大权限。
func ResolvePermissions(tools []contracts.RegisteredTool, configDefaults, overrides map[string]bool) map[string]bool {
	fallback := make(map[string]bool, len(tools))
	for _, registered := range tools {
		permission := registered.Descriptor.Permission
		if permission == "" {
			continue
		}
		readOnly := registered.Descriptor.ReadOnly
		if previous, seen := fallback[permission]; seen {
			fallback[permission] = previous && readOnly
			continue
		}
		fallback[permission] = readOnly
	}
	granted := make(map[string]bool, len(fallback))
	for permission, readOnly := range fallback {
		allowed := readOnly
		if configured, exists := configDefaults[permission]; exists {
			allowed = configured
		}
		if override, exists := overrides[permission]; exists {
			allowed = override
		}
		granted[permission] = allowed
	}
	return granted
}

// Allowed reports whether one tool may be exposed to the model and executed.
// A granted permission is not enough for tools with side effects: they stay
// hidden until the process explicitly enables them.
// Allowed 报告单个工具是否可以暴露给模型并执行。
// 对带副作用的工具，仅有授予权限还不够：在进程显式开启前它们始终不可见。
func Allowed(descriptor contracts.ToolDescriptor, granted map[string]bool, allowSideEffects bool) bool {
	if !descriptor.ReadOnly && !allowSideEffects {
		return false
	}
	if descriptor.Permission == "" {
		return true
	}
	return granted[descriptor.Permission]
}
