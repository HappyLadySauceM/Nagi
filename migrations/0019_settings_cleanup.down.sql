-- Only the column shape is restored: the removed enable list has no equivalent
-- in the old model, so payload data cannot be brought back.
-- 仅恢复列结构：已移除的启用列表在旧模型中没有对应语义，payload 数据无法还原。

ALTER TABLE agent_sending_policies
 ADD COLUMN IF NOT EXISTS allow_send_current BOOLEAN NOT NULL DEFAULT false;
