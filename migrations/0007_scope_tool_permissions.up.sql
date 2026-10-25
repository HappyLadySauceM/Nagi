-- Add explicit per-scope tool permission overrides. Only overrides are stored,
-- so changing the configured default baseline never rewrites existing rows.
-- 增加作用域级的工具权限显式覆盖。只存覆盖项，因此调整配置默认基线时
-- 不会重写已存行。

ALTER TABLE scope_policies
  ADD COLUMN tool_permissions JSONB NOT NULL DEFAULT '{}'::jsonb;
