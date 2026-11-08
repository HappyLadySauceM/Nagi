-- reverse: create index "aiauditartifact_sha256_status_expires_at_created_at" to table: "ai_audit_artifacts"
DROP INDEX "aiauditartifact_sha256_status_expires_at_created_at";
-- reverse: create index "aiauditartifact_status_next_attempt_at" to table: "ai_audit_artifacts"
DROP INDEX "aiauditartifact_status_next_attempt_at";
-- reverse: modify "ai_audit_artifacts" table
ALTER TABLE "ai_audit_artifacts" DROP COLUMN "lease_expires_at", DROP COLUMN "lease_token", DROP COLUMN "next_attempt_at", DROP COLUMN "cleanup_attempts", DROP COLUMN "bucket";

DELETE FROM schema_migrations WHERE version=20261104132309;
