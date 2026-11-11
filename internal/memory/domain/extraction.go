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

// ExtractionScope is one authorized conversation and its current memory
// authorization generation.
// ExtractionScope 是一个已授权会话及其当前记忆授权代际。
type ExtractionScope struct {
	BotID                int64
	ConversationID       string
	ScopeType            string
	ScopeID              int64
	AuthorizationEventID int64
}

// ExtractionBatch is a claimed set of one-message jobs coalesced for one model
// call. All jobs share the same bot, conversation and authorization generation.
// ExtractionBatch 是为一次模型调用合并的一组单消息任务；同批任务共享同一
// bot、会话与授权代际。
type ExtractionBatch struct {
	Scope          ExtractionScope
	Jobs           []ExtractionJob
	Messages       []ExtractionSourceMessage
	ClaimToken     string
	LeaseExpiresAt time.Time
}

// ExtractionClaimOptions bounds one claimed batch.
// ExtractionClaimOptions 限定一次认领批次的大小与租约。
type ExtractionClaimOptions struct {
	MaxMessages int
	MaxRunes    int
	Lease       time.Duration
}

// ExtractionCandidate is one candidate returned by the extractor. Subject is
// the model's asserted relation: "sender", "group" or "unknown"; the worker
// validates it against the source message before persistence.
// ExtractionCandidate 是抽取器返回的一条候选。Subject 是模型断言的主体关系：
// "sender"、"group" 或 "unknown"；工作器在落库前按来源消息校验。
type ExtractionCandidate struct {
	Content         string
	Confidence      float64
	SourceMessageID int64
	Subject         string
	SubjectLabel    string
	ReviewReason    string
}

// ExtractionRequest is the bounded input of one model extraction call.
// ExtractionRequest 是一次模型抽取调用的有界输入。
type ExtractionRequest struct {
	Scope    ExtractionScope
	Messages []ExtractionSourceMessage
	// MaxRunes bounds the serialized prompt including source labels, IDs and senders.
	// MaxRunes 限制序列化提示词的总字符数（含来源标签、ID 与发送者）。
	MaxRunes int
}

// FinalizeResult reports the candidate persistence outcome of one batch.
// FinalizeResult 报告一个批次的候选落库结果。
type FinalizeResult struct {
	Created    int
	Duplicates int
	Suppressed int
}
