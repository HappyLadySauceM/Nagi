-- create "ai_audit_artifacts" table
CREATE TABLE "ai_audit_artifacts" (
  "id" uuid NOT NULL,
  "run_id" uuid NOT NULL,
  "event_id" uuid NULL,
  "kind" text NOT NULL,
  "status" text NOT NULL DEFAULT 'pending',
  "sha256" text NOT NULL DEFAULT '',
  "content_type" text NOT NULL DEFAULT '',
  "size_bytes" bigint NOT NULL DEFAULT 0,
  "object_key" text NOT NULL DEFAULT '',
  "failure_code" text NOT NULL DEFAULT '',
  "expires_at" timestamptz NULL,
  "created_at" timestamptz NOT NULL DEFAULT now(),
  "updated_at" timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY ("id")
);
-- create "ai_audit_events" table
CREATE TABLE "ai_audit_events" (
  "id" uuid NOT NULL,
  "run_id" uuid NOT NULL,
  "operation_id" text NOT NULL,
  "sequence" bigint NOT NULL,
  "schema_version" bigint NOT NULL DEFAULT 1,
  "stage" text NOT NULL,
  "status" text NOT NULL DEFAULT 'started',
  "error_code" text NOT NULL DEFAULT '',
  "input" jsonb NULL,
  "output" jsonb NULL,
  "started_at" timestamptz NOT NULL DEFAULT now(),
  "finished_at" timestamptz NULL,
  PRIMARY KEY ("id")
);
-- create "ai_audit_runs" table
CREATE TABLE "ai_audit_runs" (
  "id" uuid NOT NULL,
  "job_id" bigint NOT NULL,
  "operation_id" text NOT NULL,
  "bot_id" bigint NOT NULL,
  "conversation_id" uuid NOT NULL,
  "trace_id" text NOT NULL DEFAULT '',
  "config_version" bigint NOT NULL DEFAULT 0,
  "status" text NOT NULL DEFAULT 'running',
  "error_code" text NOT NULL DEFAULT '',
  "started_at" timestamptz NOT NULL DEFAULT now(),
  "finished_at" timestamptz NULL,
  PRIMARY KEY ("id")
);
-- create index "ai_audit_runs_operation_id_key" to table: "ai_audit_runs"
CREATE UNIQUE INDEX "ai_audit_runs_operation_id_key" ON "ai_audit_runs" ("operation_id");
-- create index "aiauditrun_bot_id_conversation_id_started_at" to table: "ai_audit_runs"
CREATE INDEX "aiauditrun_bot_id_conversation_id_started_at" ON "ai_audit_runs" ("bot_id", "conversation_id", "started_at");
-- create index "aiauditevent_run_id_sequence" to table: "ai_audit_events"
CREATE UNIQUE INDEX "aiauditevent_run_id_sequence" ON "ai_audit_events" ("run_id", "sequence");
-- create index "aiauditevent_run_id_operation_id" to table: "ai_audit_events"
CREATE UNIQUE INDEX "aiauditevent_run_id_operation_id" ON "ai_audit_events" ("run_id", "operation_id");
-- create index "aiauditartifact_run_id_created_at" to table: "ai_audit_artifacts"
CREATE INDEX "aiauditartifact_run_id_created_at" ON "ai_audit_artifacts" ("run_id", "created_at");

INSERT INTO schema_migrations(version,name) VALUES (20261104071100,'ai_audit');
