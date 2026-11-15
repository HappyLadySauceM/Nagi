-- reverse: create index "memory_replacements_pair" to table: "memory_replacements"
DROP INDEX "memory_replacements_pair";
-- reverse: create index "memory_replacements_old" to table: "memory_replacements"
DROP INDEX "memory_replacements_old";
-- reverse: create index "memory_replacements_idempotency" to table: "memory_replacements"
DROP INDEX "memory_replacements_idempotency";
-- reverse: create "memory_replacements" table
DROP TABLE "memory_replacements";

DELETE FROM schema_migrations WHERE version=20261115201828;
