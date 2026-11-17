ALTER TABLE "intent_prompt_versions"
  DROP COLUMN "deleted_at";

DELETE FROM schema_migrations WHERE version=20261117110000;
