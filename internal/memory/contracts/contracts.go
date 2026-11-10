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
	ID         string
	Content    string
	Visibility string
	UpdatedAt  time.Time
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
