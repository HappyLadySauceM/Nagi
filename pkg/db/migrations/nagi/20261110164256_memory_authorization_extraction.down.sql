-- reverse: create index "scope_memory_auth_events_scope" to table: "scope_memory_authorization_events"
DROP INDEX "scope_memory_auth_events_scope";
-- reverse: create "scope_memory_authorization_events" table
DROP TABLE "scope_memory_authorization_events";
-- reverse: create index "memory_extraction_jobs_message_key" to table: "memory_extraction_jobs"
DROP INDEX "memory_extraction_jobs_message_key";
-- reverse: create index "memory_extraction_jobs_due" to table: "memory_extraction_jobs"
DROP INDEX "memory_extraction_jobs_due";
-- reverse: create index "memory_extraction_jobs_conversation" to table: "memory_extraction_jobs"
DROP INDEX "memory_extraction_jobs_conversation";
-- reverse: create "memory_extraction_jobs" table
DROP TABLE "memory_extraction_jobs";
-- reverse: create index "messages_memory_authorization_event" to table: "messages"
DROP INDEX IF EXISTS "messages_memory_authorization_event";
-- reverse: modify "messages" table
ALTER TABLE "messages" DROP COLUMN "memory_authorization_event_id";
-- reverse: create index "memory_extraction_cursors_business_key" to table: "memory_extraction_cursors"
DROP INDEX "memory_extraction_cursors_business_key";
-- reverse: create "memory_extraction_cursors" table
DROP TABLE "memory_extraction_cursors";
-- reverse: modify "scope_policies" table
ALTER TABLE "scope_policies" DROP COLUMN "memory_authorization_event_id";
-- reverse: create index "memories_dedupe_key" to table: "memories"
DROP INDEX "memories_dedupe_key";
-- reverse: modify "memories" table
ALTER TABLE "memories" DROP CONSTRAINT "memories_subject_type_check", DROP CONSTRAINT "memories_origin_check", DROP COLUMN "review_reason", DROP COLUMN "dedupe_key", DROP COLUMN "subject_id", DROP COLUMN "subject_type", DROP COLUMN "origin", DROP COLUMN "source_sender_id";

DELETE FROM schema_migrations WHERE version=20261110164256;
