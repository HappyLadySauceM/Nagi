package application

import (
	"context"
	"strings"
	"sync"

	"github.com/HappyLadySauceM/Nagi/internal/intent/contracts"
)

type replayBudgetKey struct{}

type replayBudget struct {
	mu           sync.Mutex
	plan         contracts.ReplayBudgetPlan
	destination  string
	requestsUsed int
	amountUsed   int64
}

func (b *replayBudget) summary() contracts.ReplayBudgetBalance {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := contracts.ReplayBudgetBalance{RequestLimit: b.plan.RequestLimit, RemainingRequests: b.plan.RequestLimit - b.requestsUsed}
	if b.plan.AmountLimitMicros != nil {
		remaining := *b.plan.AmountLimitMicros - b.amountUsed
		out.Currency = b.plan.Currency
		out.AmountLimitMicros = b.plan.AmountLimitMicros
		out.RemainingAmountMicros = &remaining
	}
	return out
}

func (b *replayBudget) validationReason() string {
	if b.plan.RequestLimit < 0 {
		return "semantic_replay_budget_exhausted"
	}
	if b.plan.AmountLimitMicros == nil || b.plan.RequestLimit == 0 {
		return ""
	}
	if b.destination == "" || b.plan.Destination != b.destination || len(b.plan.Currency) != 3 || strings.ToUpper(b.plan.Currency) != b.plan.Currency || *b.plan.AmountLimitMicros < 0 || b.plan.RequestUpperBoundMicros == nil || *b.plan.RequestUpperBoundMicros <= 0 || b.plan.PriceVersion == "" {
		return "semantic_replay_price_unverified"
	}
	if *b.plan.RequestUpperBoundMicros > *b.plan.AmountLimitMicros {
		return "semantic_replay_budget_exhausted"
	}
	return ""
}

func ReserveReplayRequest(ctx context.Context, destination string) (settle func(bool) contracts.ReplayAttemptBudget, reason string) {
	b, _ := ctx.Value(replayBudgetKey{}).(*replayBudget)
	if b == nil {
		return func(bool) contracts.ReplayAttemptBudget { return contracts.ReplayAttemptBudget{} }, ""
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.destination != destination {
		return nil, "background_destination_changed"
	}
	if b.requestsUsed >= b.plan.RequestLimit {
		return nil, "semantic_replay_budget_exhausted"
	}
	var upper int64
	if b.plan.AmountLimitMicros != nil {
		upper = *b.plan.RequestUpperBoundMicros
		if upper > *b.plan.AmountLimitMicros-b.amountUsed {
			return nil, "semantic_replay_budget_exhausted"
		}
	}
	b.requestsUsed++
	b.amountUsed += upper
	var once sync.Once
	var result contracts.ReplayAttemptBudget
	return func(held bool) contracts.ReplayAttemptBudget {
		once.Do(func() {
			b.mu.Lock()
			defer b.mu.Unlock()
			result.RequestReserved = true
			result.RequestHeld = held
			if !held {
				b.requestsUsed--
				b.amountUsed -= upper
			}
			result.RemainingRequests = b.plan.RequestLimit - b.requestsUsed
			if b.plan.AmountLimitMicros != nil {
				remaining, charged := *b.plan.AmountLimitMicros-b.amountUsed, upper
				if !held {
					charged = 0
				}
				result.ReservedAmountMicros, result.HeldAmountMicros, result.RemainingAmountMicros = &upper, &charged, &remaining
				result.ReservationCurrency, result.PriceVersion = b.plan.Currency, b.plan.PriceVersion
			}
		})
		return result
	}, ""
}
