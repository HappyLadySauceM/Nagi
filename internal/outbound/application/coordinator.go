// Package application coordinates reservations and settlements of the
// outbound delivery ledger.
// outbound/application 协调出站投递台账的预留与落定。
package application

import (
	"context"
	"fmt"

	"github.com/HappyLadySauceM/Nagi/internal/outbound/domain"
)

// Repository persists the delivery ledger; implemented by infrastructure.
// Repository 持久化投递台账；由 infrastructure 实现。
type Repository interface {
	Reserve(ctx context.Context, in domain.ReserveInput) (domain.Record, error)
	MarkSending(ctx context.Context, id string, claim ...string) error
	Settle(ctx context.Context, id string, outcome Outcome, claim ...string) error
	HasDelivered(ctx context.Context, jobID int64) (bool, error)
}

// Request describes one message to reserve inside an agent run.
// Request 描述一次 Agent 运行内待预留的一条消息。
type Request struct {
	ClaimToken     string
	JobID          int64
	BotID          int64
	ConversationID string
	RunID          string
	ToolCallID     string
	Text           string
	Limits         domain.Limits
}

// Outcome is the terminal state of one delivery attempt.
// Outcome 是一次投递尝试的终态。
type Outcome struct {
	Status            domain.Status
	PlatformMessageID int64
	ErrorCode         string
}

// Coordinator is the single entry point for ledger writes.
// Coordinator 是台账写入的唯一入口。
type Coordinator struct {
	repo Repository
}

// NewCoordinator validates dependencies and builds the coordinator.
// NewCoordinator 校验依赖并构建协调器。
func NewCoordinator(repo Repository) (*Coordinator, error) {
	if repo == nil {
		return nil, fmt.Errorf("outbound: repository required")
	}
	return &Coordinator{repo: repo}, nil
}

// Reserve allocates the next sequence and run budget for one message. The
// repository performs the allocation under a per-job row lock, so concurrent
// callers of the same run never share a sequence or exceed the budget.
// Reserve 为一条消息分配下一个序号与回合预算。仓储在按任务的行锁下完成分配，
// 同一运行的并发调用不会复用序号或超额。
func (c *Coordinator) Reserve(ctx context.Context, req Request) (domain.Record, error) {
	if req.JobID <= 0 || req.BotID <= 0 {
		return domain.Record{}, fmt.Errorf("outbound: reserve requires job and bot id")
	}
	if req.ConversationID == "" {
		return domain.Record{}, fmt.Errorf("outbound: reserve requires conversation id")
	}
	if req.Text == "" {
		return domain.Record{}, fmt.Errorf("outbound: reserve requires non-empty text")
	}
	return c.repo.Reserve(ctx, domain.ReserveInput{
		JobID:          req.JobID,
		ClaimToken:     req.ClaimToken,
		BotID:          req.BotID,
		ConversationID: req.ConversationID,
		RunID:          req.RunID,
		ToolCallID:     req.ToolCallID,
		Text:           req.Text,
		Limits:         req.Limits,
	})
}

// MarkSending advances one reserved record to sending; the platform call must
// only happen after this succeeds.
// MarkSending 把一条已预留记录推进为 sending；只有调用成功后才能发起平台发送。
func (c *Coordinator) MarkSending(ctx context.Context, id string, claim ...string) error {
	if id == "" {
		return fmt.Errorf("outbound: mark sending requires record id")
	}
	return c.repo.MarkSending(ctx, id, claim...)
}

// Settle records the terminal state of one sending record.
// Settle 记录一条 sending 记录的终态。
func (c *Coordinator) Settle(ctx context.Context, id string, outcome Outcome, claim ...string) error {
	if id == "" {
		return fmt.Errorf("outbound: settle requires record id")
	}
	if !outcome.Status.Settleable() {
		return fmt.Errorf("outbound: settle %s: %w", id, domain.ErrInvalidSettlement)
	}
	return c.repo.Settle(ctx, id, outcome, claim...)
}

// HasDelivered reports whether the job already has a record whose delivery is
// confirmed or unknown, which makes retrying the run unsafe.
// HasDelivered 报告该任务是否已有确认送达或结果不明的记录，
// 存在时重跑会产生重复消息。
func (c *Coordinator) HasDelivered(ctx context.Context, jobID int64) (bool, error) {
	if jobID <= 0 {
		return false, fmt.Errorf("outbound: has delivered requires job id")
	}
	return c.repo.HasDelivered(ctx, jobID)
}

// DeliveryError reports incomplete deliveries without replaying the run.
// DeliveryError 报告未完成的投递，避免重放整轮。
func (c *Coordinator) DeliveryError(ctx context.Context, jobID int64) error {
	if reader, ok := c.repo.(interface {
		DeliveryError(context.Context, int64) error
	}); ok {
		return reader.DeliveryError(ctx, jobID)
	}
	return fmt.Errorf("outbound: delivery summary unavailable")
}

// Cancel releases a reservation that has not reached the platform.
// Cancel 释放尚未进入平台调用的预留。
func (c *Coordinator) Cancel(ctx context.Context, id string, claim ...string) error {
	if repo, ok := c.repo.(interface {
		Cancel(context.Context, string, ...string) error
	}); ok {
		return repo.Cancel(ctx, id, claim...)
	}
	return fmt.Errorf("outbound: cancel unavailable")
}

// RecordDenied persists a run termination without claiming a platform attempt.
// RecordDenied 持久化运行拒绝原因，不将其算作平台发送尝试。
func (c *Coordinator) RecordDenied(ctx context.Context, jobID int64, reason string, claim ...string) error {
	if repo, ok := c.repo.(interface {
		RecordDenied(context.Context, int64, string, ...string) error
	}); ok {
		return repo.RecordDenied(ctx, jobID, reason, claim...)
	}
	return fmt.Errorf("outbound: outcome recorder unavailable")
}

// ValidateReserved checks a prepared message against the latest limits.
// ValidateReserved 使用最新额度检查已预留的消息。
func (c *Coordinator) ValidateReserved(ctx context.Context, id string, limits domain.Limits) error {
	if repo, ok := c.repo.(interface {
		ValidateReserved(context.Context, string, domain.Limits) error
	}); ok {
		return repo.ValidateReserved(ctx, id, limits)
	}
	return fmt.Errorf("outbound: reservation validation unavailable")
}
