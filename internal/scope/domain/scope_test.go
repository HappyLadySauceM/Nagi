// Tests for the scope policy domain model.
// 作用域策略领域模型的测试。
package domain

import (
	"fmt"
	"testing"
)

// TestDefaultPolicyDeniesEverything verifies that Default disables participation,
// uses mention_only and leaves storage and memory off.
// TestDefaultPolicyDeniesEverything 验证 Default 关闭参与、使用 mention_only
// 且不开启存储与记忆。
func TestDefaultPolicyDeniesEverything(t *testing.T) {
	p := Default(ScopeGroup, 10086)
	if p.Enabled {
		t.Fatal("default policy must not be enabled")
	}
	if p.ReplyMode != ModeMentionOnly {
		t.Fatalf("default reply mode = %q, want mention_only", p.ReplyMode)
	}
	if p.StoreMessages || p.AllowMemoryWrite || p.AllowHistoryRead {
		t.Fatal("default policy must not store messages or allow memory")
	}
	filled := p.WithDefaults()
	if filled.MinReplyIntervalMS != 10000 || filled.MaxRepliesPerHour != 20 || filled.RetentionDays != 30 {
		t.Fatalf("WithDefaults numbers = %d/%d/%d", filled.MinReplyIntervalMS, filled.MaxRepliesPerHour, filled.RetentionDays)
	}
}

// TestPolicyValidateRejectsInvalidValues verifies validation of scope type,
// scope id and reply mode.
// TestPolicyValidateRejectsInvalidValues 验证作用域类型、ID 与回复模式的校验。
func TestPolicyValidateRejectsInvalidValues(t *testing.T) {
	cases := []struct {
		name   string
		policy Policy
	}{
		{"invalid type", Policy{ScopeType: "channel", ScopeID: 1, ReplyMode: ModeMentionOnly}},
		{"negative id", Policy{ScopeType: ScopeGroup, ScopeID: -1, ReplyMode: ModeMentionOnly}},
		{"invalid mode", Policy{ScopeType: ScopePrivate, ScopeID: 1, ReplyMode: "shout"}},
		{"negative numbers", Policy{ScopeType: ScopePrivate, ScopeID: 1, ReplyMode: ModeMentionOnly, RetentionDays: -1}},
	}
	for _, c := range cases {
		if err := c.policy.Validate(); err == nil {
			t.Fatalf("%s: expected validation error", c.name)
		}
	}
	valid := Policy{ScopeType: ScopeGroup, ScopeID: 1, ReplyMode: ModeAlwaysReply}
	if err := valid.Validate(); err != nil {
		t.Fatalf("valid policy rejected: %v", err)
	}
}

// TestPolicyValidateToolPermissions verifies that override keys must be
// namespaced lowercase identifiers and that the map size is bounded.
// TestPolicyValidateToolPermissions 验证覆盖键必须是带命名空间的小写标识，
// 且映射大小受限。
func TestPolicyValidateToolPermissions(t *testing.T) {
	base := func(overrides map[string]bool) Policy {
		return Policy{ScopeType: ScopeGroup, ScopeID: 1, ReplyMode: ModeMentionOnly, ToolPermissions: overrides}
	}
	if err := base(map[string]bool{"qq.group.read": false, "qq.message.write": true}).Validate(); err != nil {
		t.Fatalf("valid overrides rejected: %v", err)
	}
	for name, overrides := range map[string]map[string]bool{
		"uppercase":    {"QQ.Group.Read": true},
		"no namespace": {"current_time": true},
		"empty":        {"": true},
	} {
		if err := base(overrides).Validate(); err == nil {
			t.Fatalf("%s: expected validation error", name)
		}
	}
	tooMany := make(map[string]bool, maxToolPermissions+1)
	for i := 0; i <= maxToolPermissions; i++ {
		tooMany[fmt.Sprintf("test.permission%d", i)] = true
	}
	if err := base(tooMany).Validate(); err == nil {
		t.Fatal("too many overrides: expected validation error")
	}
	// WithDefaults must not drop administrator overrides.
	// WithDefaults 不得丢弃管理员的覆盖项。
	p := base(map[string]bool{"qq.group.read": false}).WithDefaults()
	if granted, ok := p.ToolPermissions["qq.group.read"]; !ok || granted {
		t.Fatalf("WithDefaults changed overrides: %+v", p.ToolPermissions)
	}
}
