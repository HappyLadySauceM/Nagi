package domain

import (
	"strings"
	"time"
)

// ExtractionJob is one managed memory-extraction work item. MessageID is zero
// when the source message has already been removed by retention.
// ExtractionJob 是一条记忆提取任务；来源消息被留存清理后 MessageID 为零。
type ExtractionJob struct {
	ID                          string
	BotID                       int64
	ConversationID              string
	ScopeType                   string
	ScopeID                     int64
	MessageID                   int64
	MessageAuthorizationEventID int64
	AuthorizationEventID        int64
	Status                      string
	Attempts                    int
	RoundAttempts               int
	NextRunAt                   time.Time
	LastErrorCode               string
	ManualRetries               int
	SourceAvailable             bool
	CreatedAt                   time.Time
	UpdatedAt                   time.Time
}

// ExtractionJobFilter is the management list filter.
// ExtractionJobFilter 是管理列表过滤条件。
type ExtractionJobFilter struct {
	BotID          int64
	ConversationID string
	Status         string
	Page           int
	Limit          int
}

// Normalized clamps paging and trims user-provided filters.
// Normalized 收敛分页并去除用户过滤值两端空白。
func (f ExtractionJobFilter) Normalized() ExtractionJobFilter {
	f.ConversationID = strings.TrimSpace(f.ConversationID)
	f.Status = strings.TrimSpace(f.Status)
	if f.Page < 1 {
		f.Page = 1
	}
	if f.Limit < 1 {
		f.Limit = 50
	}
	if f.Limit > 200 {
		f.Limit = 200
	}
	return f
}

// Offset is the SQL offset for the normalized filter.
// Offset 返回规范化过滤条件的 SQL 偏移量。
func (f ExtractionJobFilter) Offset() int { return (f.Page - 1) * f.Limit }
