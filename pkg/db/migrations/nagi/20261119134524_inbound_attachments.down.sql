-- reverse: create index "inboundattachment_next_run_at_id" to table: "inbound_attachments"
DROP INDEX "inboundattachment_next_run_at_id";
-- reverse: create index "inboundattachment_message_id_segment_index" to table: "inbound_attachments"
DROP INDEX "inboundattachment_message_id_segment_index";
-- reverse: create index "inboundattachment_expires_at_id" to table: "inbound_attachments"
DROP INDEX "inboundattachment_expires_at_id";
-- reverse: create "inbound_attachments" table
DROP TABLE "inbound_attachments";

DELETE FROM schema_migrations WHERE version=20261119134524;
