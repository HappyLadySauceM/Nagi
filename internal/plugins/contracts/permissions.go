// Permission aliases and group-role requirements shared by tool descriptors
// and scope policies.
// 工具描述符与作用域策略共用的权限别名和群角色要求。
package contracts

// Group roles used by admin-only tools and the scope save-time guard.
// 群管工具与作用域保存守卫使用的群角色。
const (
	GroupRoleAdmin = "admin"
	GroupRoleOwner = "owner"
)

// Unified group-admin permission identifiers.
// 统一的群管权限标识。
const (
	PermissionGroupKick  = "qq.group.admin.kick"
	PermissionGroupMute  = "qq.group.admin.mute"
	PermissionGroupLeave = "qq.group.admin.leave"
)

var legacyPermissionAliases = map[string]string{
	"qq.api.set_group_kick":  PermissionGroupKick,
	"qq.api.set_group_ban":   PermissionGroupMute,
	"qq.api.set_group_leave": PermissionGroupLeave,
}

// CanonicalPermission maps a legacy OneBot permission to its unified group
// admin permission. Unknown values are returned unchanged.
// CanonicalPermission 将旧 OneBot 权限映射到统一群管权限；未知值原样返回。
func CanonicalPermission(permission string) string {
	if alias, ok := legacyPermissionAliases[permission]; ok {
		return alias
	}
	return permission
}

// NormalizePermissionOverrides rewrites legacy permission keys to their
// canonical names. An explicit canonical key wins when both are present, and
// an empty/nil map stays nil so policy serialization keeps the "no overrides"
// distinction.
// NormalizePermissionOverrides 将旧权限键改写为统一名称；新旧同时存在时以新键为准；
// 空/nil 映射保持 nil，以保留"无覆盖"的语义。
func NormalizePermissionOverrides(overrides map[string]bool) map[string]bool {
	if len(overrides) == 0 {
		return nil
	}
	out := make(map[string]bool, len(overrides))
	for permission, granted := range overrides {
		out[CanonicalPermission(permission)] = granted
	}
	for legacy, canonical := range legacyPermissionAliases {
		if granted, ok := overrides[canonical]; ok {
			out[canonical] = granted
			continue
		}
		if granted, ok := overrides[legacy]; ok {
			out[canonical] = granted
		}
	}
	return out
}

// GroupRoleRank orders roles for combining multiple tool requirements.
// GroupRoleRank 为合并多个工具要求时的角色排序。
func GroupRoleRank(role string) int {
	switch role {
	case GroupRoleOwner:
		return 2
	case GroupRoleAdmin:
		return 1
	default:
		return 0
	}
}

// GroupRoleSatisfies reports whether an actual role meets a required role.
// Owner satisfies admin; admin never satisfies owner; member satisfies nothing.
// GroupRoleSatisfies 报告实际角色是否满足要求：owner 满足 admin，admin 不满足 owner，
// member 不满足任何管理员要求。
func GroupRoleSatisfies(actual, required string) bool {
	switch required {
	case "":
		return true
	case GroupRoleAdmin:
		return actual == GroupRoleAdmin || actual == GroupRoleOwner
	case GroupRoleOwner:
		return actual == GroupRoleOwner
	default:
		return false
	}
}
