// Package contracts publishes the read-only long-term memory port.
// memory/contracts 发布只读长期记忆端口。
package contracts

import (
	"context"
	"time"
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
	Limit          int
}

// Reader loads approved, unexpired memories visible to one conversation.
// Reader 读取某会话可见的已批准且未过期记忆。
type Reader interface {
	Recall(ctx context.Context, req RecallRequest) ([]Memory, error)
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
