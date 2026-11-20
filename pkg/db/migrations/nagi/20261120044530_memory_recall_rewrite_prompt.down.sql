-- If rewrite versions exist, the original CHECK rejects rollback and the transaction preserves those versions.
-- 若已有改写版本，旧 CHECK 将拒绝回滚，事务会保留这些版本。
-- reverse: modify "intent_prompt_versions" table
ALTER TABLE "intent_prompt_versions" DROP CONSTRAINT "intent_prompt_versions_kind_check", ADD CONSTRAINT "intent_prompt_versions_kind_check" CHECK (prompt_kind = ANY (ARRAY['chat_reply'::text, 'intent_participation'::text, 'memory_extraction'::text]));

DELETE FROM schema_migrations WHERE version=20261120044530;
