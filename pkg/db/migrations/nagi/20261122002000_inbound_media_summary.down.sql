-- 回滚仅移除媒体状态审计摘要，原始消息与附件记录保留。
ALTER TABLE "messages" DROP COLUMN "inbound_media_summary";

DELETE FROM schema_migrations WHERE version=20261122002000;
