-- modify "scope_policies" table
ALTER TABLE "scope_policies" ADD COLUMN "allow_memory_read" boolean NOT NULL DEFAULT false;

INSERT INTO schema_migrations(version,name) VALUES (20261110101051,'add_allow_memory_read');
