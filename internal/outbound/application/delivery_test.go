package application

import (
	"context"
	"errors"
	"github.com/HappyLadySauceM/Nagi/internal/outbound/domain"
	"io"
	"testing"
)

type deliveryLedger struct {
	outcome       Outcome
	settleContext error
}

func (l *deliveryLedger) MarkSending(_ context.Context, _ string, _ ...string) error { return nil }
func (l *deliveryLedger) Settle(ctx context.Context, _ string, o Outcome, claim ...string) error {
	l.outcome = o
	l.settleContext = ctx.Err()
	return nil
}

type rejected struct{}

func (rejected) Error() string         { return "rejected" }
func (rejected) DefiniteFailure() bool { return true }
func TestDeliveryClassifiesAmbiguousOutcomes(t *testing.T) {
	for _, tc := range []struct {
		name string
		id   int64
		err  error
		want domain.Status
	}{{"confirmed", 1, nil, domain.StatusSent}, {"disconnected", 0, io.EOF, domain.StatusUnknown}, {"rejected", 0, rejected{}, domain.StatusFailed}, {"async", 0, nil, domain.StatusUnknown}} {
		t.Run(tc.name, func(t *testing.T) {
			l := &deliveryLedger{}
			o, _ := Deliver(context.Background(), l, "id", func(context.Context) (int64, error) { return tc.id, tc.err })
			if o.Status != tc.want || l.outcome.Status != tc.want {
				t.Fatalf("outcome %+v", o)
			}
		})
	}
}
func TestDeliverySettlesAfterCallerCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	l := &deliveryLedger{}
	o, err := Deliver(ctx, l, "id", func(context.Context) (int64, error) { cancel(); return 0, context.Canceled })
	if !errors.Is(err, context.Canceled) || o.Status != domain.StatusUnknown || l.settleContext != nil {
		t.Fatalf("outcome %+v error %v settle context %v", o, err, l.settleContext)
	}
}
