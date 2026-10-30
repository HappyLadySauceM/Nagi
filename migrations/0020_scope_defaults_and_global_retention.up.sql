-- Message storage is a product baseline now: policies keep the flag for
-- defensive checks but the UI no longer exposes it, so existing rows are turned
-- on. Sending budgets also switched to a permissive default where zero means
-- unlimited, therefore the old positive-only CHECKs have to go.
-- 消息存储现在是产品基线：策略保留该列用于防御性校验，但界面不再暴露，
-- 因此存量行统一开启。发送预算同时改为宽松默认（0 表示不限），
-- 旧的“必须为正数”约束必须移除。

ALTER TABLE scope_policies DROP CONSTRAINT IF EXISTS scope_policies_max_messages_per_run_check;
ALTER TABLE scope_policies DROP CONSTRAINT IF EXISTS scope_policies_max_chars_per_message_check;
ALTER TABLE scope_policies DROP CONSTRAINT IF EXISTS scope_policies_max_total_chars_per_run_check;
-- 0015 declared the cross-column check inside ADD COLUMN, so PostgreSQL named it
-- generically instead of after the column.
-- 0015 把跨列约束写在 ADD COLUMN 中，PostgreSQL 因此使用了通用名称。
ALTER TABLE scope_policies DROP CONSTRAINT IF EXISTS scope_policies_check;

ALTER TABLE scope_policies
  ALTER COLUMN store_messages SET DEFAULT TRUE,
  ALTER COLUMN max_replies_per_hour SET DEFAULT 0,
  ALTER COLUMN max_messages_per_run SET DEFAULT 0,
  ALTER COLUMN max_chars_per_message SET DEFAULT 4500,
  ALTER COLUMN max_total_chars_per_run SET DEFAULT 0;

-- Reset every stored policy to the new defaults: storage on, per-run and hourly
-- limits unlimited, single message capped at the QQ text limit. Explicit reply
-- tool denials are removed so the permission follows the new default grant.
-- 将全部存量策略重置为新默认：存储开启、每轮与每小时不限、单条取 QQ 文本上限；
-- 同时删除回复工具的显式拒绝，使其跟随新的默认授予。
UPDATE scope_policies
SET store_messages = TRUE,
    max_messages_per_run = 0,
    max_chars_per_message = 4500,
    max_total_chars_per_run = 0,
    max_replies_per_hour = 0,
    tool_permissions = tool_permissions - 'qq.reply.current';

-- Retention is a process-wide setting now; the per-scope column is dead weight.
-- 留存改为进程级配置，会话级列不再使用。
ALTER TABLE scope_policies DROP COLUMN IF EXISTS retention_days;

-- Global retention cleanup scans by created_at across all conversations.
-- 全局留存按 created_at 跨会话清理。
CREATE INDEX IF NOT EXISTS messages_created_at ON messages (created_at);

-- The reply tool is granted by default; the old baseline denied it, so the
-- stored payload is reset to the new default.
-- 回复工具默认授予；旧基线为拒绝，因此把已存 payload 重置为新默认。
UPDATE system_settings
SET payload = jsonb_set(payload, '{Tools,PermissionDefaults,qq.reply.current}', 'true'::jsonb, true)
WHERE payload -> 'Tools' IS NOT NULL;

-- Old sending defaults are replaced by the permissive baseline; the console no
-- longer edits these values, so a stale payload must not resurrect 3/500/1500.
-- 旧的发送默认值替换为宽松基线；控制台不再编辑这些值，
-- 陈旧 payload 不能把 3/500/1500 复活。
UPDATE system_settings
SET payload = jsonb_set(payload, '{Agent,Sending}', '{"MaxMessagesPerRun":0,"MaxCharsPerMessage":4500,"MaxTotalCharsPerRun":0}'::jsonb, true)
WHERE payload -> 'Agent' IS NOT NULL;

UPDATE system_settings
SET payload = jsonb_set(payload, '{Messages}', '{"RetentionDays":30}'::jsonb, true)
WHERE payload #> '{Messages}' IS NULL;
