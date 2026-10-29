// Tests for the descriptor metadata that decides where a tool may appear.
// 描述符元数据测试：该元数据决定工具可以出现在哪些会话中。
package contracts

import "testing"

// TestToolDescriptorAppliesTo verifies the conversation-kind filter: an omitted
// ScopeKinds keeps a tool visible everywhere, a restricted tool matches only
// its own kinds, and a run without a kind never reaches a restricted tool.
// TestToolDescriptorAppliesTo 验证会话类型过滤：省略 ScopeKinds 时工具在所有
// 类型中可见，限定类型的工具只匹配自身类型，类型未知的运行无法触达限定工具。
func TestToolDescriptorAppliesTo(t *testing.T) {
	cases := []struct {
		name  string
		kinds []string
		kind  string
		want  bool
	}{
		{"omitted in private", nil, ScopeKindPrivate, true},
		{"omitted in group", nil, ScopeKindGroup, true},
		{"group tool in group", []string{ScopeKindGroup}, ScopeKindGroup, true},
		{"group tool in private", []string{ScopeKindGroup}, ScopeKindPrivate, false},
		{"private tool in group", []string{ScopeKindPrivate}, ScopeKindGroup, false},
		{"group tool without a kind", []string{ScopeKindGroup}, "", false},
		{"both kinds in private", []string{ScopeKindGroup, ScopeKindPrivate}, ScopeKindPrivate, true},
	}
	for _, tc := range cases {
		descriptor := ToolDescriptor{ID: "test.tool", Description: "test", ScopeKinds: tc.kinds}
		if got := descriptor.AppliesTo(tc.kind); got != tc.want {
			t.Fatalf("%s: AppliesTo(%q) = %v, want %v", tc.name, tc.kind, got, tc.want)
		}
	}
}

// TestToolDescriptorEffectiveRiskLevel verifies the zero value is derived from
// ReadOnly so a forgotten field never downgrades a side-effecting tool.
// TestToolDescriptorEffectiveRiskLevel 验证零值按 ReadOnly 推导，
// 漏填字段不会把有副作用的工具降级为只读。
func TestToolDescriptorEffectiveRiskLevel(t *testing.T) {
	cases := map[string]struct {
		declared RiskLevel
		readOnly bool
		want     RiskLevel
	}{
		"zero value of read-only tool": {"", true, RiskRead},
		"zero value of writer":         {"", false, RiskWrite},
		"read":                         {RiskRead, true, RiskRead},
		"write":                        {RiskWrite, false, RiskWrite},
		"admin":                        {RiskAdmin, false, RiskAdmin},
	}
	for name, tc := range cases {
		descriptor := ToolDescriptor{
			ID: "test.tool", Description: "test", RiskLevel: tc.declared, ReadOnly: tc.readOnly,
		}
		if got := descriptor.EffectiveRiskLevel(); got != tc.want {
			t.Fatalf("%s: EffectiveRiskLevel() = %q, want %q", name, got, tc.want)
		}
	}
}
