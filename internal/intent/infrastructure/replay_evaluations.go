// PostgreSQL repository for independently saved replay evaluations.
// 独立保存的回放评估的 PostgreSQL 仓储。
package infrastructure

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/HappyLadySauceM/Nagi/internal/intent/contracts"
	"github.com/HappyLadySauceM/Nagi/pkg/db"
)

var (
	ErrReplayEvaluationNotFound = errors.New("intent: replay evaluation not found")
)

// ReplayEvaluationsRepo stores replay results without touching decisions.
// ReplayEvaluationsRepo 在不触碰决策的情况下保存回放结果。
type ReplayEvaluationsRepo struct{ pool *db.Pool }

func NewReplayEvaluationsRepo(p *db.Pool) (*ReplayEvaluationsRepo, error) {
	if p == nil {
		return nil, fmt.Errorf("intent: pool required")
	}
	return &ReplayEvaluationsRepo{pool: p}, nil
}

// Create inserts one independent evaluation record.
// Create 插入一条独立评估记录。
func (r *ReplayEvaluationsRepo) Create(ctx context.Context, evaluation contracts.ReplayEvaluation) (contracts.ReplayEvaluation, error) {
	before, err := json.Marshal(evaluation.Before)
	if err != nil {
		return contracts.ReplayEvaluation{}, err
	}
	after, err := json.Marshal(evaluation.After)
	if err != nil {
		return contracts.ReplayEvaluation{}, err
	}
	parameters, err := json.Marshal(evaluation.Parameters)
	if err != nil {
		return contracts.ReplayEvaluation{}, err
	}
	cost, err := json.Marshal(evaluation.Cost)
	if err != nil {
		return contracts.ReplayEvaluation{}, err
	}
	reproduction, err := json.Marshal(evaluation.Reproduction)
	if err != nil {
		return contracts.ReplayEvaluation{}, err
	}
	var createdAt time.Time
	err = r.pool.QueryRow(ctx, `
INSERT INTO intent_replay_evaluations
(decision_id, bot_id, mode, name, note, changed, before_run, after_run, parameters, cost, reproduction)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)
RETURNING id, created_at`,
		evaluation.DecisionID, evaluation.BotID, evaluation.Mode, evaluation.Name, evaluation.Note,
		evaluation.Changed, before, after, parameters, cost, reproduction).Scan(&evaluation.ID, &createdAt)
	if err != nil {
		return contracts.ReplayEvaluation{}, err
	}
	evaluation.CreatedAt = createdAt
	return evaluation, nil
}

// Get loads one saved evaluation with both runs.
// Get 读取一条已保存评估及其两次运行。
func (r *ReplayEvaluationsRepo) Get(ctx context.Context, id int64) (contracts.ReplayEvaluation, error) {
	var evaluation contracts.ReplayEvaluation
	var before, after, parameters, cost, reproduction []byte
	err := r.pool.QueryRow(ctx, `
SELECT id, decision_id, bot_id, mode, name, note, changed, before_run, after_run, parameters, cost, reproduction, created_at
FROM intent_replay_evaluations WHERE id=$1`, id).Scan(
		&evaluation.ID, &evaluation.DecisionID, &evaluation.BotID, &evaluation.Mode, &evaluation.Name, &evaluation.Note,
		&evaluation.Changed, &before, &after, &parameters, &cost, &reproduction, &evaluation.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return contracts.ReplayEvaluation{}, ErrReplayEvaluationNotFound
	}
	if err != nil {
		return contracts.ReplayEvaluation{}, err
	}
	if err := json.Unmarshal(before, &evaluation.Before); err != nil {
		return contracts.ReplayEvaluation{}, err
	}
	if err := json.Unmarshal(after, &evaluation.After); err != nil {
		return contracts.ReplayEvaluation{}, err
	}
	if err := json.Unmarshal(parameters, &evaluation.Parameters); err != nil {
		return contracts.ReplayEvaluation{}, err
	}
	if err := json.Unmarshal(cost, &evaluation.Cost); err != nil {
		return contracts.ReplayEvaluation{}, err
	}
	if err := json.Unmarshal(reproduction, &evaluation.Reproduction); err != nil {
		return contracts.ReplayEvaluation{}, err
	}
	return evaluation, nil
}

// List returns saved evaluation summaries filtered by bot and/or decision.
// List 按账号和/或决策过滤返回已保存评估摘要。
func (r *ReplayEvaluationsRepo) List(ctx context.Context, query contracts.ReplayEvaluationQuery) ([]contracts.ReplayEvaluationSummary, error) {
	limit := query.Limit
	if limit <= 0 {
		limit = 20
	}
	rows, err := r.pool.Query(ctx, `
SELECT id, decision_id, bot_id, mode, name, changed, after_run, reproduction, created_at
FROM intent_replay_evaluations
WHERE ($1 = 0 OR bot_id = $1) AND ($2 = 0 OR decision_id = $2) AND ($3 = 0 OR id < $3)
ORDER BY id DESC LIMIT $4`, query.BotID, query.DecisionID, query.BeforeID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]contracts.ReplayEvaluationSummary, 0)
	for rows.Next() {
		var item contracts.ReplayEvaluationSummary
		var after, reproduction []byte
		if err := rows.Scan(&item.ID, &item.DecisionID, &item.BotID, &item.Mode, &item.Name, &item.Changed, &after, &reproduction, &item.CreatedAt); err != nil {
			return nil, err
		}
		var run contracts.Run
		if err := json.Unmarshal(after, &run); err != nil {
			return nil, err
		}
		item.Summary = run.Summary
		if err := json.Unmarshal(reproduction, &item.Reproduction); err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}
