package infrastructure

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/HappyLadySauceM/Nagi/internal/outbound/application"
	"github.com/HappyLadySauceM/Nagi/internal/outbound/domain"
	"github.com/HappyLadySauceM/Nagi/pkg/db"
)

const resumeDigestSQL = `encode(sha256(convert_to(jsonb_build_array(id,job_id,tool_call_id,sequence,bot_id,conversation_id,target_kind,target_id,action,text_content,payload,charge_chars,memory_dependencies,send_permit)::text,'UTF8')),'hex')`

const consentEvidenceMissing = "consent_resume_evidence_missing"

func handOffDelivery(ctx context.Context, tx *db.Tx, job int64, claim string) error {
	if err := closeUnprovenConsentSlots(ctx, tx, job, claim, false); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE agent_outbound_messages SET resume_digest=COALESCE(resume_digest,`+resumeDigestSQL+`) WHERE job_id=$1 AND status='prepared' AND claim_token=$2::uuid`, job, claim); err != nil {
		return err
	}
	moved, err := tx.Exec(ctx, `UPDATE message_jobs SET delivery_continuation=true,status='pending',claim_token=NULL,lease_expires_at=NULL,next_run_at=clock_timestamp(),
 delivery_reason=CASE WHEN delivery_reason=$4 THEN delivery_reason ELSE $3 END,updated_at=clock_timestamp()
 WHERE id=$1 AND source_kind='incoming' AND consent_proposal_id IS NULL AND status='processing' AND claim_token=$2::uuid AND lease_expires_at>clock_timestamp()`, job, claim, domain.GatePending, consentEvidenceMissing)
	if err != nil {
		return err
	}
	if moved.RowsAffected() != 1 {
		return ErrLeaseLost
	}
	return nil
}

func closeUnprovenConsentSlots(ctx context.Context, tx *db.Tx, job int64, claim string, recovery bool) error {
	if _, err := tx.Exec(ctx, `SELECT id FROM agent_outbound_messages WHERE job_id=$1 AND status IN ('prepared','cancelled') ORDER BY id FOR UPDATE`, job); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `SELECT job_id FROM agent_run_budgets WHERE job_id=$1 FOR UPDATE`, job); err != nil {
		return err
	}
	var held, chars, refunded, refundedChars int
	err := tx.QueryRow(ctx, `WITH candidates AS MATERIALIZED (
 SELECT o.id,o.status='prepared' AS held,o.charge_chars
 FROM agent_outbound_messages o JOIN message_jobs j ON j.id=o.job_id
 WHERE j.id=$1 AND j.source_kind='incoming' AND j.consent_proposal_id IS NULL
 AND (($4 AND j.delivery_continuation AND ((j.status='pending' AND j.claim_token IS NULL AND j.next_run_at<=clock_timestamp())
 OR (j.status='processing' AND j.lease_expires_at<=clock_timestamp())))
 OR (NOT $4 AND j.status='processing' AND j.claim_token=NULLIF($2,'')::uuid AND o.claim_token=j.claim_token AND j.lease_expires_at>clock_timestamp()))
 AND (o.payload ? 'cross_group_fact' OR o.payload ? 'consent_read_digest') AND o.platform_message_id IS NULL
 AND NOT EXISTS (SELECT 1 FROM messages h WHERE h.outbound_id=o.id)
 AND (o.status='prepared' OR (o.status='cancelled' AND o.error_code IN ('lease_recovered','lease_expired_prepared_refunded')))
 ), closed AS (
 UPDATE agent_outbound_messages o SET status='cancelled',error_code=$3,updated_at=clock_timestamp(),
  resume_result=jsonb_build_object('tool_call_id',o.tool_call_id,'sequence',o.sequence,'status','cancelled','platform_message_id',NULL,'error_code',$3::text)
 FROM candidates c WHERE o.id=c.id RETURNING c.held,c.charge_chars
 ), tally AS (
 SELECT count(*) AS slots,count(*) FILTER (WHERE held) AS held,COALESCE(sum(charge_chars) FILTER (WHERE held),0) AS chars FROM closed
 ), refunded AS (
 UPDATE agent_run_budgets b SET reserved_messages=reserved_messages-t.held,reserved_chars=reserved_chars-t.chars,updated_at=clock_timestamp()
 FROM tally t WHERE b.job_id=$1 AND t.held>0 AND b.reserved_messages>=t.held AND b.reserved_chars>=t.chars RETURNING t.held,t.chars
 ), marked AS (
 UPDATE message_jobs j SET delivery_outcome='blocked',delivery_reason=$3,denied_count=denied_count+t.slots
 FROM tally t WHERE j.id=$1 AND t.slots>0
 ) SELECT t.held,t.chars,COALESCE(r.held,0),COALESCE(r.chars,0) FROM tally t LEFT JOIN refunded r ON true`, job, claim, consentEvidenceMissing, recovery).Scan(&held, &chars, &refunded, &refundedChars)
	if err != nil {
		return err
	}
	if held != refunded || chars != refundedChars {
		return domain.ErrInvalidSettlement
	}
	return nil
}

// ResumeNext claims and executes one original slot with a separate delivery lease.
// ResumeNext 用独立的投递租约领取并执行一个原始槽位。
func (r *Repo) ResumeNext(ctx context.Context, send func(context.Context, domain.Record) (int64, error), ready ...func(domain.Record) bool) (bool, error) {
	record, err := r.claimContinuation(ctx)
	if record.ID == "" || err != nil {
		return false, err
	}
	if record.Status == domain.StatusSending {
		outcome := application.Outcome{Status: domain.StatusUnknown, ErrorCode: "continuation_lease_expired"}
		if record.PlatformMessageID != 0 {
			outcome = application.Outcome{Status: domain.StatusSent, PlatformMessageID: record.PlatformMessageID}
		}
		return true, r.Settle(ctx, record.ID, outcome, record.ClaimToken)
	}
	if len(ready) > 0 && !ready[0](record) {
		return true, r.cancelOfflineContinuation(ctx, record.ID, record.ClaimToken, record.Status)
	}
	_, err = application.Deliver(ctx, r, record.ID, func(ctx context.Context) (int64, error) { return send(ctx, record) }, record.ClaimToken)
	var gate *domain.GateError
	if errors.As(err, &gate) {
		return true, nil
	}
	if err != nil {
		// A recorded terminal result is authoritative even when the transport returned an error.
		// 即使传输返回错误，已记入台账的终态依然有效。
		var terminal bool
		if queryErr := r.pool.QueryRow(ctx, `SELECT resume_result IS NOT NULL FROM agent_outbound_messages WHERE id=$1`, record.ID).Scan(&terminal); queryErr == nil && terminal {
			return true, nil
		}
	}
	return true, err
}

// CancelUnwrittenContinuation cancels after the gate when transport confirms no write began.
// CancelUnwrittenContinuation 在过闸后由传输确认尚未写入时取消续办。
func (r *Repo) CancelUnwrittenContinuation(ctx context.Context, id, claim string) error {
	return r.cancelOfflineContinuation(ctx, id, claim, domain.StatusSending)
}

func (r *Repo) cancelOfflineContinuation(ctx context.Context, id, claim string, status domain.Status) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if err := lockScopedDelivery(ctx, tx, id, claim); err != nil {
		return err
	}
	var job int64
	if err := tx.QueryRow(ctx, `SELECT o.job_id FROM agent_outbound_messages o JOIN message_jobs j ON j.id=o.job_id
 WHERE o.id=$1 AND j.delivery_continuation FOR UPDATE OF o`, id).Scan(&job); err != nil {
		return err
	}
	switch status {
	case domain.StatusCancelled:
		err = cancelRefunded(ctx, tx, id, claim, domain.GateOffline)
	case domain.StatusPrepared, domain.StatusSending:
		err = cancelUnsent(ctx, tx, id, claim, domain.GateOffline, status)
	default:
		return domain.ErrInvalidSettlement
	}
	if err != nil {
		return err
	}
	if err := saveContinuationResult(ctx, tx, id); err != nil {
		return err
	}
	if err := finishContinuation(ctx, tx, job, claim); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (r *Repo) claimContinuation(ctx context.Context) (domain.Record, error) {
	var record domain.Record
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return record, err
	}
	defer tx.Rollback(ctx)
	var conversation string
	err = tx.QueryRow(ctx, `SELECT c.id FROM conversations c WHERE EXISTS (
 SELECT 1 FROM message_jobs j JOIN messages m ON m.id=j.incoming_message_id
 WHERE m.conversation_id=c.id AND j.source_kind='incoming' AND j.consent_proposal_id IS NULL AND j.delivery_continuation AND NOT EXISTS (SELECT 1 FROM message_jobs busy LEFT JOIN messages bm ON bm.id=busy.incoming_message_id LEFT JOIN memory_consent_proposals bp ON bp.id=busy.consent_proposal_id WHERE COALESCE(bm.conversation_id,bp.source_conversation_id)=c.id AND busy.id<>j.id AND busy.status='processing') AND ((j.status='pending' AND j.next_run_at<=clock_timestamp()) OR (j.status='processing' AND j.lease_expires_at<=clock_timestamp()))
 ) ORDER BY c.id LIMIT 1 FOR UPDATE OF c SKIP LOCKED`).Scan(&conversation)
	if errors.Is(err, sql.ErrNoRows) {
		return record, nil
	}
	if err != nil {
		return record, err
	}
	var job int64
	var claim string
	err = tx.QueryRow(ctx, `SELECT j.id FROM message_jobs j JOIN messages m ON m.id=j.incoming_message_id
 WHERE m.conversation_id=$1 AND j.source_kind='incoming' AND j.consent_proposal_id IS NULL AND j.delivery_continuation
 AND ((j.status='pending' AND j.next_run_at<=clock_timestamp()) OR (j.status='processing' AND j.lease_expires_at<=clock_timestamp()))
 AND NOT EXISTS (SELECT 1 FROM message_jobs busy LEFT JOIN messages bm ON bm.id=busy.incoming_message_id
  LEFT JOIN memory_consent_proposals bp ON bp.id=busy.consent_proposal_id
  WHERE COALESCE(bm.conversation_id,bp.source_conversation_id)=$1 AND busy.id<>j.id AND busy.status='processing')
 ORDER BY j.id LIMIT 1 FOR UPDATE OF j`, conversation).Scan(&job)
	if errors.Is(err, sql.ErrNoRows) {
		return record, nil
	}
	if err != nil {
		return record, err
	}
	if err := closeUnprovenConsentSlots(ctx, tx, job, "", true); err != nil {
		return record, err
	}
	if err := tx.QueryRow(ctx, `UPDATE message_jobs SET status='processing',claim_token=gen_random_uuid(),lease_expires_at=clock_timestamp()+interval '45 seconds',updated_at=clock_timestamp() WHERE id=$1 RETURNING claim_token`, job).Scan(&claim); err != nil {
		return record, err
	}
	err = tx.QueryRow(ctx, `WITH picked AS (
 SELECT id FROM agent_outbound_messages WHERE job_id=$1 AND (status='sending' OR ((status='prepared' OR (status='cancelled' AND error_code='lease_recovered')) AND resume_digest IS NOT NULL AND NOT (payload ?| ARRAY['cross_group_fact','consent_read_digest'])))
 ORDER BY (status='sending') DESC,sequence LIMIT 1 FOR UPDATE
 ) UPDATE agent_outbound_messages o SET claim_token=$2::uuid FROM picked WHERE picked.id=o.id
 RETURNING o.id,o.job_id,o.conversation_id,o.bot_id,o.sequence,COALESCE(o.tool_call_id,''),o.text_content,o.target_kind,o.target_id,o.action,o.payload::text,o.status,COALESCE(o.platform_message_id,0)`, job, claim).Scan(
		&record.ID, &record.JobID, &record.ConversationID, &record.BotID, &record.Sequence, &record.ToolCallID, &record.Text, &record.TargetKind, &record.TargetID, &record.Action, &record.Payload, &record.Status, &record.PlatformMessageID)
	if errors.Is(err, sql.ErrNoRows) {
		if err := finishContinuation(ctx, tx, job, claim); err != nil {
			return domain.Record{}, err
		}
		return domain.Record{}, tx.Commit(ctx)
	}
	if err != nil {
		return domain.Record{}, err
	}
	record.ClaimToken = claim
	return record, tx.Commit(ctx)
}

func saveContinuationResult(ctx context.Context, tx *db.Tx, id string) error {
	_, err := tx.Exec(ctx, `UPDATE agent_outbound_messages SET resume_result=jsonb_build_object('tool_call_id',tool_call_id,'sequence',sequence,'status',status,'platform_message_id',platform_message_id,'error_code',error_code)
 WHERE id=$1 AND resume_result IS NULL AND status IN ('sent','failed','unknown','cancelled')`, id)
	return err
}

func finishContinuation(ctx context.Context, tx *db.Tx, job int64, claim string) error {
	updated, err := tx.Exec(ctx, `WITH state AS (
 SELECT COALESCE(bool_or(status='sending' OR ((status='prepared' OR (status='cancelled' AND error_code='lease_recovered')) AND resume_digest IS NOT NULL AND NOT (payload ?| ARRAY['cross_group_fact','consent_read_digest']))),false) AS more,
 COALESCE(bool_or(status IN ('failed','unknown') OR
  (status='cancelled' AND (resume_result IS NOT NULL OR error_code='lease_expired'))),false) AS failed
 FROM agent_outbound_messages WHERE job_id=$1
 ) UPDATE message_jobs j SET status=CASE WHEN state.more THEN 'pending' WHEN state.failed THEN 'failed' ELSE 'done' END,
 last_error_code=CASE WHEN state.failed THEN COALESCE(NULLIF(j.delivery_reason,''), j.last_error_code, 'partial') ELSE NULL END,
 delivery_reason=CASE WHEN NOT state.more AND NOT state.failed THEN '' ELSE j.delivery_reason END,
 next_run_at=clock_timestamp(),claim_token=NULL,lease_expires_at=NULL,updated_at=clock_timestamp()
 FROM state WHERE j.id=$1 AND j.delivery_continuation AND j.status='processing' AND j.claim_token=$2::uuid`, job, claim)
	if err != nil {
		return err
	}
	if updated.RowsAffected() != 1 {
		return ErrLeaseLost
	}
	return nil
}

func postponeContinuation(ctx context.Context, tx *db.Tx, job int64, claim string, delay time.Duration, reason string) error {
	updated, err := tx.Exec(ctx, `UPDATE message_jobs SET status='pending',claim_token=NULL,lease_expires_at=NULL,next_run_at=clock_timestamp()+make_interval(secs=>$3),
 delivery_reason=CASE WHEN delivery_reason=$5 THEN delivery_reason ELSE $4 END,updated_at=clock_timestamp()
 WHERE id=$1 AND delivery_continuation AND status='processing' AND claim_token=$2::uuid AND lease_expires_at>clock_timestamp()`, job, claim, delay.Seconds(), reason, consentEvidenceMissing)
	if err != nil {
		return err
	}
	if updated.RowsAffected() != 1 {
		return ErrLeaseLost
	}
	return nil
}
