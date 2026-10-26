// PostgreSQL recorder for intent decisions.
// 意图决策的 PostgreSQL 记录器。
package infrastructure

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/HappyLadySauceM/Nagi/internal/intent/domain"
)

// DecisionsRepo persists final decisions into intent_decisions.
// DecisionsRepo 将最终决策持久化到 intent_decisions。
type DecisionsRepo struct {
	pool *pgxpool.Pool
}

// NewDecisionsRepo builds the repository; the pool must not be nil.
// NewDecisionsRepo 构建仓库；pool 不能为 nil。
func NewDecisionsRepo(pool *pgxpool.Pool) (*DecisionsRepo, error) {
	if pool == nil {
		return nil, fmt.Errorf("intent: pool required")
	}
	return &DecisionsRepo{pool: pool}, nil
}

// Record inserts one decision row; scores are stored as JSONB.
// Record 插入一条决策记录；分数以 JSONB 存储。
func (r *DecisionsRepo) Record(ctx context.Context, jobID int64, decision domain.FinalDecision) error {
	scores := decision.Scores
	if scores == nil {
		scores = map[string]float64{}
	}
	scoresJSON, err := json.Marshal(scores)
	if err != nil {
		return fmt.Errorf("intent: encode scores: %w", err)
	}
	_, err = r.pool.Exec(ctx, `
		INSERT INTO intent_decisions (job_id, provider, model, decision, reason_code, scores, latency_ms)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
	`, jobID, decision.Provider, decision.Model, string(decision.Decision),
		decision.ReasonCode, scoresJSON, decision.LatencyMS)
	if err != nil {
		return fmt.Errorf("intent: record decision: %w", err)
	}
	return nil
}
