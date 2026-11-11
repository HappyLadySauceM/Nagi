-- bootstrap memory authorization events for policies that existed before this feature
WITH inserted AS (
  INSERT INTO scope_memory_authorization_events
    (bot_id, scope_type, scope_id, event_kind, authorized, policy_version, created_at)
  SELECT bot_id, scope_type, scope_id, 'bootstrap',
         (enabled AND store_messages AND allow_memory_write), version, clock_timestamp()
  FROM scope_policies
  RETURNING id, bot_id, scope_type, scope_id
)
UPDATE scope_policies p
SET memory_authorization_event_id = inserted.id
FROM inserted
WHERE p.bot_id = inserted.bot_id
  AND p.scope_type = inserted.scope_type
  AND p.scope_id = inserted.scope_id;

-- establish a per-conversation scan baseline; existing messages are never backfilled
INSERT INTO memory_extraction_cursors (bot_id, conversation_id, last_message_id, last_scanned_at)
SELECT c.bot_id, c.id, COALESCE(MAX(m.id), 0), clock_timestamp()
FROM conversations c
LEFT JOIN messages m ON m.conversation_id = c.id
GROUP BY c.bot_id, c.id
ON CONFLICT (bot_id, conversation_id) DO NOTHING;

INSERT INTO schema_migrations(version,name) VALUES (20261110170000,'bootstrap_memory_authorization');
