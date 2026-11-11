package application

import (
	"testing"

	"github.com/HappyLadySauceM/Nagi/internal/memory/domain"
)

// TestMaterializeCandidatesDoesNotAttributeUnknownSubjectToSender verifies the
// unknown-subject provenance rule.
// TestMaterializeCandidatesDoesNotAttributeUnknownSubjectToSender 验证未知主体
// 不会被归到发送者，而使用来源消息与发送者组成的稳定出处标识。
func TestMaterializeCandidatesDoesNotAttributeUnknownSubjectToSender(t *testing.T) {
	worker := &ExtractionWorker{}
	batch := domain.ExtractionBatch{
		Scope:    domain.ExtractionScope{BotID: 1, ConversationID: "conv", ScopeType: "group", ScopeID: 700, AuthorizationEventID: 3},
		Messages: []domain.ExtractionSourceMessage{{ID: 11, BotID: 1, ConversationID: "conv", ScopeType: "group", ScopeID: 700, SenderID: 9001, AuthorizationEventID: 3}},
	}
	got, err := worker.materializeCandidates(batch, []domain.ExtractionCandidate{{
		Content: "乙不吃香菜", Confidence: 0.8, SourceMessageID: 11, Subject: "unknown",
	}})
	if err != nil {
		t.Fatalf("materialize: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("memories = %+v", got)
	}
	if got[0].SubjectType != domain.SubjectUnknown || got[0].SubjectID != domain.ExtractionUnknownSubjectID(11, 9001) {
		t.Fatalf("unknown subject = %q/%q", got[0].SubjectType, got[0].SubjectID)
	}
	if got[0].SourceSenderID != 9001 {
		t.Fatalf("source sender = %d, want 9001", got[0].SourceSenderID)
	}
}
