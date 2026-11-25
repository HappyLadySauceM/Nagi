package contracts

import (
	"context"
	"time"
)

type HistoryRequest struct {
	BotID          int64
	TargetID       int64
	JobID          int64
	ConversationID string
	Kind           string
	Query          string
	SenderID       int64
	After          time.Time
	Limit          int
}

type HistoryHit struct {
	ID        int64
	SenderID  int64
	CreatedAt time.Time
	Text      string
}

type HistoryReader interface {
	Search(context.Context, HistoryRequest) ([]HistoryHit, error)
}
