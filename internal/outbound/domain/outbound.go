// Package domain defines the outbound delivery ledger model and run budget.
// outbound/domain 定义出站投递台账模型与回合预算。
package domain

import (
	"errors"
	"fmt"
	"time"
	"unicode/utf8"
)

// Status is the delivery state of one outbound message.
// Status 是一条出站消息的投递状态。
type Status string

// Ledger states. A message is reserved as prepared, advanced to sending right
// before the platform call, and settled into a terminal state afterwards.
// 台账状态。消息先以 prepared 预留，调用平台前推进为 sending，随后落定终态。
const (
	StatusPrepared  Status = "prepared"
	StatusSending   Status = "sending"
	StatusSent      Status = "sent"
	StatusFailed    Status = "failed"
	StatusUnknown   Status = "unknown"
	StatusDenied    Status = "denied"
	StatusCancelled Status = "cancelled"
)

// Valid reports whether the status is one the ledger may store.
// Valid 报告该状态是否为台账允许存储的状态。
func (s Status) Valid() bool {
	switch s {
	case StatusPrepared, StatusSending, StatusSent, StatusFailed, StatusUnknown, StatusDenied, StatusCancelled:
		return true
	default:
		return false
	}
}

// Delivered reports whether the platform may have delivered the message.
// unknown counts as delivered because the message may already be visible.
// Delivered 报告平台是否可能已送达该消息；unknown 也视为已投递，
// 因为消息可能已对用户可见。
func (s Status) Delivered() bool { return s == StatusSent || s == StatusUnknown }

// Settleable reports whether a sending record may transition into this status.
// Settleable 报告 sending 记录是否允许迁移到该状态。
func (s Status) Settleable() bool {
	return s == StatusSent || s == StatusFailed || s == StatusUnknown
}

// Record is one persisted delivery record.
// Record 是一条已持久化的投递记录。
type Record struct {
	NotBefore         time.Time
	ID                string
	JobID             int64
	ConversationID    string
	BotID             int64
	RunID             string
	Sequence          int
	ToolCallID        string
	Text              string
	Status            Status
	PlatformMessageID int64
	ErrorCode         string
	CreatedAt         time.Time
	UpdatedAt         time.Time
}

// Limits bounds the outbound messages of one agent run.
// Limits 限定一次 Agent 运行的出站消息。
type Limits struct {
	MaxPerHour       int
	StopAfterFailure bool
	MinIntervalMS    int
	// MaxMessages bounds how many messages one run may send.
	// MaxMessages 限定单次运行可发送的消息条数。
	MaxMessages int
	// MaxCharsPerMessage bounds one message in runes.
	// MaxCharsPerMessage 以字符数限定单条消息长度。
	MaxCharsPerMessage int
	// MaxTotalChars bounds the summed rune count of one run.
	// MaxTotalChars 以字符数总和限定单次运行。
	MaxTotalChars int
}

// ReserveInput is one delivery record to reserve.
// ReserveInput 是一条待预留的投递记录。
type ReserveInput struct {
	JobID          int64
	BotID          int64
	ConversationID string
	RunID          string
	ToolCallID     string
	Text           string
	Limits         Limits
}

// Usage is the budget already reserved by one run.
// Usage 是单次运行已预留的预算。
type Usage struct {
	Messages int
	Chars    int
}

// Dimension names the budget limit that rejected a reservation.
// Dimension 标识拒绝预留的预算维度。
type Dimension string

// Budget dimensions.
// 预算维度。
const (
	DimensionMessages     Dimension = "messages"
	DimensionMessageChars Dimension = "message_chars"
	DimensionTotalChars   Dimension = "total_chars"
)

// ErrBudgetExceeded is matched by every budget rejection.
// ErrBudgetExceeded 是所有预算拒绝共同匹配的哨兵值。
var ErrBudgetExceeded = errors.New("outbound: budget exceeded")

// BudgetError reports which limit rejected one reservation.
// BudgetError 报告哪一项上限拒绝了本次预留。
type BudgetError struct {
	Dimension Dimension
	Limit     int
	Requested int
}

// Error implements error.
// Error 实现 error 接口。
func (e *BudgetError) Error() string {
	return fmt.Sprintf("outbound: %s budget exceeded: limit %d, requested %d",
		e.Dimension, e.Limit, e.Requested)
}

// Unwrap lets callers match the rejection with errors.Is(ErrBudgetExceeded).
// Unwrap 让调用方可用 errors.Is(ErrBudgetExceeded) 匹配拒绝原因。
func (e *BudgetError) Unwrap() error { return ErrBudgetExceeded }

// CheckBudget reports whether one more message fits the run budget. The check
// is pure so the same rule runs before the transaction and inside it.
// CheckBudget 报告再发一条消息是否仍在预算内。校验保持纯函数，
// 使同一规则既可在事务前调用，也可在事务内使用。
func CheckBudget(limits Limits, used Usage, text string) error {
	runes := utf8.RuneCountInString(text)
	// A zero limit means unlimited, so it must short-circuit instead of
	// rejecting every message.
	// 上限为 0 表示不限，必须短路，否则会拒绝所有消息。
	if limits.MaxCharsPerMessage > 0 && runes > limits.MaxCharsPerMessage {
		return &BudgetError{
			Dimension: DimensionMessageChars,
			Limit:     limits.MaxCharsPerMessage,
			Requested: runes,
		}
	}
	if limits.MaxMessages > 0 && used.Messages+1 > limits.MaxMessages {
		return &BudgetError{
			Dimension: DimensionMessages,
			Limit:     limits.MaxMessages,
			Requested: used.Messages + 1,
		}
	}
	if limits.MaxTotalChars > 0 && used.Chars+runes > limits.MaxTotalChars {
		return &BudgetError{
			Dimension: DimensionTotalChars,
			Limit:     limits.MaxTotalChars,
			Requested: used.Chars + runes,
		}
	}
	return nil
}

// ErrRecordNotFound reports a ledger id or job that does not exist.
// ErrRecordNotFound 表示台账 ID 或任务不存在。
var ErrRecordNotFound = errors.New("outbound: record not found")

// ErrInvalidSettlement reports a settle target outside sent/failed/unknown.
// ErrInvalidSettlement 表示落定目标不在 sent/failed/unknown 之内。
var ErrInvalidSettlement = errors.New("outbound: invalid settlement status")

// TransitionError reports a status change the state machine rejects.
// TransitionError 表示状态机拒绝的状态迁移。
type TransitionError struct {
	RecordID string
	From     Status
	To       Status
}

// Error implements error.
// Error 实现 error 接口。
func (e *TransitionError) Error() string {
	return fmt.Sprintf("outbound: record %s cannot move from %s to %s", e.RecordID, e.From, e.To)
}

// ToolStatus classifies a budget refusal independently of transport errors.
// ToolStatus 独立于传输错误标记预算拒绝。
func (e *BudgetError) ToolStatus() string { return "denied" }
