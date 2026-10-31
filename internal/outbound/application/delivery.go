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
	if err := ledger.MarkSending(ctx, id, claim...); err != nil {
		return Outcome{}, err
	}
	messageID, sendErr := send(ctx)
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
	cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
	defer cancel()
	if err := ledger.Settle(cleanup, id, result, claim...); err != nil {
		return Outcome{}, fmt.Errorf("outbound: settle failed: %w", err)
	}
	return result, sendErr
}
