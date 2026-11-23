package contracts

import (
	"context"
	"time"
)

// DecisionRunEvent is the metadata-only projection of a committed stage.
// DecisionRunEvent 是已提交阶段的仅元数据投影。
type DecisionRunEvent struct {
	RunID          string
	ConversationID string
	StartedAt      time.Time
	Sequence       int
	Kind           string
	At             time.Time
	Data           LiveDecisionFacts
}

// LiveDecisionFacts contains the only values published by the live journal.
// LiveDecisionFacts 仅包含实时日志允许发布的值。
type LiveDecisionFacts struct {
	RuleID          string `json:",omitempty"`
	Branch          string `json:",omitempty"`
	ThresholdMS     *int   `json:",omitempty"`
	ElapsedMS       *int64 `json:",omitempty"`
	LatencyMS       int64  `json:",omitempty"`
	HistorySkipped  bool   `json:",omitempty"`
	HistoryDegraded bool   `json:",omitempty"`
	DecisionID      int64  `json:",omitempty"`
	State           string `json:",omitempty"`
	Decision        string `json:",omitempty"`
	ReasonCode      string `json:",omitempty"`
}

// DecisionLiveRow is one run, or one historical decision without live evidence.
// DecisionLiveRow 是一次运行，或一条没有实时证据的历史决策。
type DecisionLiveRow struct {
	RunID          string
	ConversationID string
	DecisionID     int64
	StartedAt      time.Time
	State          string
	Decision       string
	ReasonCode     string
	Events         []DecisionRunEvent
}

type LiveHistoryKey struct {
	StartedAt time.Time
	RowKey    string
}

type LiveSnapshot struct {
	Position int64
	Scope    string
	Rows     []DecisionLiveRow
	Next     LiveHistoryKey
}

type LiveBatch struct {
	Position int64
	Scope    string
	Events   []DecisionRunEvent
	Reset    bool
}

// LiveDecisionReader applies current scope policy for every account read.
// LiveDecisionReader 对每次账号读取应用当前作用域策略。
type LiveDecisionReader interface {
	DecisionRunSnapshot(ctx context.Context, bot int64, before LiveHistoryKey) (LiveSnapshot, error)
	DecisionRunBatch(ctx context.Context, bot, after int64, scope string) (LiveBatch, error)
	DecisionAuditAccess(ctx context.Context, bot int64, conversation string) (visible, history bool, err error)
	DecisionScope(ctx context.Context, bot int64) (string, error)
	DecisionHistorySources(ctx context.Context, bot int64, conversation string, platformIDs []int64) (map[int64]bool, error)
	DecisionJobAccess(ctx context.Context, bot, job int64) (bool, error)
	DecisionSourceAccess(ctx context.Context, bot int64, conversation string, message int64) (bool, error)
}
