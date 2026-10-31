-- modify "agent_outbound_messages" table
ALTER TABLE "agent_outbound_messages" ADD COLUMN "target_kind" character varying NOT NULL DEFAULT '', ADD COLUMN "target_id" bigint NOT NULL DEFAULT 0, ADD COLUMN "action" character varying NOT NULL DEFAULT '', ADD COLUMN "payload" jsonb NOT NULL DEFAULT '{}'::jsonb, ADD COLUMN "charge_chars" bigint NOT NULL DEFAULT 0;

-- Existing text deliveries retain their source target and budget.
-- 既有文本投递保留其来源目标与预算。
UPDATE agent_outbound_messages o SET target_kind=c.kind,target_id=c.target_id,
 action='send_'||c.kind||'_msg',charge_chars=char_length(o.text_content),
 payload=jsonb_build_object('message',o.text_content,'segments',jsonb_build_array(jsonb_build_object('type','text','data',jsonb_build_object('text',o.text_content))))
 FROM conversations c WHERE c.id=o.conversation_id;

INSERT INTO schema_migrations(version,name) VALUES (20261031091021,'public_api_delivery');
