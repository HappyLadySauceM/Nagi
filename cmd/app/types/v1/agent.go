package v1

import "time"

// OutboundInfo is one delivery record.
// OutboundInfo 是一条投递记录。
type OutboundInfo struct {
	RunID             string
	ToolCallID        string
	ID                string
	JobID             int64
	ConversationID    string
	Sequence          int
	Text              string
	Status            string
	PlatformMessageID int64
	ErrorCode         string
	CreatedAt         time.Time
	UpdatedAt         time.Time
}

// PolicyInfo is the API representation of one scope's sending policy.
// PolicyInfo 是单个作用域出站策略的 API 表示。
type PolicyInfo struct {
	BotID               int64
	ScopeType           string
	ScopeID             int64
	Exists              bool
	AllowSendCurrent    bool
	EffectiveEnabled    bool
	BlockedReasons      []string
	MinIntervalMS       int
	MaxMessagesPerRun   int
	MaxCharsPerMessage  int
	MaxTotalCharsPerRun int
}
