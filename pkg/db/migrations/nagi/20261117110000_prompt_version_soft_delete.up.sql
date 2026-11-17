-- add soft-delete marker for prompt versions; rows stay queryable by id for historical snapshots.
ALTER TABLE "intent_prompt_versions"
  ADD COLUMN "deleted_at" timestamptz NULL;

INSERT INTO schema_migrations(version,name) VALUES (20261117110000,'prompt_version_soft_delete');
