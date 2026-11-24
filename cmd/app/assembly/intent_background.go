package assembly

import (
	"context"
	"errors"
	intentcontracts "github.com/HappyLadySauceM/Nagi/internal/intent/contracts"
	"sync/atomic"
)

type intentRequestGuardKey struct{}

type intentRequestGuard struct {
	settle  func(bool) intentcontracts.ReplayAttemptBudget
	check   func(context.Context, string) string
	partial atomic.Bool
	unknown atomic.Int32
	sent    atomic.Int32
	failure atomic.Pointer[string]
}

var errIntentBackgroundDenied = errors.New("intent: request background no longer authorized")
