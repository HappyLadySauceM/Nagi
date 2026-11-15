package domain

import "time"

// Replacement is one immutable memory supersede relation.
// Replacement 是一条不可变的记忆替代关系。
type Replacement struct {
	ReplacementID  string
	BotID          int64
	NewMemoryID    string
	OldMemoryID    string
	Actor          string
	Reason         string
	IdempotencyKey string
	CreatedAt      time.Time
}

// ReplacementCounterpart describes the other side of one replacement relation.
// ReplacementCounterpart 描述替代关系的另一方。
type ReplacementCounterpart struct {
	MemoryID string
	Status   Status
	Missing  bool
}

// ReplacementView is one replacement relation rendered for management detail.
// ReplacementView 是管理详情使用的一条替代关系视图。
type ReplacementView struct {
	ReplacementID string
	Direction     string
	NewMemoryID   string
	OldMemoryID   string
	Actor         string
	Reason        string
	NewStatus     Status
	OldStatus     Status
	CreatedAt     time.Time
	Counterpart   ReplacementCounterpart
}

// SupersedeInput is one explicit candidate supersede request.
// SupersedeInput 是一次显式候选替代请求。
type SupersedeInput struct {
	NewMemoryID    string
	OldMemoryID    string
	Actor          string
	Reason         string
	IdempotencyKey string
}

// SupersedeResult is the stable supersede API result.
// SupersedeResult 是稳定的替代接口结果。
type SupersedeResult struct {
	NewMemoryID string
	OldMemoryID string
	NewStatus   Status
	OldStatus   Status
	ReplacedAt  time.Time
	ReplacedBy  string
	Idempotent  bool
}
