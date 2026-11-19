-- create "inbound_attachments" table
CREATE TABLE "inbound_attachments" (
  "id" uuid NOT NULL,
  "message_id" bigint NOT NULL,
  "segment_index" bigint NOT NULL,
  "kind" character varying NOT NULL,
  "source" jsonb NULL,
  "status" character varying NOT NULL DEFAULT 'pending',
  "failure_code" character varying NOT NULL DEFAULT '',
  "attempts" bigint NOT NULL DEFAULT 0,
  "filename" character varying NOT NULL DEFAULT '',
  "content_type" character varying NOT NULL DEFAULT '',
  "size_bytes" bigint NOT NULL DEFAULT 0,
  "sha256" character varying NOT NULL DEFAULT '',
  "bucket" character varying NOT NULL DEFAULT '',
  "object_key" character varying NOT NULL DEFAULT '',
  "lease_token" uuid NULL,
  "lease_expires_at" timestamptz NULL,
  "next_run_at" timestamptz NOT NULL DEFAULT now(),
  "expires_at" timestamptz NOT NULL,
  PRIMARY KEY ("id")
);
-- create index "inboundattachment_expires_at_id" to table: "inbound_attachments"
CREATE INDEX "inboundattachment_expires_at_id" ON "inbound_attachments" ("expires_at", "id");
-- create index "inboundattachment_message_id_segment_index" to table: "inbound_attachments"
CREATE UNIQUE INDEX "inboundattachment_message_id_segment_index" ON "inbound_attachments" ("message_id", "segment_index");
-- create index "inboundattachment_status_next_run_at_id" to table: "inbound_attachments"
CREATE INDEX "inboundattachment_status_next_run_at_id" ON "inbound_attachments" ("status", "next_run_at", "id");

INSERT INTO schema_migrations(version,name) VALUES (20261119134524,'inbound_attachments');
