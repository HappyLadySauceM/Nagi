package v1

import (
	"time"

	memorydomain "github.com/HappyLadySauceM/Nagi/internal/memory/domain"
)

// RecallPreviewRequest is the read-only recall diagnostic input.
// RecallPreviewRequest 是只读召回诊断输入。
type RecallPreviewRequest struct {
	BotID            int64  `json:"bot_id"`
	PreviewMode      string `json:"preview_mode"`
	Kind             string `json:"kind"`
	TargetType       string `json:"target_type"`
	TargetID         int64  `json:"target_id"`
	ConversationID   string `json:"conversation_id"`
	Query            string `json:"query"`
	Limit            int    `json:"limit"`
	SpeakerType      string `json:"speaker_type"`
	SpeakerID        string `json:"speaker_id"`
	SubjectType      string `json:"subject_type"`
	SubjectID        string `json:"subject_id"`
	SubjectSource    string `json:"subject_source"`
	SubjectEvidence  string `json:"subject_evidence"`
	SubjectAmbiguous bool   `json:"subject_ambiguous"`
}

// SupersedeRequest is the explicit memory supersede input.
// SupersedeRequest 是显式记忆替代输入。
type SupersedeRequest struct {
	OldMemoryID    string `json:"old_memory_id"`
	Reason         string `json:"reason"`
	IdempotencyKey string `json:"idempotency_key"`
}

// ManagementMemoryView is the stable administrator detail shape.
// ManagementMemoryView 是稳定的管理员详情结构。
type ManagementMemoryView struct {
	ID              string
	BotID           int64
	Status          memorydomain.Status
	Visibility      memorydomain.Visibility
	OwnerKey        string
	Content         string
	Source          string
	SourceMessageID int64
	SourceSenderID  int64
	Confidence      float64
	SubjectType     memorydomain.SubjectType
	SubjectID       string
	Origin          memorydomain.Origin
	ApprovedBy      string
	CreatedAt       time.Time
	UpdatedAt       time.Time
	ExpiresAt       *time.Time
}

// CreateRequest is the POST body; approve=true activates the memory immediately.
// CreateRequest 是 POST 请求体；approve=true 时立即激活记忆。
type CreateRequest struct {
	BotID          int64   `json:"bot_id"`
	Visibility     string  `json:"visibility"`
	OwnerKey       string  `json:"owner_key"`
	Content        string  `json:"content"`
	Source         string  `json:"source"`
	SourceSenderID int64   `json:"source_sender_id"`
	SubjectType    string  `json:"subject_type"`
	SubjectID      string  `json:"subject_id"`
	ReviewReason   string  `json:"review_reason"`
	Confidence     float64 `json:"confidence"`
	Approve        bool    `json:"approve"`
}

// PatchRequest is the PATCH body; empty fields keep current values.
// PatchRequest 是 PATCH 请求体；空字段保持当前值。
type PatchRequest struct {
	Content    *string `json:"content"`
	Visibility string  `json:"visibility"`
	OwnerKey   *string `json:"owner_key"`
	Status     string  `json:"status"`
}
