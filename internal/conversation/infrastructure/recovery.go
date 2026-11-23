package infrastructure

const recoverExpiredJobsSQL = `WITH expired AS MATERIALIZED (
 SELECT j.id, j.claim_token, j.source_kind,
 EXISTS (
  SELECT 1 FROM ai_audit_runs r WHERE r.job_id=j.id AND r.bot_id=m.bot_id AND r.conversation_id=m.conversation_id
   AND r.operation_id='job:'||j.id::text||':claim:'||j.claim_token::text
   AND (r.error_code=$4 OR EXISTS (SELECT 1 FROM ai_audit_events e
    WHERE e.run_id=r.id AND e.stage IN ('worker.process','intent.decision','agent.generation')
     AND e.status='failed' AND e.error_code=$4)
    OR EXISTS (SELECT 1 FROM intent_decisions d JOIN intent_decision_snapshots s ON s.decision_id=d.id
     WHERE d.job_id=j.id AND d.decision='silent' AND d.reason_code=$4
      AND s.input->>'WorkerAuditRunID'=r.id::text))
 ) AS audit_start_failed,
 COALESCE(live.recovery_decision IS NULL AND EXISTS (
  SELECT 1 FROM intent_run_events e WHERE e.run_id=live.id AND (
   (live.state='running' AND e.kind='semantic_started') OR
   (live.state='interrupted' AND e.kind='end' AND e.data->>'ReasonCode'='semantic_result_unknown'))),false) AS semantic_unknown,
 COALESCE(live.state='failed' AND EXISTS (SELECT 1 FROM intent_run_events e WHERE e.run_id=live.id
  AND e.kind='end' AND e.data->>'ReasonCode'='decision_persistence_failed'),false) AS persistence_failed,
 COALESCE((live.state='completed' AND live.decision_id IS NOT NULL) OR
  (live.state='running' AND live.recovery_decision IS NOT NULL),false) AS decision_resume,
 j.source_kind='incoming' AND j.consent_proposal_id IS NULL
  AND EXISTS (SELECT 1 FROM agent_outbound_messages o WHERE o.job_id=j.id AND o.claim_token=j.claim_token AND o.status='prepared' AND o.resume_digest IS NOT NULL) AS pure_delivery,
 EXISTS (SELECT 1 FROM agent_outbound_messages o WHERE o.job_id=j.id AND o.status IN ('sent','unknown','sending','failed')) AS partial,
 EXISTS (SELECT 1 FROM agent_run_budgets b WHERE b.job_id=j.id
  AND b.reserved_messages=(SELECT count(*) FROM agent_outbound_messages o WHERE o.job_id=j.id AND o.status<>'cancelled')
  AND b.reserved_chars=(SELECT COALESCE(sum(o.charge_chars),0) FROM agent_outbound_messages o WHERE o.job_id=j.id AND o.status<>'cancelled')) AS balanced
 FROM message_jobs j LEFT JOIN messages m ON m.id=j.incoming_message_id
 LEFT JOIN LATERAL (SELECT r.* FROM intent_decision_runs r WHERE r.job_id=j.id AND r.bot_id=m.bot_id AND r.conversation_id=m.conversation_id
  AND (r.claim_token=j.claim_token OR (j.last_error_code='decision_resume' AND
   (r.state='completed' OR (r.state='running' AND r.recovery_decision IS NOT NULL))))
  ORDER BY (r.claim_token=j.claim_token) DESC,r.started_at DESC,r.id DESC LIMIT 1) live ON j.source_kind='incoming'
 WHERE j.id IN (SELECT value::bigint FROM jsonb_array_elements_text($1::jsonb))
  AND j.status='processing' AND NOT j.delivery_continuation AND j.lease_expires_at<=clock_timestamp()
), candidates AS MATERIALIZED (
 SELECT o.id,o.job_id,o.charge_chars,o.status='prepared' AS held,
 e.pure_delivery AND (o.payload ? 'cross_group_fact' OR o.payload ? 'consent_read_digest') AS evidence_missing,
 e.pure_delivery AND o.resume_digest IS NOT NULL AS resumable,
 e.source_kind='memory_consent_prompt' OR (o.payload @> '{"cross_group_fact":true}'::jsonb AND o.payload ? 'consent_read_digest') AS may_reread,
 e.balanced AND o.platform_message_id IS NULL AND NOT EXISTS(SELECT 1 FROM messages h WHERE h.outbound_id=o.id) AS refundable
 FROM expired e JOIN agent_outbound_messages o ON o.job_id=e.id AND o.claim_token=e.claim_token
 WHERE o.status='prepared' OR (e.pure_delivery AND o.status='cancelled' AND o.error_code IN ('lease_recovered','lease_expired_prepared_refunded')
  AND (o.payload ? 'cross_group_fact' OR o.payload ? 'consent_read_digest')
  AND o.platform_message_id IS NULL AND NOT EXISTS(SELECT 1 FROM messages h WHERE h.outbound_id=o.id))
), cancelled AS (
 UPDATE agent_outbound_messages o SET status='cancelled',updated_at=clock_timestamp(),
 error_code=CASE WHEN c.evidence_missing AND (NOT c.held OR c.refundable) THEN 'consent_resume_evidence_missing'
  WHEN NOT c.refundable THEN 'lease_expired'
  WHEN c.resumable THEN 'lease_recovered'
  WHEN c.may_reread THEN 'lease_expired_prepared_refunded'
  ELSE 'lease_recovered' END,
 resume_result=CASE WHEN c.evidence_missing AND (NOT c.held OR c.refundable) THEN jsonb_build_object(
  'tool_call_id',o.tool_call_id,'sequence',o.sequence,'status','cancelled','platform_message_id',NULL,'error_code','consent_resume_evidence_missing') ELSE o.resume_result END
 FROM candidates c WHERE o.id=c.id
 RETURNING o.job_id,o.charge_chars,c.held,c.refundable,o.error_code,c.resumable
), refund AS (
 SELECT job_id,count(*) AS messages,sum(charge_chars) AS chars FROM cancelled WHERE held AND refundable GROUP BY job_id
), budgets AS (
 UPDATE agent_run_budgets b SET reserved_messages=b.reserved_messages-r.messages,reserved_chars=b.reserved_chars-r.chars,updated_at=clock_timestamp()
 FROM refund r WHERE b.job_id=r.job_id
), settled AS (
 UPDATE agent_outbound_messages o SET status=CASE WHEN o.platform_message_id IS NULL THEN 'unknown' ELSE 'sent' END,
 error_code=CASE WHEN o.platform_message_id IS NULL THEN 'lease_expired' END,updated_at=clock_timestamp()
 FROM expired e WHERE o.job_id=e.id AND o.status='sending'
 RETURNING o.*
), history AS (
 INSERT INTO messages(conversation_id,bot_id,direction,platform_message_id,sender_id,text_content,segments,sent_status,outbound_id,created_at,control_kind)
 SELECT c.id,o.bot_id,'outgoing',o.platform_message_id,o.bot_id,o.text_content,COALESCE(o.payload->'segments','[]'::jsonb),o.status,o.id,o.updated_at,
 CASE WHEN e.source_kind='memory_consent_prompt' THEN e.source_kind WHEN o.payload @> '{"cross_group_fact":true}'::jsonb THEN 'memory_consent_reply' ELSE '' END
 FROM settled o JOIN expired e ON e.id=o.job_id JOIN conversations c ON c.id=o.conversation_id AND c.kind=o.target_kind AND c.target_id=o.target_id
 JOIN LATERAL (SELECT enabled,store_messages FROM scope_policies WHERE bot_id=o.bot_id AND scope_type=c.kind AND scope_id IN (c.target_id,0) ORDER BY scope_id DESC LIMIT 1) p ON p.enabled AND p.store_messages
 ON CONFLICT(outbound_id) WHERE outbound_id IS NOT NULL DO NOTHING
), outcomes AS (
 SELECT e.*, (SELECT count(*) FROM cancelled c WHERE c.job_id=e.id AND c.error_code='consent_resume_evidence_missing') AS missing,
 EXISTS (SELECT 1 FROM cancelled c WHERE c.job_id=e.id AND c.resumable AND c.error_code='lease_recovered') AS resume,
 EXISTS (SELECT 1 FROM cancelled c WHERE c.job_id=e.id AND c.error_code='lease_expired') AS incomplete
 FROM expired e
) UPDATE message_jobs j SET
 delivery_continuation=o.pure_delivery AND NOT o.audit_start_failed,
 delivery_outcome=CASE WHEN o.missing>0 THEN 'blocked' ELSE j.delivery_outcome END,
 delivery_reason=CASE WHEN o.missing>0 THEN 'consent_resume_evidence_missing' ELSE j.delivery_reason END,
 denied_count=j.denied_count+o.missing,
 status=CASE WHEN o.audit_start_failed THEN 'failed' WHEN o.resume THEN 'pending'
  WHEN o.pure_delivery OR o.partial OR o.incomplete OR j.delivery_outcome='blocked' OR o.semantic_unknown OR o.persistence_failed THEN 'failed'
  WHEN o.decision_resume THEN 'pending' WHEN j.attempts+1>=$2 THEN 'failed' ELSE 'pending' END,
 last_error_code=CASE WHEN o.audit_start_failed THEN $4 WHEN o.missing>0 THEN 'consent_resume_evidence_missing'
  WHEN o.partial THEN 'partial' WHEN o.semantic_unknown THEN 'semantic_result_unknown'
  WHEN o.persistence_failed THEN 'decision_persistence_failed' WHEN o.decision_resume AND NOT o.pure_delivery AND NOT o.incomplete AND j.delivery_outcome<>'blocked' THEN 'decision_resume'
  ELSE 'lease_expired' END,
 attempts=j.attempts+1,next_run_at=clock_timestamp()+make_interval(secs=>$3),claim_token=NULL,lease_expires_at=NULL,updated_at=clock_timestamp()
 FROM outcomes o WHERE j.id=o.id AND j.claim_token=o.claim_token`
