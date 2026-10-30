// PostgreSQL recorder for intent decisions.
// 意图决策的 PostgreSQL 记录器。
package infrastructure

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/HappyLadySauceM/Nagi/internal/intent/contracts"
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

// Record inserts one decision row together with its explainable steps; both are
// written in one transaction so a decision never loses its reasons.
// Record 在一个事务内写入决策记录及其可解释步骤，
// 保证决策不会与原因脱节。
func (r *DecisionsRepo) Record(ctx context.Context, jobID int64, decision domain.FinalDecision) error {
	scores := decision.Scores
	if scores == nil {
		scores = map[string]float64{}
	}
	scoresJSON, err := json.Marshal(scores)
	if err != nil {
		return fmt.Errorf("intent: encode scores: %w", err)
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("intent: begin record decision: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var decisionID int64
	if err := tx.QueryRow(ctx, `
		INSERT INTO intent_decisions (job_id, provider, model, decision, reason_code, scores, latency_ms, ruleset_version)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		RETURNING id
	`, jobID, decision.Provider, decision.Model, string(decision.Decision),
		decision.ReasonCode, scoresJSON, decision.LatencyMS, decision.RulesetVersion,
	).Scan(&decisionID); err != nil {
		return fmt.Errorf("intent: record decision: %w", err)
	}
	for _, step := range decision.Steps {
		detail := step.Detail
		if detail == nil {
			detail = map[string]any{}
		}
		detailJSON, err := json.Marshal(detail)
		if err != nil {
			return fmt.Errorf("intent: encode step %s: %w", step.Phase, err)
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO intent_decision_steps (decision_id, step_index, phase, reason_code, detail)
			VALUES ($1, $2, $3, $4, $5)
		`, decisionID, step.Index, step.Phase, step.ReasonCode, detailJSON); err != nil {
			return fmt.Errorf("intent: record step %s: %w", step.Phase, err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("intent: commit decision: %w", err)
	}
	return nil
}

// decisionSelect is shared by the list and detail queries so both expose the
// same account and conversation columns.
// decisionSelect 被列表与详情查询共用，保证两者暴露相同的账号与会话字段。
const decisionSelect = `
	SELECT d.id, d.job_id, m.bot_id, m.conversation_id::text, d.decision, d.reason_code,
	       d.provider, d.model, d.ruleset_version, d.scores, d.latency_ms, d.created_at
	FROM intent_decisions d
	JOIN message_jobs j ON j.id = d.job_id
	JOIN messages m ON m.id = j.incoming_message_id`

// List returns the newest decisions of one account, optionally narrowed to one
// conversation and paginated by decision id.
// List 返回某账号最新的决策，可按会话过滤并按决策 id 翻页。
func (r *DecisionsRepo) List(ctx context.Context, q contracts.DecisionQuery) ([]contracts.DecisionRecord, error) {
	if q.BotID <= 0 {
		return nil, fmt.Errorf("intent: list decisions requires bot id")
	}
	limit := q.Limit
	if limit <= 0 {
		limit = 20
	}
	rows, err := r.pool.Query(ctx, decisionSelect+`
		WHERE m.bot_id = $1
		  AND ($2 = '' OR m.conversation_id = $2::uuid)
		  AND ($3 = 0 OR d.id < $3)
 AND ($5 = '' OR d.reason_code=$5)
		ORDER BY d.id DESC
		LIMIT $4
	`, q.BotID, q.ConversationID, q.BeforeID, limit, q.ReasonCode)
	if err != nil {
		return nil, fmt.Errorf("intent: list decisions: %w", err)
	}
	defer rows.Close()
	var out []contracts.DecisionRecord
	for rows.Next() {
		record, err := scanDecision(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, record)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("intent: scan decisions: %w", err)
	}
	return out, nil
}

// Get loads one decision with its explainable steps.
// Get 读取一条决策及其可解释步骤。
func (r *DecisionsRepo) Get(ctx context.Context, id int64) (contracts.DecisionRecord, error) {
	record, err := scanDecision(r.pool.QueryRow(ctx, decisionSelect+` WHERE d.id = $1`, id))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return contracts.DecisionRecord{}, ErrDecisionNotFound
		}
		return contracts.DecisionRecord{}, fmt.Errorf("intent: get decision: %w", err)
	}
	rows, err := r.pool.Query(ctx, `
		SELECT step_index, phase, reason_code, detail, created_at
		FROM intent_decision_steps WHERE decision_id = $1 ORDER BY step_index
	`, id)
	if err != nil {
		return contracts.DecisionRecord{}, fmt.Errorf("intent: get decision steps: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var (
			step   contracts.DecisionStep
			detail []byte
		)
		if err := rows.Scan(&step.Index, &step.Phase, &step.ReasonCode, &detail, &step.CreatedAt); err != nil {
			return contracts.DecisionRecord{}, fmt.Errorf("intent: scan decision step: %w", err)
		}
		if len(detail) > 0 {
			if err := json.Unmarshal(detail, &step.Detail); err != nil {
				return contracts.DecisionRecord{}, fmt.Errorf("intent: decode step detail: %w", err)
			}
		}
		record.Steps = append(record.Steps, step)
	}
	if err := rows.Err(); err != nil {
		return contracts.DecisionRecord{}, fmt.Errorf("intent: scan decision steps: %w", err)
	}
	return record, nil
}

// ErrDecisionNotFound reports a decision id that does not exist.
// ErrDecisionNotFound 表示决策 id 不存在。
var ErrDecisionNotFound = errors.New("intent: decision not found")

// scanDecision decodes one row of decisionSelect.
// scanDecision 解码 decisionSelect 的一行。
func scanDecision(row pgx.Row) (contracts.DecisionRecord, error) {
	var (
		record       contracts.DecisionRecord
		scores       []byte
		conversation string
	)
	err := row.Scan(&record.ID, &record.JobID, &record.BotID, &conversation, &record.Decision,
		&record.ReasonCode, &record.Provider, &record.Model, &record.RulesetVersion,
		&scores, &record.LatencyMS, &record.CreatedAt)
	if err != nil {
		return contracts.DecisionRecord{}, err
	}
	record.ConversationID = conversation
	if len(scores) > 0 {
		if err := json.Unmarshal(scores, &record.Scores); err != nil {
			return contracts.DecisionRecord{}, fmt.Errorf("intent: decode scores: %w", err)
		}
	}
	return record, nil
}

func (r *DecisionsRepo) Stats(ctx context.Context, botID int64, since time.Time) (contracts.DecisionStats, error) {
	var result contracts.DecisionStats
	err := r.pool.QueryRow(ctx, `
 WITH decisions AS (
 SELECT d.decision,d.latency_ms,
 EXISTS(SELECT 1 FROM intent_decision_steps s WHERE s.decision_id=d.id AND s.phase='semantic') AS called,
 EXISTS(SELECT 1 FROM intent_decision_steps s WHERE s.decision_id=d.id AND s.phase='semantic' AND s.detail->>'available'='true') AS success
 FROM intent_decisions d
 JOIN message_jobs j ON j.id=d.job_id
 JOIN messages m ON m.id=j.incoming_message_id
 WHERE m.bot_id=$1 AND d.created_at >= $2
 )
 SELECT count(*),count(*) FILTER(WHERE called),count(*) FILTER(WHERE success),
 count(*) FILTER(WHERE called AND NOT success),count(*) FILTER(WHERE decision='reply'),
 count(*) FILTER(WHERE decision='silent'),COALESCE(avg(latency_ms) FILTER(WHERE called),0)
 FROM decisions`, botID, since).Scan(&result.Total, &result.SemanticCalls, &result.SemanticSuccess, &result.Degraded, &result.Replies, &result.Silent, &result.AverageLatencyMS)
	return result, err
}
