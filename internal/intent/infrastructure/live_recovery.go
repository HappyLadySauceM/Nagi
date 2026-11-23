package infrastructure

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/HappyLadySauceM/Nagi/internal/intent/domain"
)

func (r *DecisionsRepo) SaveDecisionRecovery(ctx context.Context, task domain.Task, decision domain.FinalDecision) error {
	payload, err := json.Marshal(decision)
	if err != nil {
		return err
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	bot, err := lockRunningDecision(ctx, tx, task.JobID, task.ClaimToken, decision.RunID)
	if err != nil {
		return err
	}
	if bot != task.BotID {
		return errors.New("intent: recovery decision scope mismatch")
	}
	if err := checkDecisionLease(ctx, tx, task.JobID, task.ClaimToken); err != nil {
		return err
	}
	result, err := tx.Exec(ctx, `UPDATE intent_decision_runs SET recovery_decision=$2::jsonb
	 WHERE id=$1 AND conversation_id=$3 AND (recovery_decision IS NULL OR recovery_decision=$2::jsonb)
	 AND EXISTS(SELECT 1 FROM intent_run_events e WHERE e.run_id=$1 AND e.sequence=5 AND e.kind='semantic')`, decision.RunID, payload, task.ConversationID)
	if err != nil {
		return err
	}
	if result.RowsAffected() != 1 {
		return errors.New("intent: conflicting recovery decision")
	}
	if err := tx.Commit(ctx); err != nil {
		probe, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
		defer cancel()
		var committed bool
		if r.pool.QueryRow(probe, `SELECT EXISTS(SELECT 1 FROM intent_decision_runs WHERE id=$1 AND job_id=$2
		 AND claim_token=$3 AND recovery_decision=$4::jsonb AND bot_id=$5 AND conversation_id=$6)`,
			decision.RunID, task.JobID, task.ClaimToken, payload, task.BotID, task.ConversationID).Scan(&committed) != nil || !committed {
			return err
		}
	}
	return nil
}

func (r *DecisionsRepo) LoadDecisionRecovery(ctx context.Context, task domain.Task) (domain.FinalDecision, bool, error) {
	var decision domain.FinalDecision
	var run, state string
	var id int64
	var pending []byte
	err := r.pool.QueryRow(ctx, `SELECT COALESCE(r.id::text,''),COALESCE(r.state,''),COALESCE(r.decision_id,0),r.recovery_decision
	 FROM message_jobs j LEFT JOIN LATERAL (
	  SELECT id,state,decision_id,recovery_decision FROM intent_decision_runs r WHERE r.job_id=j.id
	   AND r.bot_id=$3 AND r.conversation_id=$4 AND (r.state='completed' OR (r.state='running' AND r.recovery_decision IS NOT NULL))
	  ORDER BY r.started_at DESC,r.id DESC LIMIT 1
	 ) r ON true
	 WHERE j.id=$1 AND j.source_kind='incoming' AND NOT j.delivery_continuation AND j.status='processing'
	 AND j.claim_token=$2 AND j.lease_expires_at>clock_timestamp() AND j.last_error_code='decision_resume'`, task.JobID, task.ClaimToken, task.BotID, task.ConversationID).Scan(&run, &state, &id, &pending)
	if errors.Is(err, sql.ErrNoRows) {
		return decision, false, nil
	}
	if err != nil {
		return decision, false, err
	}
	if run == "" {
		return decision, true, errors.New("intent: recovery decision missing")
	}
	if state == "running" {
		if err := json.Unmarshal(pending, &decision); err != nil {
			return decision, true, err
		}
		if decision.RunID != run || decision.Snapshot == nil {
			return decision, true, errors.New("intent: invalid recovery decision")
		}
		for i := range decision.Steps {
			detail := decision.Steps[i].Detail
			if value, ok := detail["RuleEvents"]; ok {
				raw, err := json.Marshal(value)
				if err != nil {
					return decision, true, err
				}
				var events []domain.CooldownEvent
				if err := json.Unmarshal(raw, &events); err != nil {
					return decision, true, err
				}
				detail["RuleEvents"] = events
			}
		}
		if err := r.RecordDecisionRun(ctx, task.JobID, decision, task.ClaimToken); err != nil {
			return decision, true, err
		}
		return decision, true, nil
	}
	record, err := r.Get(ctx, id)
	if err != nil {
		return decision, true, err
	}
	snapshot, err := r.snapshotByDecision(ctx, id)
	if err != nil {
		return decision, true, err
	}
	if record.JobID != task.JobID || record.BotID != task.BotID || record.ConversationID != task.ConversationID || snapshot.RunID != run {
		return decision, true, errors.New("intent: recovery decision scope mismatch")
	}
	return domain.FinalDecision{RunID: run, Decision: domain.Decision(record.Decision), ReasonCode: record.ReasonCode,
		Topic: snapshot.Input.TopicChange, Snapshot: &snapshot}, true, nil
}

func (r *DecisionsRepo) BeginDecisionReply(ctx context.Context, task domain.Task, run string) (bool, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer tx.Rollback(ctx)
	var job int64
	err = tx.QueryRow(ctx, `SELECT id FROM message_jobs WHERE id=$1 AND claim_token=$2 AND source_kind='incoming'
	 AND NOT delivery_continuation AND status='processing' AND lease_expires_at>clock_timestamp() FOR UPDATE`, task.JobID, task.ClaimToken).Scan(&job)
	if err != nil {
		return false, err
	}
	result, err := tx.Exec(ctx, `UPDATE intent_decision_runs r SET reply_started=true FROM message_jobs j
	 WHERE r.id=$1 AND j.id=$2 AND r.job_id=j.id AND j.claim_token=$3 AND j.lease_expires_at>clock_timestamp()
	 AND r.bot_id=$4 AND r.conversation_id=$5 AND r.state='completed' AND NOT r.reply_started
	 AND (r.claim_token=j.claim_token OR j.last_error_code='decision_resume')`, run, job, task.ClaimToken, task.BotID, task.ConversationID)
	if err != nil || result.RowsAffected() != 1 {
		return false, err
	}
	if err := tx.Commit(ctx); err != nil {
		return false, err
	}
	return true, nil
}
