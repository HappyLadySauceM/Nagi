-- reverse: create index "ai_audit_runs_operation_id_key" to table: "ai_audit_runs"
DROP INDEX IF EXISTS "aiauditartifact_run_id_created_at";
DROP INDEX IF EXISTS "ai_audit_artifacts_run_id_created_at";
DROP INDEX IF EXISTS "aiauditevent_run_id_operation_id";
DROP INDEX IF EXISTS "ai_audit_events_run_id_operation_id_key";
DROP INDEX IF EXISTS "aiauditevent_run_id_sequence";
DROP INDEX IF EXISTS "ai_audit_events_run_id_sequence_key";
DROP INDEX IF EXISTS "aiauditrun_bot_id_conversation_id_started_at";
DROP INDEX IF EXISTS "ai_audit_runs_bot_id_conversation_id_started_at";
DROP INDEX "ai_audit_runs_operation_id_key";
-- reverse: create "ai_audit_runs" table
DROP TABLE "ai_audit_runs";
-- reverse: create "ai_audit_events" table
DROP TABLE "ai_audit_events";
-- reverse: create "ai_audit_artifacts" table
DROP TABLE "ai_audit_artifacts";

DELETE FROM schema_migrations WHERE version=20261104071100;
