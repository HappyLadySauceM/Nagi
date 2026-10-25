-- Revert the persisted trace context columns.
-- 撤销持久化的追踪上下文字段。

ALTER TABLE message_jobs
  DROP COLUMN tracestate,
  DROP COLUMN traceparent;
