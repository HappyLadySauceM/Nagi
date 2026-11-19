// Package domain defines conversation, message and job value types.
// conversation/domain 定义会话、消息与任务值类型。
package domain

import (
	"time"

	obdomain "github.com/HappyLadySauceM/Nagi/internal/onebot/domain"
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

// SourceMessage is the narrow read model used to verify a memory's original
// source without opening a general chat-history API.
// SourceMessage 是用于核验记忆原始来源的窄读模型，不开放通用聊天历史接口。
type SourceMessage struct {
	ID             int64
	ConversationID string
	BotID          int64
	Text           string
	CreatedAt      time.Time
}

// IngestInput is the input for persisting one authorized inbound message.
// IngestInput 是持久化一条已授权入站消息的输入。
type IngestInput struct {
	ArchiveOnly       bool
	BotID             int64
	Kind              Kind
	TargetID          int64
	PlatformMessageID int64
	SenderID          int64
	Text              string
	Segments          []obdomain.Segment
	// TraceParent and TraceState carry the W3C context of the ingress span so
	// the worker continues the same trace; empty values mean a new trace.
	// TraceParent 与 TraceState 携带入站 span 的 W3C 上下文，供工作器延续
	// 同一条 trace；为空表示开启新的 trace。
	TraceParent string
	TraceState  string
}

// IngestResult reports the outcome of one ingest attempt.
// IngestResult 报告一次入库的结果。
type IngestResult struct {
	ConversationID string
	MessageID      int64
	JobID          int64
	Duplicate      bool
}

// JobIngress links a stored inbound message to the job that processes it.
// JobIngress 关联已存入站消息和处理它的任务。
type JobIngress struct {
	Kind        Kind
	TargetID    int64
	Message     Message
	TraceParent string
	Status      string
}

// Job is one pending decision task derived from an inbound message.
// Job 是从入站消息派生的一个待处理决策任务。
type Job struct {
	ClaimToken     string
	LeaseExpiresAt time.Time
	ID             int64
	MessageID      int64
	ConversationID string
	BotID          int64
	Kind           Kind
	TargetID       int64
	Message        Message
	// TraceParent and TraceState restore the ingress trace in the worker; an
	// empty TraceParent marks a legacy job that starts its own trace.
	// TraceParent 与 TraceState 让工作器还原入站 trace；
	// TraceParent 为空表示历史任务，将自行开启新的 trace。
	TraceParent string
	TraceState  string
}

// RecoveryPolicy bounds retries after an expired claim.
// RecoveryPolicy 限定过期认领恢复后的重试策略。
type RecoveryPolicy struct {
	MaxAttempts  int
	RetryBackoff time.Duration
}

// HistoryDocument is the normalized, conversation-bound search projection.
// HistoryDocument 是绑定会话的规范化搜索投影。
type HistoryDocument struct {
	ID             int64     `json:"-"`
	BotID          int64     `json:"bot_id"`
	ConversationID string    `json:"conversation_id"`
	SenderID       int64     `json:"sender_id"`
	CreatedAt      time.Time `json:"created_at"`
	Text           string    `json:"text"`
}

// HistorySearch describes an authorized search bounded by the triggering message.
// HistorySearch 描述已授权的检索，范围截至触发本轮的消息。
type HistorySearch struct {
	BotID          int64
	ConversationID string
	BeforeID       int64
	Before         time.Time
	After          time.Time
	SenderID       int64
	Query          string
	Limit          int
}

// IndexedMessage carries the scope needed to authorize its search projection.
// IndexedMessage 携带搜索投影所需的作用域信息。
type IndexedMessage struct {
	Message  Message
	Kind     Kind
	TargetID int64
}
