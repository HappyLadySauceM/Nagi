package infrastructure

import "testing"

// TestParseExtractionCandidates validates JSON framing and subject defaults.
// TestParseExtractionCandidates 验证 JSON 结构与主体字段解析。
func TestParseExtractionCandidates(t *testing.T) {
	raw := `{"candidates":[
		{"content":"小明喜欢喝咖啡","confidence":0.9,"source_message_id":11,"subject":"sender"},
		{"content":"乙不吃香菜","confidence":0.8,"source_message_id":12,"subject":"unknown","review_reason":"subject_unresolved"}
	]}`
	got, err := parseExtractionCandidates(raw)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("candidates = %+v, want 2", got)
	}
	if got[0].Subject != "sender" || got[1].Subject != "unknown" || got[1].ReviewReason != "subject_unresolved" {
		t.Fatalf("subject binding = %+v", got)
	}
}

// TestParseExtractionCandidatesRejectsInvalidCandidate verifies malformed model
// output fails the batch instead of silently dropping a candidate.
// TestParseExtractionCandidatesRejectsInvalidCandidate 验证畸形模型输出整批失败，
// 不静默丢弃单条候选。
func TestParseExtractionCandidatesRejectsInvalidCandidate(t *testing.T) {
	if _, err := parseExtractionCandidates(`{"candidates":[{"content":"","confidence":0.9,"source_message_id":13,"subject":"sender"}]}`); err == nil {
		t.Fatal("expected invalid candidate error")
	}
}

// TestParseExtractionCandidatesRejectsNonJSON verifies malformed model output
// fails the batch instead of producing empty candidates.
// TestParseExtractionCandidatesRejectsNonJSON 验证模型输出非 JSON 时返回错误，
// 而不是静默生成空候选。
func TestParseExtractionCandidatesRejectsNonJSON(t *testing.T) {
	if _, err := parseExtractionCandidates("not json"); err == nil {
		t.Fatal("expected invalid JSON error")
	}
}
