-- modify "memories" table
ALTER TABLE "memories" ADD COLUMN "fact_key" text NULL;
-- create index "memories_fact_key_live" to table: "memories"
CREATE UNIQUE INDEX "memories_fact_key_live" ON "memories" ("bot_id", "visibility", "owner_key", "fact_key") WHERE ((fact_key IS NOT NULL) AND (status = ANY (ARRAY['candidate'::text, 'active'::text])));
-- create "memory_fact_sources" table
CREATE TABLE "memory_fact_sources" (
  "id" uuid NOT NULL,
  "authorization_event_id" bigint NOT NULL,
  "expires_at" timestamptz NULL,
  "memory_id" uuid NOT NULL,
  "message_id" bigint NOT NULL,
  PRIMARY KEY ("id"),
  CONSTRAINT "memory_fact_sources_memories_fact_sources" FOREIGN KEY ("memory_id") REFERENCES "memories" ("memory_id") ON UPDATE NO ACTION ON DELETE CASCADE,
  CONSTRAINT "memory_fact_sources_messages_memory_fact_sources" FOREIGN KEY ("message_id") REFERENCES "messages" ("id") ON UPDATE NO ACTION ON DELETE CASCADE
);
-- create index "memory_fact_sources_memory_message_key" to table: "memory_fact_sources"
CREATE UNIQUE INDEX "memory_fact_sources_memory_message_key" ON "memory_fact_sources" ("memory_id", "message_id");
-- create index "memory_fact_sources_message" to table: "memory_fact_sources"
CREATE INDEX "memory_fact_sources_message" ON "memory_fact_sources" ("message_id");

INSERT INTO schema_migrations(version,name) VALUES (20261124044902,'memory_fact_sources');
