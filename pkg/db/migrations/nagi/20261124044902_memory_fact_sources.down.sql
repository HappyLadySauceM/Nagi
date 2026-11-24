-- reverse: create index "memory_fact_sources_message" to table: "memory_fact_sources"
DROP INDEX "memory_fact_sources_message";
-- reverse: create index "memory_fact_sources_memory_message_key" to table: "memory_fact_sources"
DROP INDEX "memory_fact_sources_memory_message_key";
-- reverse: create "memory_fact_sources" table
DROP TABLE "memory_fact_sources";
-- reverse: create index "memories_fact_key_live" to table: "memories"
DROP INDEX "memories_fact_key_live";
-- reverse: modify "memories" table
ALTER TABLE "memories" DROP COLUMN "fact_key";

DELETE FROM schema_migrations WHERE version=20261124044902;
