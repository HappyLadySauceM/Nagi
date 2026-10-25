-- Restore the tool whitelist column with its original default.
-- 恢复工具白名单列及其原始默认值。
ALTER TABLE scope_policies
    ADD COLUMN tool_policy JSONB NOT NULL DEFAULT '[]'::jsonb;
