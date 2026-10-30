ALTER TABLE scope_policies
 ADD COLUMN previous_reply_permission JSONB,
 ADD COLUMN max_messages_per_run INTEGER NOT NULL DEFAULT 3 CHECK (max_messages_per_run > 0),
 ADD COLUMN max_chars_per_message INTEGER NOT NULL DEFAULT 500 CHECK (max_chars_per_message > 0),
 ADD COLUMN max_total_chars_per_run INTEGER NOT NULL DEFAULT 1500 CHECK (max_total_chars_per_run >= max_chars_per_message),
 ADD COLUMN message_interval_ms INTEGER NOT NULL DEFAULT 700 CHECK (message_interval_ms BETWEEN 0 AND 60000);
UPDATE scope_policies s SET
 max_messages_per_run = p.max_messages_per_run,
 max_chars_per_message = p.max_chars_per_message,
 max_total_chars_per_run = p.max_total_chars_per_run,
 message_interval_ms = p.min_interval_ms
 FROM agent_sending_policies p WHERE s.bot_id=p.bot_id AND s.scope_type=p.scope_type AND s.scope_id=p.scope_id;
UPDATE scope_policies SET previous_reply_permission = tool_permissions->'qq.reply.current';
UPDATE scope_policies s SET tool_permissions = jsonb_set(s.tool_permissions, '{qq.reply.current}', 'false'::jsonb)
 WHERE NOT EXISTS (
 SELECT 1 FROM agent_sending_policies p WHERE p.bot_id=s.bot_id AND p.scope_type=s.scope_type AND p.scope_id=s.scope_id AND p.allow_send_current);
