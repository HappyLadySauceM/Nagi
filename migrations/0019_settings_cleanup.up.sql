-- Autonomous sending is decided by the per-scope capability grant now, so the
-- legacy column and the stored provider enable list are dead weight.
-- 自主发送改由会话的能力授权决定，旧的开关列与已存储的提供方启用列表因此不再使用。

ALTER TABLE agent_sending_policies DROP COLUMN IF EXISTS allow_send_current;

UPDATE system_settings
SET payload = payload #- '{Tools,Enabled}'
WHERE payload #> '{Tools,Enabled}' IS NOT NULL;
