package domain

import (
	"errors"
	"time"
)

var (
	// ErrNotFound keeps missing audit facts distinguishable from storage failures.
	// ErrNotFound 让缺失的审计事实与存储故障可区分。
	ErrNotFound = errors.New("ai audit: not found")
	// ErrConflict rejects an idempotency key reused with different facts.
	// ErrConflict 拒绝使用同一幂等键写入不同事实。
	ErrConflict = errors.New("ai audit: conflicting idempotency key")
)

// AIRun is the durable fact for one worker execution. IDs are strings here so
// callers stay independent from the database UUID implementation.
// AIRun 是一次 worker 执行的持久事实。此处 ID 用字符串，调用方不依赖数据库 UUID 实现。
type AIRun struct {
	ID             string
	OperationID    string
	ConversationID string
	TraceID        string
	Status         string
	ErrorCode      string
	JobID          int64
	BotID          int64
	ConfigVersion  int64
	StartedAt      time.Time
	FinishedAt     *time.Time
	Events         []AIEvent
	Artifacts      []AIArtifact
}

// AIEvent is one ordered operation inside an AI run. Input and Output are
// already redacted by the caller when content export is disabled.
// AIEvent 是一次 AI 运行内的有序操作。关闭正文导出时，调用方应先完成脱敏。
type AIEvent struct {
	ID            string
	RunID         string
	OperationID   string
	Stage         string
	Status        string
	ErrorCode     string
	Input         []byte
	Output        []byte
	Sequence      int
	SchemaVersion int
	StartedAt     time.Time
	FinishedAt    *time.Time
}

// AIArtifact describes media retained for an audited run. Bytes live in the
// S3-compatible archive; this model keeps only the stable object reference.
// AIArtifact 描述为审计运行保留的媒体。字节存放在兼容 S3 的归档中，此模型只保留稳定对象引用。
type AIArtifact struct {
	ID              string
	RunID           string
	EventID         string
	Kind            string
	Status          string
	SHA256          string
	ContentType     string
	ObjectKey       string
	Bucket          string
	FailureCode     string
	SizeBytes       int64
	ExpiresAt       *time.Time
	CreatedAt       time.Time
	UpdatedAt       time.Time
	CleanupAttempts int        `json:"-"`
	NextAttemptAt   *time.Time `json:"-"`
	LeaseToken      string     `json:"-"`
	LeaseExpiresAt  *time.Time `json:"-"`
}

// AIRunQuery restricts the administrator's read-only audit view.
// AIRunQuery 限制管理员只读审计视图的查询范围。
type AIRunQuery struct {
	ConversationID string
	JobID          int64
	BotID          int64
	Limit          int
}
