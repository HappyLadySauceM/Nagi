// Package domain defines conversation, message and job value types.
// conversation/domain 定义会话、消息与任务值类型。
package domain

import (
	"time"

	obdomain "github.com/HappyLadySauceM/QQBot/internal/onebot/domain"
)

// Kind is the conversation kind, mirroring the scope types.
// Kind 是会话类型，与作用域类型对应。
type Kind string

// Supported conversation kinds.
// 支持的会话类型。
const (
	KindGroup   Kind = "group"
	KindPrivate Kind = "private"
)

// Valid reports whether the kind is supported.
// Valid 报告会话类型是否受支持。
func (k Kind) Valid() bool { return k == KindGroup || k == KindPrivate }

// Direction distinguishes inbound from outbound messages.
// Direction 区分入站与出站消息。
type Direction string

// Supported directions.
// 支持的方向。
const (
	Incoming Direction = "incoming"
	Outgoing Direction = "outgoing"
)

// Conversation is one persistent chat thread (group or private).
// Conversation 是一个持久化会话（群聊或私聊）。
type Conversation struct {
	ID        string
	BotID     int64
	Kind      Kind
	TargetID  int64
	Summary   string
	LastMsgAt time.Time
}

// Message is one stored message; Segments use the OneBot published language.
// Message 是一条已存储消息；Segments 使用 OneBot 已发布语言。
type Message struct {
	ID                int64
	ConversationID    string
	BotID             int64
	Direction         Direction
	PlatformMessageID int64
	SenderID          int64
	Text              string
	Segments          []obdomain.Segment
	SentStatus        string
	CreatedAt         time.Time
}

// IngestInput is the input for persisting one authorized inbound message.
// IngestInput 是持久化一条已授权入站消息的输入。
type IngestInput struct {
	BotID             int64
	Kind              Kind
	TargetID          int64
	PlatformMessageID int64
	SenderID          int64
	Text              string
	Segments          []obdomain.Segment
}

// IngestResult reports the outcome of one ingest attempt.
// IngestResult 报告一次入库的结果。
type IngestResult struct {
	ConversationID string
	MessageID      int64
	JobID          int64
	Duplicate      bool
}

// Job is one pending decision task derived from an inbound message.
// Job 是从入站消息派生的一个待处理决策任务。
type Job struct {
	ID             int64
	MessageID      int64
	ConversationID string
	BotID          int64
	Kind           Kind
	TargetID       int64
	Message        Message
}
