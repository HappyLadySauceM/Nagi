-- restore the old prompt/version uniqueness only when the layer key cannot collide.
DO $$
BEGIN
  IF EXISTS (
    SELECT 1
    FROM "intent_prompt_versions"
    GROUP BY "prompt_id", "version"
    HAVING count(*) > 1
  ) THEN
    RAISE EXCEPTION 'cannot rollback prompt_unified_management: duplicate prompt_id/version rows exist';
  END IF;
END $$;

DROP INDEX "intent_prompt_versions_layer_version_key";
CREATE UNIQUE INDEX "intent_prompt_versions_prompt_id_version_key"
  ON "intent_prompt_versions" ("prompt_id", "version");

ALTER TABLE "intent_prompt_versions"
  DROP CONSTRAINT "intent_prompt_versions_kind_check",
  DROP CONSTRAINT "intent_prompt_versions_scope_type_check",
  DROP CONSTRAINT "intent_prompt_versions_scope_id_check",
  DROP COLUMN "archived_at",
  DROP COLUMN "scope_id",
  DROP COLUMN "scope_type",
  DROP COLUMN "bot_id",
  DROP COLUMN "prompt_kind";

DELETE FROM schema_migrations WHERE version=20261115180000;
