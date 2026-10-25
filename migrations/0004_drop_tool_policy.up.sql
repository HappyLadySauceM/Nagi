-- Remove the unenforced tool whitelist. It never had an execution path: tools
-- are registered in code, so a free-text allow list only misled administrators.
-- 删除未生效的工具白名单。它从未接入执行链路：工具在代码中注册，
-- 自由文本白名单只会误导管理员。
ALTER TABLE scope_policies DROP COLUMN tool_policy;
