-- Constrain the outbound delivery outcome to the three modeled states so the
-- unknown-outcome path (timed sends with unconfirmed delivery) is explicit.
-- NULL remains allowed for incoming messages.
-- 将出站投递结果约束为三种建模状态，使“结果未知”路径（已发出但未确认）显式化。
-- 入站消息的 NULL 仍被允许。
ALTER TABLE messages
    ADD CONSTRAINT messages_sent_status_check
    CHECK (sent_status IS NULL OR sent_status IN ('sent','failed','unknown'));
