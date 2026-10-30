-- Revert to the previous defaults. Stored data is reset to the old baseline
-- because the up migration overwrote it; per-policy custom values are not
-- recoverable by design.
-- 回退到旧默认。up 迁移已覆盖存量值，逐条自定义值无法恢复，故按旧基线重置。

ALTER TABLE scope_policies
  ADD COLUMN IF NOT EXISTS retention_days INTEGER NOT NULL DEFAULT 30;

UPDATE scope_policies
SET store_messages = FALSE,
    max_messages_per_run = 3,
    max_chars_per_message = 500,
    max_total_chars_per_run = 1500,
    max_replies_per_hour = 20,
    retention_days = 30;

ALTER TABLE scope_policies
  ALTER COLUMN store_messages SET DEFAULT FALSE,
  ALTER COLUMN max_replies_per_hour SET DEFAULT 20,
  ALTER COLUMN max_messages_per_run SET DEFAULT 3,
  ALTER COLUMN max_chars_per_message SET DEFAULT 500,
  ALTER COLUMN max_total_chars_per_run SET DEFAULT 1500;

ALTER TABLE scope_policies
  ADD CONSTRAINT scope_policies_max_messages_per_run_check CHECK (max_messages_per_run > 0),
  ADD CONSTRAINT scope_policies_max_chars_per_message_check CHECK (max_chars_per_message > 0),
  ADD CONSTRAINT scope_policies_max_total_chars_per_run_check CHECK (max_total_chars_per_run >= max_chars_per_message);

DROP INDEX IF EXISTS messages_created_at;

UPDATE system_settings
SET payload = jsonb_set(payload, '{Tools,PermissionDefaults,qq.reply.current}', 'false'::jsonb, true)
WHERE payload -> 'Tools' IS NOT NULL;

UPDATE system_settings
SET payload = jsonb_set(payload, '{Agent,Sending}', '{"MaxMessagesPerRun":3,"MaxCharsPerMessage":500,"MaxTotalCharsPerRun":1500}'::jsonb, true)
WHERE payload -> 'Agent' IS NOT NULL;

UPDATE system_settings
SET payload = payload #- '{Messages}'
WHERE payload #> '{Messages}' IS NOT NULL;
