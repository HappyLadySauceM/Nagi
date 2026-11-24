package assembly

import (
	"context"

	intentapp "github.com/HappyLadySauceM/Nagi/internal/intent/application"
	intentdomain "github.com/HappyLadySauceM/Nagi/internal/intent/domain"
	intentinfra "github.com/HappyLadySauceM/Nagi/internal/intent/infrastructure"
	settingscontracts "github.com/HappyLadySauceM/Nagi/internal/settings/contracts"
)

func (r *semanticRouter) dispatch(ctx context.Context, decider intentapp.SemanticDecider, in intentdomain.Input, dest settingscontracts.IntentDestination, evidence *intentdomain.BackgroundEvidence) (intentdomain.SemanticAnswer, error) {
	return r.background.Dispatch(ctx, in, dest, evidence, func(runCtx context.Context, makeGuard func(context.Context) *intentapp.RequestGuard, account func(int, *intentapp.RequestGuard, intentdomain.SemanticUsage, error) error) (intentdomain.SemanticAnswer, error) {
		if local, ok := decider.(*intentinfra.LocalClient); ok {
			return local.DecideWithDispatch(runCtx, in, func(qCtx context.Context, q intentinfra.LocalQuestion) ([]float64, intentdomain.SemanticUsage, error) {
				guard := makeGuard(qCtx)
				values, usage, err := local.ScoreQuestion(intentinfra.WithRequestGuard(qCtx, guard), in, q)
				return values, usage, account(q.Index, guard, usage, err)
			})
		}
		guard := makeGuard(runCtx)
		answer, err := decider.Decide(intentinfra.WithRequestGuard(runCtx, guard), in)
		return answer, account(0, guard, answer.Usage, err)
	})
}

func (r *semanticRouter) WithSemanticResult(ctx context.Context, in intentdomain.Input, answer intentdomain.SemanticAnswer, use func() error) (string, error) {
	_, dest, _ := r.resolve(ctx)
	return r.background.WithSemanticResult(ctx, in, answer, dest, use)
}
