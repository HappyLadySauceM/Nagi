-- Drop dead pipeline structures left from the v1 design: the unused thread
-- tracker and context-compression columns that were never written, plus the
-- bot account status column whose only writer had no reader.
-- 删除 v1 设计遗留的死结构：从未使用的线程追踪表、从未写入的上下文压缩列，
-- 以及只有写入方没有读取方的账号状态列。

DROP TABLE IF EXISTS active_threads;

ALTER TABLE conversations
    DROP COLUMN summary,
    DROP COLUMN summary_upto_message_id;

ALTER TABLE bot_accounts
    DROP COLUMN status;
