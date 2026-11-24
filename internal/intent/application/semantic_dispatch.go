package application

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"time"

	intentcontracts "github.com/HappyLadySauceM/Nagi/internal/intent/contracts"
	intentdomain "github.com/HappyLadySauceM/Nagi/internal/intent/domain"
	settingscontracts "github.com/HappyLadySauceM/Nagi/internal/settings/contracts"
)

type RequestGuard struct {
	reserve func() (func(bool) intentcontracts.ReplayAttemptBudget, string)
	settle  func(bool) intentcontracts.ReplayAttemptBudget
	write   func(context.Context, string, func() error) (string, error)
	partial atomic.Bool
	unknown atomic.Int32
	sent    atomic.Int32
	failure atomic.Pointer[string]
}

var ErrIntentBackgroundDenied = errors.New("intent: request background no longer authorized")

func (g *RequestGuard) Write(ctx context.Context, endpoint string, write func() error) (string, error) {
	return g.write(ctx, endpoint, func() error {
		if err := ctx.Err(); err != nil {
			return err
		}
		settle, reason := g.reserve()
		if reason != "" {
			g.failure.Store(&reason)
			return ErrIntentBackgroundDenied
		}
		g.settle = settle
		return write()
	})
}

func (g *RequestGuard) Complete(written, partial, uncertain bool, reason string) {
	if reason != "" {
		g.failure.Store(&reason)
	}
	if written {
		g.sent.Add(1)
		g.partial.Store(partial)
	} else if uncertain {
		g.unknown.Add(1)
	}
}

func (b IntentBackground) Dispatch(ctx context.Context, in intentdomain.Input, dest settingscontracts.IntentDestination, evidence *intentdomain.BackgroundEvidence, score func(context.Context, func(context.Context) *RequestGuard, func(int, *RequestGuard, intentdomain.SemanticUsage, error) error) (intentdomain.SemanticAnswer, error)) (answer intentdomain.SemanticAnswer, err error) {
	withMemory := len(in.Background.Memories) > 0
	fence, _ := b.Grants.(settingscontracts.IntentDisclosureFence)
	var revision int64
	if fence != nil {
		var reason string
		revision, reason = fence.IntentDisclosureRevision(ctx, in.Task.BotID, in.Task.TargetID, dest, withMemory)
		if reason != "" {
			return stoppedIntent(in.FixedBackground, intentdomain.SemanticAnswer{}, reason)
		}
	}
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	var stop atomic.Pointer[string]
	stopRun := func(reason string) {
		if reason != "" && stop.CompareAndSwap(nil, &reason) {
			cancel()
		}
	}
	verify := func(ctx context.Context, endpoint string) string {
		return b.CheckDispatch(ctx, in, dest, endpoint, evidence)
	}
	checkRevision := func(ctx context.Context) string {
		if fence == nil {
			return verify(ctx, dest.Endpoint)
		}
		current, reason := fence.IntentDisclosureRevision(ctx, in.Task.BotID, in.Task.TargetID, dest, true)
		if reason == "" && current != revision {
			return "background_disclosure_denied"
		}
		return reason
	}
	watchDone := make(chan struct{})
	if withMemory {
		go func() {
			defer close(watchDone)
			ticker := time.NewTicker(40 * time.Millisecond)
			defer ticker.Stop()
			for {
				select {
				case <-runCtx.Done():
					return
				case <-ticker.C:
					probe, done := context.WithTimeout(runCtx, 150*time.Millisecond)
					reason := checkRevision(probe)
					done()
					if reason != "" && runCtx.Err() == nil {
						stopRun(reason)
					}
				}
			}
		}()
	} else {
		close(watchDone)
	}
	var mu sync.Mutex
	var sent, unknown int
	var attempts [6]*intentdomain.SemanticAttempt
	makeGuard := func(questionCtx context.Context) *RequestGuard {
		return &RequestGuard{
			reserve: func() (func(bool) intentcontracts.ReplayAttemptBudget, string) {
				return ReserveReplayRequest(questionCtx, dest.Fingerprint)
			},
			write: func(writeCtx context.Context, endpoint string, write func() error) (string, error) {
				if reason := verify(writeCtx, endpoint); reason != "" {
					if writeCtx.Err() == nil {
						stopRun(reason)
					}
					return reason, nil
				}
				if fence == nil {
					return "", write()
				}
				reason, err := fence.WithIntentDisclosure(writeCtx, in.Task.BotID, in.Task.TargetID, dest, withMemory, revision, write)
				stopRun(reason)
				return reason, err
			},
		}
	}
	account := func(question int, guard *RequestGuard, usage intentdomain.SemanticUsage, callErr error) error {
		mu.Lock()
		defer mu.Unlock()
		confirmed, unresolved := int(guard.sent.Load()), int(guard.unknown.Load())
		sent += confirmed
		unknown += unresolved
		var budget *intentdomain.SemanticAttemptBudget
		if guard.settle != nil {
			if result := guard.settle(confirmed+unresolved > 0); result.RequestReserved {
				value := intentdomain.SemanticAttemptBudget(result)
				budget = &value
			}
		}
		switch {
		case unresolved > 0:
			attempts[question] = &intentdomain.SemanticAttempt{Outcome: "dispatch_unknown", Budget: budget, Memory: withMemory, CostBasis: "unknown", ReservationHeld: true}
		case confirmed > 0:
			outcome := "sent"
			if callErr != nil {
				outcome = "response_unusable"
			}
			attempts[question] = &intentdomain.SemanticAttempt{Outcome: outcome, Budget: budget, Memory: withMemory, CostBasis: "unknown", ReservationHeld: true, PartialWrite: guard.partial.Load(), InputTokens: usage.InputValue(), OutputTokens: usage.OutputValue()}
		case budget != nil:
			attempts[question] = &intentdomain.SemanticAttempt{Outcome: "not_sent", Budget: budget, Memory: withMemory, CostBasis: "not_applicable"}
		}
		if reason := guard.failure.Load(); reason != nil {
			stopRun(*reason)
			return ErrIntentBackgroundDenied
		}
		if unresolved > 0 {
			stopRun("semantic_dispatch_unknown")
			return ErrIntentBackgroundDenied
		}
		return callErr
	}
	answer, err = score(runCtx, makeGuard, account)
	cancel()
	<-watchDone
	answer.ExternalRequests, answer.UnknownRequests = sent, unknown
	answer.Attempts = nil
	for _, attempt := range attempts {
		if attempt != nil {
			answer.Attempts = append(answer.Attempts, *attempt)
		}
	}
	if sent+unknown > 0 {
		answer.BackgroundEvidence = evidence
	}
	if reason := stop.Load(); reason != nil {
		return stoppedIntent(in.FixedBackground, answer, *reason)
	}
	if ctx.Err() != nil {
		return stoppedIntent(in.FixedBackground, answer, "semantic_response_unusable")
	}
	if answer.Available {
		if reason := verify(ctx, dest.Endpoint); reason != "" {
			return stoppedIntent(in.FixedBackground, answer, reason)
		}
		if withMemory {
			if reason := checkRevision(ctx); reason != "" {
				return stoppedIntent(in.FixedBackground, answer, reason)
			}
		}
	}
	if withMemory && answer.Available {
		answer.BindDisclosureRevision(revision)
	}
	return answer, err
}

func stoppedIntent(fixed bool, answer intentdomain.SemanticAnswer, reason string) (intentdomain.SemanticAnswer, error) {
	out := intentdomain.SemanticAnswer{BackgroundFailure: reason, BackgroundEvidence: answer.BackgroundEvidence, ExternalRequests: answer.ExternalRequests, UnknownRequests: answer.UnknownRequests, Attempts: answer.Attempts, Usage: answer.Usage}
	if fixed {
		return out, nil
	}
	return out, ErrIntentBackgroundDenied
}

func (b IntentBackground) WithSemanticResult(ctx context.Context, in intentdomain.Input, answer intentdomain.SemanticAnswer, dest settingscontracts.IntentDestination, use func() error) (string, error) {
	if !answer.Available || answer.BackgroundEvidence == nil {
		return "", use()
	}
	if dest.Fingerprint != answer.BackgroundEvidence.Destination {
		return "background_destination_changed", nil
	}
	if !in.Policy.AllowHistoryRead {
		in.Recent, in.AwaitedRequest, in.Topics = nil, nil, nil
	}
	in.Background.Memories = make([]string, len(answer.BackgroundEvidence.Memories))
	if reason := b.CheckDispatch(ctx, in, dest, dest.Endpoint, answer.BackgroundEvidence); reason != "" {
		return reason, nil
	}
	if fence, ok := b.Grants.(settingscontracts.IntentDisclosureFence); ok {
		return fence.WithIntentDisclosure(ctx, in.Task.BotID, in.Task.TargetID, dest, len(in.Background.Memories) > 0, answer.DisclosureRevision(), use)
	}
	return "", use()
}
