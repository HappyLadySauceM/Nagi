-- Rolling back removes B-only control tasks and their ledger rows.
-- 回退会移除仅属于 B 批的控制任务及其级联台账。
DELETE FROM message_jobs WHERE source_kind = 'memory_consent_prompt';
-- reverse: create index "message_jobs_consent_proposal_unique" to table: "message_jobs"
DROP INDEX "message_jobs_consent_proposal_unique";
-- reverse: modify "message_jobs" table
ALTER TABLE "message_jobs" DROP CONSTRAINT message_jobs_bound_incoming, DROP CONSTRAINT message_jobs_incoming_direction_check, DROP COLUMN incoming_direction, DROP CONSTRAINT "message_jobs_source_check", DROP CONSTRAINT "message_jobs_memory_consent_proposals_delivery_job", DROP COLUMN "consent_proposal_id", DROP COLUMN "source_kind", ALTER COLUMN "incoming_message_id" SET NOT NULL;
-- reverse: create index "memory_consent_grant_target" to table: "memory_consent_grants"
DROP INDEX "memory_consent_grant_target";
-- reverse: create index "memory_consent_grant_current" to table: "memory_consent_grants"
DROP INDEX "memory_consent_grant_current";
-- reverse: create "memory_consent_grants" table
DROP TABLE "memory_consent_grants";
-- reverse: create index "memory_consent_event_processed" to table: "memory_consent_events"
DROP INDEX "memory_consent_event_processed";
-- reverse: create "memory_consent_events" table
DROP TABLE "memory_consent_events";
-- reverse: create index "memory_consent_proposal_idempotency" to table: "memory_consent_proposals"
DROP INDEX "memory_consent_proposal_idempotency";
-- reverse: create index "memory_consent_proposal_fact" to table: "memory_consent_proposals"
DROP INDEX "memory_consent_proposal_fact";
-- reverse: create "memory_consent_proposals" table
DROP TABLE "memory_consent_proposals";
DROP INDEX messages_bound_direction;
-- reverse: modify "memories" table
ALTER TABLE "memories" DROP COLUMN "authorization_version";

DELETE FROM messages WHERE control_kind <> '';
UPDATE agent_outbound_messages SET text_content='', payload='{}'::jsonb WHERE payload @> '{"cross_group_fact":true}'::jsonb;
ALTER TABLE messages DROP COLUMN control_kind;

DELETE FROM schema_migrations WHERE version=20261122050000;
