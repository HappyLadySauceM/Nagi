package application

import (
	"context"
	"errors"
	"fmt"
	"time"

	obcontracts "github.com/HappyLadySauceM/Nagi/internal/onebot/contracts"
	"github.com/HappyLadySauceM/Nagi/internal/outbound/domain"
)

// DeliveryLedger is the transactional portion of one platform delivery.
// DeliveryLedger 是一次平台投递的事务部分。
type DeliveryLedger interface {
	MarkSending(context.Context, string, ...string) error
	Settle(context.Context, string, Outcome, ...string) error
}

// Deliver settles a platform attempt even when its caller's deadline expires.
// Deliver 即使调用方期限结束也会落定平台投递尝试。
func Deliver(ctx context.Context, ledger DeliveryLedger, id string, send func(context.Context) (int64, error), claim ...string) (Outcome, error) {
	return DeliverWithRecheck(ctx, ledger, id, nil, send, claim...)
}

// DeliverWithRecheck repeats the caller's authorization after a policy-mandated wait.
// DeliverWithRecheck 在策略要求的等待结束后重新核对调用方授权。
func DeliverWithRecheck(ctx context.Context, ledger DeliveryLedger, id string, recheck func() error, send func(context.Context) (int64, error), claim ...string) (Outcome, error) {
	for {
		err := ledger.MarkSending(ctx, id, claim...)
		if err == nil {
			break
		}
		var delay time.Duration
		var interval *domain.IntervalWait
		var gate *domain.GateError
		switch {
		case errors.As(err, &interval):
			delay = interval.Remaining
		case errors.As(err, &gate) && gate.Reason == domain.GateInterval && gate.RetryAfter > 0:
			delay = gate.RetryAfter
		default:
			return Outcome{}, err
		}
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return Outcome{}, ctx.Err()
		case <-timer.C:
		}
		if recheck != nil {
			if err := recheck(); err != nil {
				return Outcome{}, err
			}
		}
	}
	messageID, sendErr := send(ctx)
	cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
	defer cancel()
	if messageID == 0 && errors.Is(sendErr, domain.ErrDispatchNotWritten) {
		if recorder, ok := ledger.(interface {
			CancelUnwrittenContinuation(context.Context, string, string) error
		}); ok && len(claim) == 1 {
			if err := recorder.CancelUnwrittenContinuation(cleanup, id, claim[0]); err != nil {
				return Outcome{}, fmt.Errorf("outbound: cancel unwritten continuation: %w", err)
			}
			return Outcome{Status: domain.StatusCancelled, ErrorCode: domain.GateOffline}, sendErr
		}
	}
	result := Outcome{Status: domain.StatusSent, PlatformMessageID: messageID}
	if sendErr != nil {
		result.Status = domain.StatusUnknown
		result.ErrorCode = "uncertain"
		var rejection interface{ DefiniteFailure() bool }
		if errors.As(sendErr, &rejection) && rejection.DefiniteFailure() {
			result.Status = domain.StatusFailed
			result.ErrorCode = "send_failed"
		}
		if errors.Is(sendErr, obcontracts.ErrActionTimeout) {
			result.ErrorCode = "timeout"
		}
	} else if messageID == 0 {
		result.Status = domain.StatusUnknown
		result.ErrorCode = "async_unconfirmed"
		sendErr = fmt.Errorf("outbound: platform receipt unconfirmed")
	}
	if result.Status == domain.StatusSent {
		if recorder, ok := ledger.(interface {
			RememberReceipt(context.Context, string, int64, string) error
		}); ok && len(claim) == 1 {
			// Settlement can still succeed if the preliminary receipt write fails.
			// 回执预写失败时仍尝试完成结算。
			_ = recorder.RememberReceipt(cleanup, id, result.PlatformMessageID, claim[0])
		}
	}
	for {
		err := ledger.Settle(cleanup, id, result, claim...)
		if err == nil {
			break
		}
		var transition *domain.TransitionError
		if result.Status != domain.StatusSent || errors.As(err, &transition) {
			return Outcome{}, fmt.Errorf("outbound: settle failed: %w", err)
		}
		timer := time.NewTimer(50 * time.Millisecond)
		select {
		case <-cleanup.Done():
			timer.Stop()
			return Outcome{}, fmt.Errorf("outbound: settle failed: %w", err)
		case <-timer.C:
		}
	}
	return result, sendErr
}
