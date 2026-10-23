-- Remove the sent_status outcome constraint.
-- 移除 sent_status 结果约束。
ALTER TABLE messages
    DROP CONSTRAINT IF EXISTS messages_sent_status_check;
