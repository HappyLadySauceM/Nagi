-- modify "ai_audit_artifacts" table
ALTER TABLE "ai_audit_artifacts" ADD COLUMN "bucket" text NOT NULL DEFAULT '', ADD COLUMN "cleanup_attempts" bigint NOT NULL DEFAULT 0, ADD COLUMN "next_attempt_at" timestamptz NULL, ADD COLUMN "lease_token" text NOT NULL DEFAULT '', ADD COLUMN "lease_expires_at" timestamptz NULL;
-- create index "aiauditartifact_status_next_attempt_at" to table: "ai_audit_artifacts"
CREATE INDEX "aiauditartifact_status_next_attempt_at" ON "ai_audit_artifacts" ("status", "next_attempt_at");
-- create index "aiauditartifact_sha256_status_expires_at_created_at" to table: "ai_audit_artifacts"
CREATE INDEX "aiauditartifact_sha256_status_expires_at_created_at" ON "ai_audit_artifacts" ("sha256", "status", "expires_at", "created_at");

INSERT INTO schema_migrations(version,name) VALUES (20261104132309,'audit_media_archive');
