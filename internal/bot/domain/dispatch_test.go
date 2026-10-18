package domain

import "testing"

func TestSelectKeepsRegistrationOrder(t *testing.T) {
	// 验证按注册顺序返回匹配项并过滤未匹配项。
	got := Select([]Descriptor{
		{Name: "a", Match: false},
		{Name: "b", Match: true},
		{Name: "c", Match: true},
	})
	if len(got) != 2 || got[0] != "b" || got[1] != "c" {
		t.Fatalf("select = %v", got)
	}
}

func TestSelectReturnsEmptyWhenNothingMatches(t *testing.T) {
	// 验证无人匹配时返回空切片。
	if got := Select([]Descriptor{{Name: "a", Match: false}}); len(got) != 0 {
		t.Fatalf("select = %v", got)
	}
}
