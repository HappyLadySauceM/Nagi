package domain

import "testing"

// TestComputeDedupeKeySupportsExactSubjectAndContent verifies the key separates
// subjects and does not case-fold or collapse internal whitespace.
// TestComputeDedupeKeySupportsExactSubjectAndContent 验证去重键区分主体，
// 且不做大小写折叠或内部空白折叠。
func TestComputeDedupeKeySupportsExactSubjectAndContent(t *testing.T) {
	base := ComputeDedupeKey(1, VisibilityGroup, "700", SubjectUser, "1001", "I like tea")
	if base == "" {
		t.Fatal("empty dedupe key")
	}
	if got := ComputeDedupeKey(1, VisibilityGroup, "700", SubjectUser, "1002", "I like tea"); got == base {
		t.Fatal("different users produced the same dedupe key")
	}
	if got := ComputeDedupeKey(1, VisibilityGroup, "700", SubjectUser, "1001", "i like tea"); got == base {
		t.Fatal("case folding changed the dedupe key")
	}
	if got := ComputeDedupeKey(1, VisibilityGroup, "700", SubjectUser, "1001", "I  like tea"); got == base {
		t.Fatal("internal whitespace folding changed the dedupe key")
	}
	if got := ComputeDedupeKey(1, VisibilityGroup, "700", SubjectUnknown, "src:1:1001", "I like tea"); got == base {
		t.Fatal("unknown subject produced the same dedupe key as a user subject")
	}
}

// TestComputeDedupeKeyUsesLengthPrefixes verifies that field boundaries cannot
// be shifted to produce the same key.
// TestComputeDedupeKeyUsesLengthPrefixes 验证字段边界不能通过拼接移位得到相同键。
func TestComputeDedupeKeyUsesLengthPrefixes(t *testing.T) {
	a := ComputeDedupeKey(1, VisibilityGroup, "a", SubjectUser, "bc", "x")
	b := ComputeDedupeKey(1, VisibilityGroup, "ab", SubjectUser, "c", "x")
	if a == b {
		t.Fatal("different field boundaries produced the same dedupe key")
	}
}

// TestPrivateMemoryRejectsGroupSubject verifies private memories cannot claim a
// group as their subject.
// TestPrivateMemoryRejectsGroupSubject 验证私聊记忆不能把群作为主体。
func TestPrivateMemoryRejectsGroupSubject(t *testing.T) {
	m := Memory{BotID: 1, Visibility: VisibilityPrivateUser, OwnerKey: "1001", Content: "x", Status: StatusCandidate, SubjectType: SubjectGroup, SubjectID: "700"}
	if err := m.Validate(); err == nil {
		t.Fatal("private memory accepted a group subject")
	}
}
