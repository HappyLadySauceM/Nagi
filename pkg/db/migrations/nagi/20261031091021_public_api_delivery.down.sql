-- Target metadata and rich payloads are lost on rollback; retain text_content
-- and source job records. Export the added columns before manual rollback if needed.
-- 回滚会丢失目标元数据与富消息载荷，保留文本与来源任务；需要恢复时先导出新增列。
-- reverse: modify "agent_outbound_messages" table
ALTER TABLE "agent_outbound_messages" DROP COLUMN "charge_chars", DROP COLUMN "payload", DROP COLUMN "action", DROP COLUMN "target_id", DROP COLUMN "target_kind";

DELETE FROM schema_migrations WHERE version=20261031091021;
