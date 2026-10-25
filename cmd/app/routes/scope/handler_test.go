// Tests for scope policy request validation.
// 作用域策略请求校验的测试。
package scope

import (
	"reflect"
	"testing"
)

// TestUnknownPermissions verifies that overrides outside the registered
// permission set are rejected, while an unavailable registry validates nothing.
// TestUnknownPermissions 验证已注册权限集合之外的覆盖项会被拒绝，
// 注册表不可用时则不做校验。
func TestUnknownPermissions(t *testing.T) {
	known := map[string]bool{"qq.group.read": true}

	if got := unknownPermissions(map[string]bool{"qq.group.read": false}, known); len(got) != 0 {
		t.Fatalf("known permission rejected: %v", got)
	}
	got := unknownPermissions(map[string]bool{"qq.admin.delete": true, "qq.group.read": true}, known)
	if !reflect.DeepEqual(got, []string{"qq.admin.delete"}) {
		t.Fatalf("unknown = %v", got)
	}
	if got := unknownPermissions(map[string]bool{"qq.admin.delete": true}, nil); got != nil {
		t.Fatalf("missing registry must skip validation, got %v", got)
	}
}

// TestRejectablePermissionsAllowsStoredKeys verifies that a permission removed
// from the catalogue can still be written back, while a brand-new unknown key
// is rejected.
// TestRejectablePermissionsAllowsStoredKeys 验证已从清单移除的权限仍可写回，
// 而全新的未知键仍被拒绝。
func TestRejectablePermissionsAllowsStoredKeys(t *testing.T) {
	unknown := []string{"qq.group.read", "qq.admin.delete"}
	stored := map[string]bool{"qq.group.read": false}

	got := rejectablePermissions(unknown, stored)
	if !reflect.DeepEqual(got, []string{"qq.admin.delete"}) {
		t.Fatalf("rejectable = %v", got)
	}
	if got := rejectablePermissions(unknown, nil); len(got) != len(unknown) {
		t.Fatalf("a missing stored policy must reject every unknown key, got %v", got)
	}
}
