package domain

import "time"

// ExtractionSourceMessage is one incoming message that has a durable memory
// authorization stamp and no extraction job yet.
// ExtractionSourceMessage 是一条已有持久记忆授权戳且尚未建立提取任务的入站消息。
type ExtractionSourceMessage struct {
	ID                   int64
	BotID                int64
	ConversationID       string
	ScopeType            string
	ScopeID              int64
	SenderID             int64
	Text                 string
	CreatedAt            time.Time
	AuthorizationEventID int64
}
