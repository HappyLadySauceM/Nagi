-- Remove the per-scope tool permission overrides; policies fall back to the
-- configured default baseline.
-- 移除作用域级的工具权限覆盖；策略回到配置的默认基线。

ALTER TABLE scope_policies
  DROP COLUMN tool_permissions;
