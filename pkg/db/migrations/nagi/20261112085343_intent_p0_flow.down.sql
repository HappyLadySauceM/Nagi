-- reverse: create index "intent_decision_snapshots_decision_id_key" to table: "intent_decision_snapshots"
DROP INDEX "intent_decision_snapshots_decision_id_key";
-- reverse: create "intent_decision_snapshots" table
DROP TABLE "intent_decision_snapshots";
-- reverse: create index "intent_prompt_versions_prompt_status" to table: "intent_prompt_versions"
DROP INDEX "intent_prompt_versions_prompt_status";
-- reverse: create index "intent_prompt_versions_prompt_id_version_key" to table: "intent_prompt_versions"
DROP INDEX "intent_prompt_versions_prompt_id_version_key";
-- reverse: create "intent_prompt_versions" table
DROP TABLE "intent_prompt_versions";
-- reverse: modify "intent_decision_steps" table
ALTER TABLE "intent_decision_steps" DROP COLUMN "duration_ms", DROP COLUMN "evidence", DROP COLUMN "summary", DROP COLUMN "branch", DROP COLUMN "status", DROP COLUMN "node_version", DROP COLUMN "node_id";

DELETE FROM schema_migrations WHERE version=20261112085343;
