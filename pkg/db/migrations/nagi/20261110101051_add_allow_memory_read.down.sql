-- reverse: modify "scope_policies" table
ALTER TABLE "scope_policies" DROP COLUMN "allow_memory_read";

DELETE FROM schema_migrations WHERE version=20261110101051;
