-- Persist the W3C trace context of the inbound message with its decision job so
-- the worker can continue the same trace after the queue hop.
-- 把入站消息的 W3C 追踪上下文与决策任务一起持久化，
-- 使工作器在跨队列后仍能延续同一条 trace。

ALTER TABLE message_jobs
  ADD COLUMN traceparent TEXT NOT NULL DEFAULT '',
  ADD COLUMN tracestate TEXT NOT NULL DEFAULT '';
