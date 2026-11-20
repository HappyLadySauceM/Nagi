-- modify "intent_prompt_versions" table
ALTER TABLE "intent_prompt_versions" DROP CONSTRAINT "intent_prompt_versions_kind_check", ADD CONSTRAINT "intent_prompt_versions_kind_check" CHECK (prompt_kind = ANY (ARRAY['chat_reply'::text, 'intent_participation'::text, 'memory_extraction'::text, 'memory_recall_rewrite'::text]));

INSERT INTO schema_migrations(version,name) VALUES (20261120044530,'memory_recall_rewrite_prompt');
