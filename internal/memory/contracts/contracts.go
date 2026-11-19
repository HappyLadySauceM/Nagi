// Package contracts publishes the read-only long-term memory port.
// memory/contracts 发布只读长期记忆端口。
package contracts

import (
	"context"
	"time"

	memorydomain "github.com/HappyLadySauceM/Nagi/internal/memory/domain"
)

// Memory is one approved memory entry handed to another business context.
// Memory 是交给其他业务上下文的单条已批准记忆。
type Memory struct {
	ID              string
	Content         string
	Visibility      string
	Source          string
	SourceMessageID int64
	SourceSenderID  int64
	Origin          string
	SubjectType     string
	SubjectID       string
	ReviewReason    string
	UpdatedAt       time.Time
}

// RecallRequest is the visibility-scoped read request of one reply run.
// RecallRequest 是单次回复运行中按可见性过滤的读取请求。
type RecallRequest struct {
	BotID          int64
	Kind           string
	TargetID       int64
	ConversationID string
	Query          string
	AllowRewrite   bool
	Limit          int
	// SpeakerType/SpeakerID identify the current message sender. They are
	// separate from the subject being asked about and never imply the subject.
	// SpeakerType/SpeakerID 标识当前消息发送者，与查询主体分离，不能据此推断主体。
	SpeakerType string
	SpeakerID   string
	// SubjectType/SubjectID are the target subject of this recall.
	// SubjectType/SubjectID 是本次召回的目标主体。
	SubjectType string
	SubjectID   string
	// SubjectSource is the deterministic source that supplied SubjectID.
	// SubjectSource 是对 SubjectID 的确定性来源。
	SubjectSource string
	// SubjectEvidence is a masked source identifier such as mention:1001.
	// SubjectEvidence 是脱敏来源标识，例如 mention:1001。
	SubjectEvidence string
	// SubjectAmbiguous marks an unresolved subject ambiguity.
	// SubjectAmbiguous 标记主体无法消歧。
	SubjectAmbiguous bool
}

// RecallItem is one ranked memory with its audit fields.
// RecallItem 是一条带排序与命中审计信息的记忆。
type RecallItem struct {
	MemoryID        string
	Content         string
	Visibility      string
	SubjectType     string
	SubjectID       string
	Source          string
	SourceMessageID int64
	SourceSenderID  int64
	Score           float64
	HitKeywords     []string
	SubjectMatch    string
	SelectionReason string
	UpdatedAt       time.Time `json:"-"`
	// Memory carries the original domain row for compatibility views; it is
	// deliberately excluded from the recall audit JSON.
	// Memory 保留原始领域行供兼容视图使用，召回审计 JSON 不输出该字段。
	Memory memorydomain.Memory `json:"-"`
}

// RecallResult is the bounded result of one recall request.
// RecallResult 是单次召回请求的有界结果。
type RecallResult struct {
	Items     []RecallItem
	NoKeyword bool
	Source    string
	Limit     int
	// Reason distinguishes no_keyword from no_match for diagnostics.
	// Reason 用于区分 no_keyword 与 no_match，便于调试。
	Reason string
}

// Reader loads ranked, approved, unexpired memories visible to one conversation.
// Reader 读取某会话可见且经过排序的已批准、未过期记忆。
type Reader interface {
	Recall(ctx context.Context, req RecallRequest) (RecallResult, error)
}

// DetailRequest asks for one memory that is visible to one conversation.
// DetailRequest 请求一条对指定会话可见的记忆详情。
type DetailRequest struct {
	BotID          int64
	Kind           string
	TargetID       int64
	ConversationID string
	MemoryID       string
}

// DetailReader reads one visible memory with its stored source metadata.
// DetailReader 读取一条可见记忆及其已存来源信息。
type DetailReader interface {
	RecallDetail(ctx context.Context, req DetailRequest) (Memory, bool, error)
}
