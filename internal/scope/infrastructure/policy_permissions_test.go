package infrastructure

import (
	"encoding/json"
	"testing"
)

// TestDecodeToolPermissionsPreservesFalse guards JSONB false entries from being dropped.
// TestDecodeToolPermissionsPreservesFalse 防止 JSONB 中的 false 权限项被丢弃。
func TestDecodeToolPermissionsPreservesFalse(t *testing.T) {
	raw, err := encodeToolPermissions(map[string]bool{"qq.group.read": false, "qq.message.write": true})
	if err != nil {
		t.Fatal(err)
	}
	permissions, err := decodeToolPermissions(raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(permissions) != 2 || permissions["qq.group.read"] || !permissions["qq.message.write"] {
		t.Fatalf("permissions = %+v", permissions)
	}
	round, err := decodeToolPermissions([]byte(`{"qq.group.read":false,"qq.message.write":true}`))
	if err != nil || len(round) != 2 || round["qq.group.read"] {
		t.Fatalf("json round trip = %+v, err=%v", round, err)
	}
	var again map[string]bool
	if json.Unmarshal(raw, &again) != nil || again["qq.group.read"] {
		t.Fatalf("encoded json = %+v", again)
	}
}
