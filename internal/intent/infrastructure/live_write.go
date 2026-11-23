package infrastructure

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/HappyLadySauceM/Nagi/internal/intent/contracts"
	"github.com/HappyLadySauceM/Nagi/internal/intent/domain"
	"github.com/google/uuid"
)

type liveWriter interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}

func scanLive(ctx context.Context, q liveWriter, query string, args []any, dest ...any) error {
	rows, err := q.QueryContext(ctx, query, args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	if !rows.Next() {
		if err := rows.Err(); err != nil {
			return err
		}
		return sql.ErrNoRows
	}
	return rows.Scan(dest...)
}

// StartDecisionRun binds an observation to the job's current durable claim.
// StartDecisionRun 将观察记录绑定到任务的当前持久领取。
func (r *DecisionsRepo) StartDecisionRun(ctx context.Context, task domain.Task) (string, bool, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return "", false, err
	}
	defer tx.Rollback(ctx)
	var bot int64
	var conversation string
	err = tx.QueryRow(ctx, `SELECT m.bot_id,m.conversation_id FROM message_jobs j JOIN messages m ON m.id=j.incoming_message_id
	 WHERE j.id=$1 AND j.status='processing' AND j.claim_token=$2::uuid AND j.lease_expires_at>clock_timestamp() FOR UPDATE OF j`, task.JobID, task.ClaimToken).Scan(&bot, &conversation)
	if err != nil {
		return "", false, err
	}
	if bot != task.BotID || conversation != task.ConversationID {
		return "", false, errors.New("intent: run scope mismatch")
	}
	var id string
	var state string
	err = tx.QueryRow(ctx, `SELECT id,state FROM intent_decision_runs WHERE job_id=$1 AND claim_token=$2::uuid`, task.JobID, task.ClaimToken).Scan(&id, &state)
	if err == nil {
		if state == "completed" {
			return id, false, nil
		}
		return id, false, fmt.Errorf("intent: claim already recorded in state %s", state)
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return "", false, err
	}
	id = uuid.NewString()
	if _, err := tx.Exec(ctx, `INSERT INTO intent_decision_runs(id,bot_id,conversation_id,job_id,claim_token,state,started_at)
	 VALUES($1,$2,$3,$4,$5,'running',clock_timestamp())`, id, bot, conversation, task.JobID, task.ClaimToken); err != nil {
		return "", false, err
	}
	if err := appendLiveEvent(ctx, tx, bot, id, task.JobID, task.ClaimToken, 1, "start", contracts.LiveDecisionFacts{State: "running"}); err != nil {
		return "", false, err
	}
	if err := tx.Commit(ctx); err != nil {
		probe, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
		defer cancel()
		var exists bool
		if r.pool.QueryRow(probe, `SELECT EXISTS(SELECT 1 FROM intent_decision_runs WHERE id=$1 AND job_id=$2 AND claim_token=$3 AND state='running')`, id, task.JobID, task.ClaimToken).Scan(&exists) != nil || !exists {
			return "", false, err
		}
	}
	return id, true, nil
}

func appendLiveEvent(ctx context.Context, tx liveWriter, bot int64, run string, job int64, claim string, sequence int, kind string, facts contracts.LiveDecisionFacts) error {
	data, err := json.Marshal(facts)
	if err != nil {
		return err
	}
	var existingKind string
	var matches bool
	err = scanLive(ctx, tx, `SELECT kind,data=$3::jsonb FROM intent_run_events WHERE run_id=$1 AND sequence=$2`, []any{run, sequence, data}, &existingKind, &matches)
	if err == nil {
		if existingKind != kind || !matches {
			return errors.New("intent: conflicting run event")
		}
		return nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	var previous int
	if err := scanLive(ctx, tx, `SELECT COALESCE(MAX(sequence),0) FROM intent_run_events WHERE run_id=$1`, []any{run}, &previous); err != nil {
		return err
	}
	if sequence <= previous {
		return errors.New("intent: out-of-order run event")
	}
	if err := lockDecisionHead(ctx, tx, bot); err != nil {
		return err
	}
	if job != 0 {
		if err := checkDecisionLease(ctx, tx, job, claim); err != nil {
			return err
		}
	}
	var position int64
	if err := scanLive(ctx, tx, `UPDATE intent_stream_heads SET position=position+1 WHERE bot_id=$1 RETURNING position`, []any{bot}, &position); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO intent_run_events(run_id,bot_id,position,sequence,kind,data,created_at) VALUES($1,$2,$3,$4,$5,$6::jsonb,clock_timestamp())`, run, bot, position, sequence, kind, data); err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `SELECT pg_notify('intent_live',$1::text)`, fmt.Sprint(bot))
	return err
}

func lockDecisionHead(ctx context.Context, tx liveWriter, bot int64) error {
	if _, err := tx.ExecContext(ctx, `INSERT INTO intent_stream_heads(bot_id,position) VALUES($1,0) ON CONFLICT(bot_id) DO NOTHING`, bot); err != nil {
		return err
	}
	var position int64
	return scanLive(ctx, tx, `SELECT position FROM intent_stream_heads WHERE bot_id=$1 FOR UPDATE`, []any{bot}, &position)
}

// checkDecisionLease takes a new database timestamp after any lock wait.
// checkDecisionLease 在等待锁之后用新语句取得数据库时间。
func checkDecisionLease(ctx context.Context, tx liveWriter, job int64, claim string) error {
	var id int64
	return scanLive(ctx, tx, `SELECT id FROM message_jobs WHERE id=$1 AND claim_token=$2::uuid AND status='processing' AND lease_expires_at>clock_timestamp()`, []any{job, claim}, &id)
}

func lockRunningDecision(ctx context.Context, tx liveWriter, job int64, claim, run string) (int64, error) {
	var lockedJob int64
	if err := scanLive(ctx, tx, `SELECT id FROM message_jobs WHERE id=$1 AND claim_token=$2::uuid AND status='processing' AND lease_expires_at>clock_timestamp() FOR UPDATE`, []any{job, claim}, &lockedJob); err != nil {
		return 0, err
	}
	var bot int64
	err := scanLive(ctx, tx, `SELECT bot_id FROM intent_decision_runs WHERE id=$1 AND job_id=$2 AND claim_token=$3::uuid AND state='running' FOR UPDATE`, []any{run, job, claim}, &bot)
	return bot, err
}

func (r *DecisionsRepo) AppendDecisionStep(ctx context.Context, task domain.Task, run string, sequence int, kind string, facts contracts.LiveDecisionFacts) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	bot, err := lockRunningDecision(ctx, tx, task.JobID, task.ClaimToken, run)
	if err != nil {
		return err
	}
	if err := appendLiveEvent(ctx, tx, bot, run, task.JobID, task.ClaimToken, sequence, kind, facts); err != nil {
		return err
	}
	return r.confirmDecisionStep(ctx, task, run, sequence, kind, facts, tx.Commit(ctx))
}

func (r *DecisionsRepo) confirmDecisionStep(ctx context.Context, task domain.Task, run string, sequence int, kind string, facts contracts.LiveDecisionFacts, commitErr error) error {
	if commitErr == nil {
		return nil
	}
	probe, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
	defer cancel()
	var exists bool
	expected, _ := json.Marshal(facts)
	if r.pool.QueryRow(probe, `SELECT EXISTS(SELECT 1 FROM intent_run_events e JOIN intent_decision_runs r ON r.id=e.run_id
	 WHERE e.run_id=$1 AND e.sequence=$2 AND e.kind=$3 AND e.data=$4::jsonb AND r.job_id=$5 AND r.claim_token=$6
	 AND r.bot_id=$7 AND r.conversation_id=$8)`, run, sequence, kind, expected, task.JobID, task.ClaimToken, task.BotID, task.ConversationID).Scan(&exists) == nil && exists {
		return nil
	}
	return commitErr
}

func finishLiveDecision(ctx context.Context, tx liveWriter, bot int64, run string, job int64, claim string, sequence int, facts contracts.LiveDecisionFacts) error {
	if err := lockDecisionHead(ctx, tx, bot); err != nil {
		return err
	}
	if job != 0 {
		if err := checkDecisionLease(ctx, tx, job, claim); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx, `UPDATE intent_decision_runs SET state=$2,decision_id=NULLIF($3,0),finished_at=clock_timestamp(),recovery_decision=NULL WHERE id=$1 AND state='running'`, run, facts.State, facts.DecisionID); err != nil {
		return err
	}
	return appendLiveEvent(ctx, tx, bot, run, job, claim, sequence, "end", facts)
}

func (r *DecisionsRepo) FailDecisionRun(ctx context.Context, task domain.Task, run, state, reason string) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	bot, err := lockRunningDecision(ctx, tx, task.JobID, task.ClaimToken, run)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if err := finishLiveDecision(ctx, tx, bot, run, task.JobID, task.ClaimToken, 8, contracts.LiveDecisionFacts{State: state, ReasonCode: reason}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// InterruptExpiredDecisionRuns closes observations whose processing claim is gone.
// InterruptExpiredDecisionRuns 收尾已失去任务领取的观察记录。
func (r *DecisionsRepo) InterruptExpiredDecisionRuns(ctx context.Context) error {
	for range 64 {
		changed, err := r.interruptOneRun(ctx)
		if err != nil || !changed {
			return err
		}
	}
	return nil
}

func (r *DecisionsRepo) interruptOneRun(ctx context.Context) (bool, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer tx.Rollback(ctx)
	var id string
	var bot int64
	err = tx.QueryRow(ctx, `SELECT r.id,r.bot_id FROM intent_decision_runs r JOIN message_jobs j ON j.id=r.job_id
	 WHERE r.state='running' AND (j.status<>'processing' OR j.claim_token IS DISTINCT FROM r.claim_token OR j.lease_expires_at<=clock_timestamp())
	 AND NOT (r.recovery_decision IS NOT NULL AND (
	  (j.status='processing' AND (j.claim_token=r.claim_token OR COALESCE(j.last_error_code,'')='decision_resume'))
	  OR (j.status='pending' AND COALESCE(j.last_error_code,'')='decision_resume')))
	 ORDER BY r.started_at,r.id LIMIT 1 FOR UPDATE OF j SKIP LOCKED`).Scan(&id, &bot)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	var state string
	if err := tx.QueryRow(ctx, `SELECT state FROM intent_decision_runs WHERE id=$1 FOR UPDATE`, id).Scan(&state); err != nil {
		return false, err
	}
	if state == "running" {
		var unknown bool
		if err := tx.QueryRow(ctx, `SELECT r.recovery_decision IS NULL AND EXISTS (SELECT 1 FROM intent_run_events e
		 WHERE e.run_id=r.id AND e.kind='semantic_started') FROM intent_decision_runs r WHERE r.id=$1`, id).Scan(&unknown); err != nil {
			return false, err
		}
		reason := "claim_lost"
		if unknown {
			reason = "semantic_result_unknown"
		}
		if err := finishLiveDecision(ctx, tx, bot, id, 0, "", 8, contracts.LiveDecisionFacts{State: "interrupted", ReasonCode: reason}); err != nil {
			return false, err
		}
	}
	return true, tx.Commit(ctx)
}
