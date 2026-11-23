// PostgreSQL recorder for intent decisions.
// 意图决策的 PostgreSQL 记录器。
package infrastructure

import (
	entsql "entgo.io/ent/dialect/sql"
	"github.com/HappyLadySauceM/Nagi/pkg/db/ent"
	d "github.com/HappyLadySauceM/Nagi/pkg/db/ent/intentdecision"
	dsnap "github.com/HappyLadySauceM/Nagi/pkg/db/ent/intentdecisionsnapshot"
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
	return r.record(ctx, jobID, decision, "", claim...)
}

func (r *DecisionsRepo) RecordDecisionRun(ctx context.Context, jobID int64, decision domain.FinalDecision, claim string) error {
	return r.record(ctx, jobID, decision, decision.RunID, claim)
}

func (r *DecisionsRepo) record(ctx context.Context, jobID int64, decision domain.FinalDecision, live string, claim ...string) error {
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
	rows, err = client.QueryContext(ctx, `SELECT attempts FROM message_jobs WHERE id=$1 AND status='processing' AND claim_token=$2::uuid AND lease_expires_at>clock_timestamp()`, jobID, claim[0])
	if err != nil {
		return err
	}
	var attempts int
	if !rows.Next() {
		rows.Close()
		return fmt.Errorf("intent: lease lost")
	}
	err = rows.Scan(&attempts)
	rows.Close()
	if err != nil {
		return err
	}
	if decision.Topic != nil && decision.Topic.Action != "personal_exit" {
		if err := persistTopicChange(ctx, client, id, jobID, decision.Topic); err != nil {
			return err
		}
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
	var liveBot int64
	if live != "" {
		var state string
		if err := scanLive(ctx, client, `SELECT r.bot_id,r.state FROM intent_decision_runs r JOIN message_jobs j ON j.id=r.job_id
		 WHERE r.id=$1 AND j.id=$2 AND j.claim_token=$3::uuid
		 AND (r.claim_token=j.claim_token OR (j.last_error_code='decision_resume' AND r.recovery_decision IS NOT NULL)) FOR UPDATE OF r`, []any{live, jobID, claim[0]}, &liveBot, &state); err != nil {
			return err
		}
		if state == "completed" {
			return nil
		}
		if state != "running" {
			return fmt.Errorf("intent: run has ended")
		}
		if err := lockDecisionHead(ctx, client, liveBot); err != nil {
			return err
		}
		if err := checkDecisionLease(ctx, client, jobID, claim[0]); err != nil {
			return err
		}
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
		} else if events, ok := detail["RuleEvents"].([]domain.CooldownEvent); ok {
			bound := append([]domain.CooldownEvent(nil), events...)
			for i := range bound {
				bound[i].Attempt = attempts + 1
			}
			fields := make(map[string]any, len(detail))
			for key, value := range detail {
				fields[key] = value
			}
			fields["RuleEvents"] = bound
			detail = fields
		}
		data, err := json.Marshal(detail)
		if err != nil {
			return err
		}
		evidence, err := json.Marshal(evidencePayload(step.Evidence))
		if err != nil {
			return err
		}
		if err := client.IntentDecisionStep.Create().SetDecisionID(record.ID).SetStepIndex(step.Index).SetPhase(step.Phase).
			SetNodeID(step.NodeID).SetNodeVersion(step.NodeVersion).SetStatus(step.Status).SetBranch(step.Branch).
			SetSummary(step.Summary).SetReasonCode(step.ReasonCode).SetDetail(data).SetEvidence(evidence).
			SetDurationMs(int(step.DurationMS)).Exec(ctx); err != nil {
			return err
		}
	}
	if decision.Snapshot != nil {
		input, err := json.Marshal(decision.Snapshot.Input)
		if err != nil {
			return err
		}
		semantic, err := json.Marshal(decision.Snapshot.Semantic)
		if err != nil {
			return err
		}
		prompt, err := json.Marshal(decision.Snapshot.Prompt)
		if err != nil {
			return err
		}
		rule, err := json.Marshal(decision.Snapshot.Rule)
		if err != nil {
			return err
		}
		missing, err := json.Marshal(decision.Snapshot.MissingFields)
		if err != nil {
			return err
		}
		rulesetVersion := decision.Snapshot.RulesetVersion
		if rulesetVersion == "" {
			rulesetVersion = decision.RulesetVersion
		}
		if _, err := client.IntentDecisionSnapshot.Create().
			SetDecisionID(record.ID).SetRunID(decision.Snapshot.RunID).SetFlowVersion(decision.Snapshot.FlowVersion).
			SetSnapshotVersion(decision.Snapshot.Version).SetInput(input).SetSemantic(semantic).SetPrompt(prompt).
			SetRule(rule).SetRulesetVersion(rulesetVersion).SetMissingFields(missing).Save(ctx); err != nil {
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
	if live != "" {
		if err := finishLiveDecision(ctx, client, liveBot, live, jobID, claim[0], 8, contracts.LiveDecisionFacts{State: "completed", DecisionID: record.ID, Decision: string(decision.Decision), ReasonCode: decision.ReasonCode}); err != nil {
			return err
		}
	}
	err = tx.Commit()
	if err != nil && live != "" {
		probe, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
		defer cancel()
		var exists bool
		if r.pool.QueryRow(probe, `SELECT EXISTS(SELECT 1 FROM intent_decision_runs WHERE id=$1 AND decision_id=$2 AND state='completed')`, live, record.ID).Scan(&exists) == nil && exists {
			return nil
		}
	}
	return err
}

// List returns the newest decisions of one account, optionally narrowed to one
// conversation and paginated by decision id.
// List 返回某账号最新的决策，可按会话过滤并按决策 id 翻页。
func (r *DecisionsRepo) List(ctx context.Context, q contracts.DecisionQuery) ([]contracts.DecisionRecord, error) {
	if q.BotID <= 0 {
		return nil, fmt.Errorf("intent: list decisions requires bot id")
	}
	messages := m.BotID(q.BotID)
	query := r.pool.Client.IntentDecision.Query().Where(d.HasJobIDRefWith(j.HasIncomingMessageIDRefWith(messages)))
	if q.AuditVisibleOnly {
		query.Where(d.HasJobIDRefWith(j.HasIncomingMessageIDRefWith(func(s *entsql.Selector) {
			s.Where(entsql.P(func(b *entsql.Builder) {
				b.Ident(s.C(m.FieldConversationID)).WriteString(" IN (SELECT c.id" + livePolicyJoin).Arg(q.BotID).WriteByte(')')
			}))
		})))
	}
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
	ids := make([]int64, 0, len(rows))
	for _, v := range rows {
		record, err := decisionModel(v)
		if err != nil {
			return nil, err
		}
		ids = append(ids, v.ID)
		out = append(out, record)
	}
	if len(ids) > 0 {
		snapshots, err := r.pool.Client.IntentDecisionSnapshot.Query().Where(dsnap.DecisionIDIn(ids...)).All(ctx)
		if err != nil {
			return nil, err
		}
		byDecision := make(map[int64]*ent.IntentDecisionSnapshot, len(snapshots))
		for _, snapshot := range snapshots {
			byDecision[snapshot.DecisionID] = snapshot
		}
		for i := range out {
			if snapshot, ok := byDecision[out[i].ID]; ok {
				out[i].RunID = snapshot.RunID
				out[i].FlowVersion = snapshot.FlowVersion
				out[i].MissingFields = []string{}
				out[i].SnapshotStatus = domain.SnapshotStatusComplete
				if len(snapshot.Prompt) > 0 {
					var prompt domain.Prompt
					if err := json.Unmarshal(snapshot.Prompt, &prompt); err == nil {
						out[i].Prompt = promptInfo(prompt)
					}
				}
				if len(snapshot.MissingFields) > 0 {
					var missing []string
					if err := json.Unmarshal(snapshot.MissingFields, &missing); err == nil && len(missing) > 0 {
						out[i].MissingFields = missing
						out[i].SnapshotStatus = domain.SnapshotStatusPartial
					}
				}
			}
		}
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
		step := contracts.DecisionStep{Index: v.StepIndex, Phase: v.Phase, NodeID: v.NodeID,
			NodeVersion: v.NodeVersion, Status: v.Status, Branch: v.Branch, Summary: v.Summary,
			ReasonCode: v.ReasonCode, DurationMS: int64(v.DurationMs), CreatedAt: v.CreatedAt}
		if len(v.Detail) > 0 {
			if err := json.Unmarshal(v.Detail, &step.Detail); err != nil {
				return record, err
			}
		}
		if len(v.Evidence) > 0 {
			var evidence []domain.Evidence
			if err := json.Unmarshal(v.Evidence, &evidence); err != nil {
				return record, err
			}
			step.Evidence = evidencePayloadToContract(evidence)
		}
		record.Steps = append(record.Steps, step)
	}
	if snapshot, err := r.snapshotByDecision(ctx, id); err == nil {
		record.RunID = snapshot.RunID
		record.FlowVersion = snapshot.FlowVersion
		record.MissingFields = snapshot.MissingFields
		record.SnapshotStatus = snapshot.SnapshotStatus()
		record.Prompt = promptInfo(snapshot.Prompt)
	} else if !errors.Is(err, domain.ErrSnapshotNotFound) {
		return record, err
	} else {
		record.SnapshotStatus = domain.SnapshotStatusLegacy
	}
	return record, nil
}

// ErrDecisionNotFound reports a decision id that does not exist.
// ErrDecisionNotFound 表示决策 id 不存在。
var ErrDecisionNotFound = errors.New("intent: decision not found")

func (r *DecisionsRepo) Stats(ctx context.Context, botID int64, since time.Time) (contracts.DecisionStats, error) {
	var result contracts.DecisionStats
	err := r.pool.QueryRow(ctx, liveScope+`
 , decisions AS (
 SELECT d.decision,d.latency_ms,
 EXISTS(SELECT 1 FROM intent_decision_steps s WHERE s.decision_id=d.id AND s.phase='semantic') AS called,
 EXISTS(SELECT 1 FROM intent_decision_steps s WHERE s.decision_id=d.id AND s.phase='semantic' AND COALESCE(s.detail->>'Available', s.detail->>'available')='true') AS success
 FROM intent_decisions d
 JOIN message_jobs j ON j.id=d.job_id
 JOIN messages m ON m.id=j.incoming_message_id JOIN visible v ON v.id=m.conversation_id
 WHERE m.bot_id=$1 AND d.created_at >= $2
 )
 SELECT count(*),count(*) FILTER(WHERE called),count(*) FILTER(WHERE success),
 count(*) FILTER(WHERE called AND NOT success),count(*) FILTER(WHERE decision='reply'),
 count(*) FILTER(WHERE decision='silent'),COALESCE(avg(latency_ms) FILTER(WHERE called),0)
 FROM decisions`, botID, since).Scan(&result.Total, &result.SemanticCalls, &result.SemanticSuccess, &result.Degraded, &result.Replies, &result.Silent, &result.AverageLatencyMS)
	return result, err
}

// Snapshot loads the exact input/semantic/prompt snapshot of one decision.
// Snapshot 读取一条决策的精确输入、语义与 Prompt 快照。
func (r *DecisionsRepo) Snapshot(ctx context.Context, decisionID int64) (domain.DecisionSnapshot, error) {
	return r.snapshotByDecision(ctx, decisionID)
}

func (r *DecisionsRepo) snapshotByDecision(ctx context.Context, decisionID int64) (domain.DecisionSnapshot, error) {
	row, err := r.pool.Client.IntentDecisionSnapshot.Query().Where(dsnap.DecisionID(decisionID)).Only(ctx)
	if ent.IsNotFound(err) {
		return domain.DecisionSnapshot{}, domain.ErrSnapshotNotFound
	}
	if err != nil {
		return domain.DecisionSnapshot{}, err
	}
	snap := domain.DecisionSnapshot{Version: row.SnapshotVersion, FlowVersion: row.FlowVersion, RunID: row.RunID}
	if len(row.Input) > 0 {
		if err := json.Unmarshal(row.Input, &snap.Input); err != nil {
			return snap, err
		}
	}
	if len(row.Semantic) > 0 {
		if err := json.Unmarshal(row.Semantic, &snap.Semantic); err != nil {
			return snap, err
		}
	}
	if len(row.Prompt) > 0 {
		if err := json.Unmarshal(row.Prompt, &snap.Prompt); err != nil {
			return snap, err
		}
	}
	ruleLoaded := false
	if len(row.Rule) > 0 {
		var raw map[string]json.RawMessage
		if err := json.Unmarshal(row.Rule, &raw); err != nil {
			return snap, err
		}
		if len(raw) > 0 {
			if err := json.Unmarshal(row.Rule, &snap.Rule); err != nil {
				return snap, err
			}
			ruleLoaded = true
		}
	}
	snap.RulesetVersion = row.RulesetVersion
	if len(row.MissingFields) > 0 {
		if err := json.Unmarshal(row.MissingFields, &snap.MissingFields); err != nil {
			return snap, err
		}
	}
	if !ruleLoaded {
		snap.MissingFields = append(snap.MissingFields, "rule")
	}
	return snap, nil
}

func promptInfo(prompt domain.Prompt) contracts.PromptInfo {
	finalHash := prompt.FinalHash
	if finalHash == "" {
		finalHash = prompt.Hash
	}
	return contracts.PromptInfo{
		ID: prompt.ID, Kind: prompt.Kind, Name: prompt.Name, Version: prompt.Version,
		Source: prompt.Source, SourceRef: prompt.SourceRef, Hash: prompt.Hash,
		FinalHash: finalHash, RenderedHash: prompt.RenderedHash, Content: domain.PromptContentJSON(prompt),
		TargetType: prompt.TargetType, TargetID: prompt.TargetID, ConversationID: prompt.ConversationID, Layers: prompt.Layers,
	}
}

func evidencePayload(items []domain.Evidence) []map[string]string {
	out := make([]map[string]string, 0, len(items))
	for _, item := range items {
		out = append(out, map[string]string{"Kind": item.Kind, "Text": item.Text})
	}
	return out
}

func evidencePayloadToContract(items []domain.Evidence) []contracts.EvidenceItem {
	out := make([]contracts.EvidenceItem, 0, len(items))
	for _, item := range items {
		out = append(out, contracts.EvidenceItem{Kind: item.Kind, Text: item.Text})
	}
	return out
}

func decisionModel(v *ent.IntentDecision) (contracts.DecisionRecord, error) {
	record := contracts.DecisionRecord{ID: v.ID, JobID: v.JobID, Decision: v.Decision, ReasonCode: v.ReasonCode, Provider: v.Provider, Model: v.Model, RulesetVersion: v.RulesetVersion, LatencyMS: v.LatencyMs, CreatedAt: v.CreatedAt, SnapshotStatus: domain.SnapshotStatusLegacy, MissingFields: []string{}}
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

// AwaitedRequest finds the newest unconsumed wait for this account, conversation and sender.
// AwaitedRequest 查找该账号、会话和发送者最新且未接续的等待。
func (r *DecisionsRepo) AwaitedRequest(ctx context.Context, task domain.Task) (*domain.Message, error) {
	rows, err := r.pool.Query(ctx, `
	SELECT COALESCE(previous.platform_message_id, 0), previous.sender_id, previous.text_content, previous.created_at
	FROM message_jobs current_job
	JOIN messages current ON current.id = current_job.incoming_message_id
	JOIN messages previous ON previous.conversation_id = current.conversation_id
		AND previous.bot_id = current.bot_id AND previous.sender_id = current.sender_id
		AND previous.direction = 'incoming'
		AND (previous.created_at, previous.id) < (current.created_at, current.id)
		AND previous.created_at >= current.created_at - interval '30 seconds'
	JOIN message_jobs previous_job ON previous_job.incoming_message_id = previous.id
	JOIN LATERAL (
		SELECT decision, reason_code FROM intent_decisions
		WHERE job_id = previous_job.id ORDER BY id DESC LIMIT 1
	) verdict ON true
	WHERE current_job.id = $1 AND current.bot_id = $2 AND previous_job.status = 'done'
		AND verdict.decision = 'defer' AND verdict.reason_code = 'awaiting_content'
		AND NOT EXISTS (
			SELECT 1 FROM messages picked
			JOIN message_jobs picked_job ON picked_job.incoming_message_id = picked.id
			JOIN LATERAL (SELECT id, reason_code FROM intent_decisions WHERE job_id = picked_job.id ORDER BY id DESC LIMIT 1) picked_decision ON true
			JOIN intent_decision_snapshots snap ON snap.decision_id = picked_decision.id
			WHERE picked.conversation_id = current.conversation_id AND picked.bot_id = current.bot_id
				AND picked.sender_id = current.sender_id AND picked.direction = 'incoming'
				AND (picked.created_at, picked.id) > (previous.created_at, previous.id)
				AND (picked.created_at, picked.id) < (current.created_at, current.id)
				AND ((picked_decision.reason_code = 'awaited_content_received'
					AND snap.input->'AwaitedRequest'->>'MessageID' = previous.platform_message_id::text)
					OR (snap.input->'TopicChange'->>'Action' IN ('close', 'personal_exit')
						AND snap.input->'TopicChange'->>'ID' = previous_job.id::text))
				AND (picked_job.status = 'done' OR EXISTS (
					SELECT 1 FROM agent_outbound_messages o WHERE o.job_id = picked_job.id AND o.status IN ('sent', 'unknown')
				))
		)
	ORDER BY previous.created_at DESC, previous.id DESC LIMIT 1`, task.JobID, task.BotID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	if !rows.Next() {
		return nil, rows.Err()
	}
	var message domain.Message
	if err := rows.Scan(&message.MessageID, &message.SenderID, &message.Text, &message.CreatedAt); err != nil {
		return nil, err
	}
	return &message, nil
}

// ListInbound returns one row per stored incoming message, including messages
// whose jobs have not produced a decision.
func (r *DecisionsRepo) ListInbound(ctx context.Context, q contracts.InboundQuery) ([]contracts.InboundRecord, error) {
	rows, err := r.pool.Client.QueryContext(ctx, liveScope+`
		SELECT m.id, COALESCE(m.platform_message_id, 0), m.sender_id, m.conversation_id::text,
		       CASE WHEN NOT $6 OR v.allow_history_read THEN left(m.text_content,801) ELSE '' END, m.created_at,
		       CASE WHEN NOT $6 OR v.allow_history_read THEN jsonb_path_query_array(m.segments, '$[*].type') ELSE '[]'::jsonb END,
		       COALESCE(j.id, 0), COALESCE(j.traceparent, ''), COALESCE(j.status, ''),
		       COALESCE(j.last_error_code, ''), COALESCE(d.id, 0), COALESCE(d.decision, ''),
		       COALESCE(d.reason_code, ''), COALESCE(d.model, ''),
		       CASE WHEN $4::bigint > 0 THEN to_jsonb(ARRAY(
		           SELECT id FROM intent_decisions WHERE job_id=j.id ORDER BY id DESC
		       )) ELSE '[]'::jsonb END
		FROM messages m LEFT JOIN visible v ON v.id=m.conversation_id
		LEFT JOIN message_jobs j ON j.incoming_message_id=m.id
		LEFT JOIN LATERAL (
		    SELECT id, decision, reason_code, model FROM intent_decisions
		    WHERE job_id=j.id ORDER BY id DESC LIMIT 1
		) d ON true
		WHERE m.bot_id=$1 AND m.direction='incoming' AND (NOT $6 OR v.id IS NOT NULL)
		  AND ($2::uuid IS NULL OR m.conversation_id=$2::uuid)
		  AND ($3::bigint=0 OR m.id<$3)
		  AND ($4::bigint=0 OR m.id=$4)
		ORDER BY m.id DESC LIMIT $5
	`, q.BotID, nullableConversation(q.ConversationID), q.BeforeID, q.MessageID, q.Limit, q.AuditVisibleOnly)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]contracts.InboundRecord, 0, q.Limit)
	for rows.Next() {
		var item contracts.InboundRecord
		var types, decisionIDs []byte
		if err := rows.Scan(&item.MessageID, &item.PlatformMessageID, &item.SenderID,
			&item.ConversationID, &item.Text, &item.ReceivedAt, &types, &item.JobID,
			&item.TraceParent, &item.JobStatus, &item.JobError, &item.DecisionID,
			&item.Decision, &item.ReasonCode, &item.Model, &decisionIDs); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(types, &item.SegmentTypes); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(decisionIDs, &item.DecisionIDs); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func nullableConversation(id string) any {
	if id == "" {
		return nil
	}
	return id
}

// persistTopicChange records the immutable input version before generation.
// persistTopicChange 在生成前登记不可变的输入版本。
func persistTopicChange(ctx context.Context, client *ent.Client, conversation string, jobID int64, topic *domain.TopicChange) error {
	action := topic.Action
	opening := action == "opened" || action == "waiting"
	if action == "reopened" {
		action = "follow_up"
	}
	if _, err := client.ExecContext(ctx, `WITH origin AS (
 SELECT r.id FROM message_jobs j JOIN group_topic_routes r ON r.id=j.incoming_message_id AND r.conversation_id=$1
 WHERE j.id=$2 AND NOT EXISTS (SELECT 1 FROM group_topic_inputs i WHERE i.route_id=r.id)
 ), created AS (
 INSERT INTO group_topics (id,conversation_id,version,expires_at)
 SELECT $3,$1,1,$4 FROM origin WHERE $6 AND $4>clock_timestamp()
 ON CONFLICT(id) DO NOTHING RETURNING id,version
 ), advanced AS (
 UPDATE group_topics t SET version=t.version+1,expires_at=$4,closed=($5='close')
 FROM origin WHERE NOT $6 AND t.id=$3 AND t.conversation_id=$1
 RETURNING t.id,t.version
 ) INSERT INTO group_topic_inputs (id,conversation_id,topic_id,version,route_id,action)
 SELECT gen_random_uuid(),$1,changed.id,changed.version,origin.id,$5 FROM origin CROSS JOIN (
 SELECT * FROM created UNION ALL SELECT * FROM advanced
 ) changed`, conversation, jobID, topic.ID, topic.ExpiresAt, action, opening); err != nil {
		return err
	}
	if _, err := client.ExecContext(ctx, `UPDATE group_topics t SET closed=true FROM group_topic_inputs i JOIN message_jobs j ON j.incoming_message_id=i.route_id
 WHERE j.id=$2 AND t.conversation_id=$1 AND (t.id,t.version)=(i.topic_id,i.version) AND $3='close'`, conversation, jobID, action); err != nil {
		return err
	}
	_, err := client.ExecContext(ctx, `INSERT INTO group_topic_route_candidates (id,conversation_id,route_id,topic_id,version)
 SELECT gen_random_uuid(),r.conversation_id,r.id,t.id,t.version
 FROM group_topic_inputs i JOIN message_jobs j ON j.incoming_message_id=i.route_id
 JOIN group_topics t ON (t.id,t.conversation_id,t.version)=(i.topic_id,i.conversation_id,i.version)
 JOIN group_topic_routes r ON r.conversation_id=t.conversation_id AND r.id>i.route_id
 WHERE j.id=$2 AND t.conversation_id=$1 AND NOT t.closed AND r.status IN ('pending','routing','expired','unavailable')
 ON CONFLICT (route_id,topic_id) DO NOTHING`, conversation, jobID)
	return err
}
