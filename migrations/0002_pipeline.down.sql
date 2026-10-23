-- Reverse of 0002_pipeline.up.sql.
-- 0002_pipeline.up.sql 的回滚脚本。

DROP TABLE IF EXISTS memories;
DROP TABLE IF EXISTS intent_decisions;
DROP TABLE IF EXISTS active_threads;
DROP TABLE IF EXISTS message_jobs;
DROP TABLE IF EXISTS messages;
DROP TABLE IF EXISTS conversations;
