package domain

import (
	"testing"
	"time"
)

// TestRankRecallOrdersExactSubjectBeforeOtherSubject verifies that asking about
// A keeps A's exact fact ahead of B's fact even though B is the speaker.
// TestRankRecallOrdersExactSubjectBeforeOtherSubject 验证询问 A 时，A 的精确事实
// 排在 B 的事实之前，即使 B 是当前发言者。
func TestRankRecallOrdersExactSubjectBeforeOtherSubject(t *testing.T) {
	now := time.Date(2026, 11, 16, 10, 0, 0, 0, time.UTC)
	memories := []Memory{
		{ID: "b", Visibility: VisibilityGroup, Content: "B 爱喝拿铁", SubjectType: SubjectUser, SubjectID: "200", UpdatedAt: now},
		{ID: "a", Visibility: VisibilityGroup, Content: "A 爱喝美式", SubjectType: SubjectUser, SubjectID: "1001", UpdatedAt: now.Add(-time.Minute)},
	}
	hits, noKeyword, reason := RankRecall(memories, RecallQuery{
		Kind: "group", TargetID: 700, Query: "A 爱喝什么",
		SubjectType: SubjectUser, SubjectID: "1001", Limit: 3,
	})
	if noKeyword || reason != "" {
		t.Fatalf("rank diagnostics = noKeyword=%v reason=%q", noKeyword, reason)
	}
	if len(hits) != 2 || hits[0].Memory.ID != "a" || hits[1].Memory.ID != "b" {
		t.Fatalf("ranked hits = %+v", hits)
	}
	if hits[0].SubjectMatch != SubjectMatchExact || hits[0].SelectionReason != SelectionReasonSubjectExact || hits[0].Score != 3 {
		t.Fatalf("exact hit = %+v", hits[0])
	}
	if hits[1].SubjectMatch != SubjectMatchOther || hits[1].SelectionReason != SelectionReasonKeywordMatch || hits[1].Score != 1 {
		t.Fatalf("other hit = %+v", hits[1])
	}
}

// TestRankRecallSharedFallbackAndUnknownRequest verifies shared fallback and
// unknown-subject keyword selection reasons.
// TestRankRecallSharedFallbackAndUnknownRequest 验证共享回退与未知主体关键词
// 命中原因。
func TestRankRecallSharedFallbackAndUnknownRequest(t *testing.T) {
	memories := []Memory{
		{ID: "shared", Visibility: VisibilityBotShared, Content: "大家爱喝咖啡", SubjectType: SubjectUnknown},
		{ID: "group", Visibility: VisibilityGroup, Content: "群友爱喝茶", SubjectType: SubjectGroup, SubjectID: "700"},
	}
	hits, noKeyword, _ := RankRecall(memories, RecallQuery{
		Kind: "group", TargetID: 700, Query: "爱喝", SubjectType: SubjectUser, SubjectID: "1001", Limit: 5,
	})
	if noKeyword || len(hits) != 2 {
		t.Fatalf("ranked hits = %+v noKeyword=%v", hits, noKeyword)
	}
	if hits[0].Memory.ID != "group" || hits[0].SubjectMatch != SubjectMatchGroup || hits[0].SelectionReason != SelectionReasonGroupMatch {
		t.Fatalf("group hit = %+v", hits[0])
	}
	if hits[1].Memory.ID != "shared" || hits[1].SubjectMatch != SubjectMatchShared || hits[1].SelectionReason != SelectionReasonKeywordMatch {
		t.Fatalf("shared hit with exact/group present = %+v", hits[1])
	}

	unknownHits, noKeyword, _ := RankRecall([]Memory{{ID: "known", Visibility: VisibilityGroup, Content: "爱喝茶", SubjectType: SubjectUser, SubjectID: "200"}}, RecallQuery{
		Kind: "group", TargetID: 700, Query: "爱喝", SubjectType: SubjectUnknown,
	})
	if noKeyword || len(unknownHits) != 1 || unknownHits[0].SubjectMatch != SubjectMatchNone || unknownHits[0].SelectionReason != SelectionReasonUnknownSubjectKeyword {
		t.Fatalf("unknown subject hit = %+v noKeyword=%v", unknownHits, noKeyword)
	}
}

// TestRankRecallDistinguishesNoKeywordFromNoMatch verifies the diagnostic
// reason used by recall observability.
// TestRankRecallDistinguishesNoKeywordFromNoMatch 验证召回观测使用的诊断原因。
func TestRankRecallDistinguishesNoKeywordFromNoMatch(t *testing.T) {
	if _, noKeyword, reason := RankRecall(nil, RecallQuery{Query: ""}); !noKeyword || reason != "no_keyword" {
		t.Fatalf("empty query diagnostics = %v/%q", noKeyword, reason)
	}
	if _, noKeyword, reason := RankRecall(nil, RecallQuery{Query: "什么"}); !noKeyword || reason != "no_keyword" {
		t.Fatalf("common-only query diagnostics = %v/%q", noKeyword, reason)
	}
	if _, noKeyword, reason := RankRecall(nil, RecallQuery{Query: "不存在的词"}); !noKeyword || reason != "no_match" {
		t.Fatalf("no-match diagnostics = %v/%q", noKeyword, reason)
	}
}

// TestRankRecallCapsLimitAndKeepsStableTieBreak verifies the bounded result and
// deterministic memory-id tie-break.
// TestRankRecallCapsLimitAndKeepsStableTieBreak 验证结果有界及内存 ID 的稳定兜底。
func TestRankRecallCapsLimitAndKeepsStableTieBreak(t *testing.T) {
	memories := []Memory{
		{ID: "z", Visibility: VisibilityGroup, Content: "爱喝茶", SubjectType: SubjectUser, SubjectID: "1001"},
		{ID: "a", Visibility: VisibilityGroup, Content: "爱喝咖啡", SubjectType: SubjectUser, SubjectID: "1001"},
	}
	hits, noKeyword, _ := RankRecall(memories, RecallQuery{
		Kind: "group", TargetID: 700, Query: "爱喝", SubjectType: SubjectUser, SubjectID: "1001", Limit: 1,
	})
	if noKeyword || len(hits) != 1 || hits[0].Memory.ID != "a" {
		t.Fatalf("capped hits = %+v noKeyword=%v", hits, noKeyword)
	}
}

// TestRankRecallKeepsSameTextPreferencesSeparate verifies identical text from
// two people stays attached to the requested subject instead of being merged.
// TestRankRecallKeepsSameTextPreferencesSeparate 验证两个人的相同文本仍归属
// 请求主体，不会被合并串味。
func TestRankRecallKeepsSameTextPreferencesSeparate(t *testing.T) {
	memories := []Memory{
		{ID: "a", Visibility: VisibilityGroup, Content: "我喜欢猫", SubjectType: SubjectUser, SubjectID: "1001"},
		{ID: "b", Visibility: VisibilityGroup, Content: "我喜欢猫", SubjectType: SubjectUser, SubjectID: "200"},
	}
	hits, noKeyword, _ := RankRecall(memories, RecallQuery{
		Kind: "group", TargetID: 700, Query: "喜欢", SubjectType: SubjectUser, SubjectID: "1001", Limit: 3,
	})
	if noKeyword || len(hits) != 2 || hits[0].Memory.ID != "a" || hits[1].Memory.ID != "b" {
		t.Fatalf("same-text hits = %+v noKeyword=%v", hits, noKeyword)
	}
	if hits[0].SubjectMatch != SubjectMatchExact || hits[1].SubjectMatch != SubjectMatchOther {
		t.Fatalf("same-text subject matches = %+v", hits)
	}
}
