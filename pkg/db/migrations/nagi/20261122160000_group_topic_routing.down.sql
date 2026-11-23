-- Export or back up routing state before rolling back. Original inbound messages remain in place.
-- 回退前导出或备份归属记录，原始入站消息保留。
-- Do not reopen unsent slots closed by the upgrade's authorization check.
-- 升级时因许可证据缺失而关闭的未发槽在回退时保持终态。
ALTER TABLE agent_outbound_messages DROP COLUMN send_permit, DROP COLUMN resume_result, DROP COLUMN resume_digest, DROP COLUMN memory_dependencies;
DROP TABLE send_scope_selections;
ALTER TABLE system_settings DROP COLUMN send_permit_generations, DROP COLUMN send_permit_instance;
ALTER TABLE scope_policies DROP COLUMN send_permit_generations, DROP COLUMN send_permit_instance;
ALTER TABLE message_jobs DROP COLUMN delivery_continuation;
DROP TABLE group_topic_inputs;
DROP TABLE group_topic_route_candidates;
DROP TABLE group_topic_routes;
DROP TABLE group_topics;
DROP INDEX messages_topic_inbound_key;

DELETE FROM schema_migrations WHERE version=20261122160000;
