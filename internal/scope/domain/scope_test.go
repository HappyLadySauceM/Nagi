// Tests for the scope policy domain model.
// 作用域策略领域模型的测试。
package domain

import "testing"

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
		{"zero id", Policy{ScopeType: ScopeGroup, ScopeID: 0, ReplyMode: ModeMentionOnly}},
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
