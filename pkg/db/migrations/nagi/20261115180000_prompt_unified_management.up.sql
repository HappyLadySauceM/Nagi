-- add prompt kind and scope layer columns; scope_id is fixed to zero for editing layers.
ALTER TABLE "intent_prompt_versions"
  ADD COLUMN "prompt_kind" text NOT NULL DEFAULT 'intent_participation',
  ADD COLUMN "bot_id" bigint NOT NULL DEFAULT 0,
  ADD COLUMN "scope_type" text NOT NULL DEFAULT 'account',
  ADD COLUMN "scope_id" bigint NOT NULL DEFAULT 0,
  ADD COLUMN "archived_at" timestamptz NULL;

ALTER TABLE "intent_prompt_versions"
  ADD CONSTRAINT "intent_prompt_versions_kind_check" CHECK (prompt_kind = ANY (ARRAY['chat_reply'::text, 'intent_participation'::text, 'memory_extraction'::text])),
  ADD CONSTRAINT "intent_prompt_versions_scope_type_check" CHECK (scope_type = ANY (ARRAY['account'::text, 'group'::text, 'private'::text])),
  ADD CONSTRAINT "intent_prompt_versions_scope_id_check" CHECK (scope_id = 0);

DROP INDEX "intent_prompt_versions_prompt_id_version_key";
CREATE UNIQUE INDEX "intent_prompt_versions_layer_version_key"
  ON "intent_prompt_versions" ("prompt_id", "bot_id", "scope_type", "scope_id", "provider", "version");

INSERT INTO schema_migrations(version,name) VALUES (20261115180000,'prompt_unified_management');
