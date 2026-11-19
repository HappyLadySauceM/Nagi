package v1

import memorydomain "github.com/HappyLadySauceM/Nagi/internal/memory/domain"

// PreviewMemory preserves the knowledge-preview memory shape and appends recall audit fields.
// PreviewMemory 保持知识预览记忆形状，并附加召回审计字段。
type PreviewMemory struct {
	memorydomain.Memory
	Score           float64  `json:"Score,omitempty"`
	HitKeywords     []string `json:"HitKeywords,omitempty"`
	SubjectMatch    string   `json:"SubjectMatch,omitempty"`
	SelectionReason string   `json:"SelectionReason,omitempty"`
}

// PreviewRequest is the read-only context preview input.
// PreviewRequest 是只读上下文预览请求。
type PreviewRequest struct {
	BotID            int64  `json:"bot_id"`
	PreviewMode      string `json:"preview_mode"`
	TargetType       string `json:"target_type"`
	TargetID         int64  `json:"target_id"`
	ConversationID   string `json:"conversation_id"`
	Query            string `json:"query"`
	SpeakerType      string `json:"speaker_type"`
	SpeakerID        string `json:"speaker_id"`
	SubjectType      string `json:"subject_type"`
	SubjectID        string `json:"subject_id"`
	SubjectSource    string `json:"subject_source"`
	SubjectEvidence  string `json:"subject_evidence"`
	SubjectAmbiguous bool   `json:"subject_ambiguous"`
}
