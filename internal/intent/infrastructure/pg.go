// PostgreSQL recorder for intent decisions.
// 意图决策的 PostgreSQL 记录器。
package infrastructure

import (
	"github.com/HappyLadySauceM/Nagi/pkg/db/ent"
	d "github.com/HappyLadySauceM/Nagi/pkg/db/ent/intentdecision"
	ds "github.com/HappyLadySauceM/Nagi/pkg/db/ent/intentdecisionstep"
	m "github.com/HappyLadySauceM/Nagi/pkg/db/ent/message"
	j "github.com/HappyLadySauceM/Nagi/pkg/db/ent/messagejob"

	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/google/uuid"
	"time"

	"github.com/HappyLadySauceM/Nagi/pkg/db"

	"github.com/HappyLadySauceM/Nagi/internal/intent/contracts"
	"github.com/HappyLadySauceM/Nagi/internal/intent/domain"
)

// DecisionsRepo persists final decisions into intent_decisions.
// DecisionsRepo 将最终决策持久化到 intent_decisions。
type DecisionsRepo struct {
	pool *db.Pool
}

// NewDecisionsRepo builds the repository; the pool must not be nil.
// NewDecisionsRepo 构建仓库；pool 不能为 nil。
func NewDecisionsRepo(pool *db.Pool) (*DecisionsRepo, error) {
	if pool == nil {
		return nil, fmt.Errorf("intent: pool required")
	}
	return &DecisionsRepo{pool: pool}, nil
}

// Record inserts one decision row together with its explainable steps; both are
// written in one transaction so a decision never loses its reasons.
// Record 在一个事务内写入决策记录及其可解释步骤，
// 保证决策不会与原因脱节。
func (r *DecisionsRepo) Record(ctx context.Context, jobID int64, decision domain.FinalDecision, claim ...string) error {
	if len(claim) != 1 || claim[0] == "" {
		return fmt.Errorf("intent: claim required")
	}
	token, err := uuid.Parse(claim[0])
	if err != nil {
		return err
	}
	_ = token
	tx, err := r.pool.Client.Tx(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	client := tx.Client()
	rows, err := client.QueryContext(ctx, `SELECT c.id FROM conversations c JOIN messages m ON m.conversation_id=c.id JOIN message_jobs j ON j.incoming_message_id=m.id WHERE j.id=$1 FOR UPDATE OF c`, jobID)
	if err != nil {
		return err
	}
	var id string
	if !rows.Next() {
		rows.Close()
		return fmt.Errorf("intent: job missing")
	}
	err = rows.Scan(&id)
	rows.Close()
	if err != nil {
		return err
	}
	rows, err = client.QueryContext(ctx, `SELECT id FROM message_jobs WHERE id=$1 AND status='processing' AND claim_token=$2::uuid AND lease_expires_at>clock_timestamp() FOR UPDATE`, jobID, claim[0])
	if err != nil {
		return err
	}
	valid := rows.Next()
	rows.Close()
	if !valid {
		return fmt.Errorf("intent: lease lost")
	}
	scores := decision.Scores
	if scores == nil {
		scores = map[string]float64{}
	}
	data, err := json.Marshal(scores)
	if err != nil {
		return err
	}
	record, err := client.IntentDecision.Create().SetJobID(jobID).SetProvider(decision.Provider).SetModel(decision.Model).SetDecision(string(decision.Decision)).SetReasonCode(decision.ReasonCode).SetScores(data).SetLatencyMs(int(decision.LatencyMS)).SetRulesetVersion(decision.RulesetVersion).Save(ctx)
	if err != nil {
		return err
	}
	for _, step := range decision.Steps {
		detail := step.Detail
		if detail == nil {
			detail = map[string]any{}
		}
		data, err := json.Marshal(detail)
		if err != nil {
			return err
		}
		if err := client.IntentDecisionStep.Create().SetDecisionID(record.ID).SetStepIndex(step.Index).SetPhase(step.Phase).SetReasonCode(step.ReasonCode).SetDetail(data).Exec(ctx); err != nil {
			return err
		}
	}
	rows, err = client.QueryContext(ctx, "SELECT EXISTS(SELECT 1 FROM message_jobs WHERE id=$1 AND status='processing' AND claim_token=$2::uuid AND lease_expires_at>clock_timestamp())", jobID, claim[0])
	if err != nil {
		return err
	}
	valid = false
	if rows.Next() {
		err = rows.Scan(&valid)
	}
	scanErr := rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	if scanErr != nil {
		return scanErr
	}
	if !valid {
		return fmt.Errorf("intent: lease lost before commit")
	}
	return tx.Commit()
}

// List returns the newest decisions of one account, optionally narrowed to one
// conversation and paginated by decision id.
// List 返回某账号最新的决策，可按会话过滤并按决策 id 翻页。
func (r *DecisionsRepo) List(ctx context.Context, q contracts.DecisionQuery) ([]contracts.DecisionRecord, error) {
	if q.BotID <= 0 {
		return nil, fmt.Errorf("intent: list decisions requires bot id")
	}
	query := r.pool.Client.IntentDecision.Query().Where(d.HasJobIDRefWith(j.HasIncomingMessageIDRefWith(m.BotID(q.BotID))))
	if q.ConversationID != "" {
		id, err := uuid.Parse(q.ConversationID)
		if err != nil {
			return nil, err
		}
		query.Where(d.HasJobIDRefWith(j.HasIncomingMessageIDRefWith(m.ConversationID(id))))
	}
	if q.BeforeID > 0 {
		query.Where(d.IDLT(q.BeforeID))
	}
	if q.ReasonCode != "" {
		query.Where(d.ReasonCode(q.ReasonCode))
	}
	limit := q.Limit
	if limit <= 0 {
		limit = 20
	}
	rows, err := query.WithJobIDRef(func(q *ent.MessageJobQuery) { q.WithIncomingMessageIDRef() }).Order(ent.Desc(d.FieldID)).Limit(limit).All(ctx)
	if err != nil {
		return nil, err
	}
	var out []contracts.DecisionRecord
	for _, v := range rows {
		record, err := decisionModel(v)
		if err != nil {
			return nil, err
		}
		out = append(out, record)
	}
	return out, nil
}

// Get loads one decision with its explainable steps.
// Get 读取一条决策及其可解释步骤。
func (r *DecisionsRepo) Get(ctx context.Context, id int64) (contracts.DecisionRecord, error) {
	v, err := r.pool.Client.IntentDecision.Query().Where(d.ID(id)).WithJobIDRef(func(q *ent.MessageJobQuery) { q.WithIncomingMessageIDRef() }).Only(ctx)
	if ent.IsNotFound(err) {
		return contracts.DecisionRecord{}, ErrDecisionNotFound
	}
	if err != nil {
		return contracts.DecisionRecord{}, err
	}
	record, err := decisionModel(v)
	if err != nil {
		return record, err
	}
	steps, err := r.pool.Client.IntentDecisionStep.Query().Where(ds.DecisionID(id)).Order(ent.Asc(ds.FieldStepIndex)).All(ctx)
	if err != nil {
		return record, err
	}
	for _, v := range steps {
		step := contracts.DecisionStep{Index: v.StepIndex, Phase: v.Phase, ReasonCode: v.ReasonCode, CreatedAt: v.CreatedAt}
		if len(v.Detail) > 0 {
			if err := json.Unmarshal(v.Detail, &step.Detail); err != nil {
				return record, err
			}
		}
		record.Steps = append(record.Steps, step)
	}
	return record, nil
}

// ErrDecisionNotFound reports a decision id that does not exist.
// ErrDecisionNotFound 表示决策 id 不存在。
var ErrDecisionNotFound = errors.New("intent: decision not found")

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

func decisionModel(v *ent.IntentDecision) (contracts.DecisionRecord, error) {
	record := contracts.DecisionRecord{ID: v.ID, JobID: v.JobID, Decision: v.Decision, ReasonCode: v.ReasonCode, Provider: v.Provider, Model: v.Model, RulesetVersion: v.RulesetVersion, LatencyMS: v.LatencyMs, CreatedAt: v.CreatedAt}
	job, err := v.Edges.JobIDRefOrErr()
	if err != nil {
		return record, err
	}
	message, err := job.Edges.IncomingMessageIDRefOrErr()
	if err != nil {
		return record, err
	}
	record.BotID = message.BotID
	record.ConversationID = message.ConversationID.String()
	if len(v.Scores) > 0 {
		err = json.Unmarshal(v.Scores, &record.Scores)
	}
	return record, err
}
