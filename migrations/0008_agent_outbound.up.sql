-- Agent outbound delivery ledger: one row per message an agent run intends to
-- send, the per-run budget it draws from, and the link from the logical
-- outgoing message back to its delivery record.
-- Agent 出站投递台账：每次 Agent 运行打算发送的一条消息一行，另含该运行消耗的
-- 回合预算，并把逻辑出站消息关联回它的投递记录。

CREATE TABLE agent_outbound_messages (
  id UUID PRIMARY KEY,
  job_id BIGINT NOT NULL REFERENCES message_jobs(id) ON DELETE CASCADE,
  conversation_id UUID NOT NULL REFERENCES conversations(id),
  bot_id BIGINT NOT NULL REFERENCES bot_accounts(bot_id),
  run_id TEXT NOT NULL DEFAULT '',
  sequence INTEGER NOT NULL CHECK (sequence > 0),
  tool_call_id TEXT,
  text_content TEXT NOT NULL,
  status TEXT NOT NULL
    CHECK (status IN ('prepared','sending','sent','failed','unknown','denied','cancelled')),
  platform_message_id BIGINT,
  error_code TEXT,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (job_id, sequence)
);

-- One tool call must never produce two records, even when the run retries it.
-- 一次工具调用绝不能产生两条记录，即使运行内重试该调用。
CREATE UNIQUE INDEX agent_outbound_job_tool_call_uidx
  ON agent_outbound_messages(job_id, tool_call_id)
  WHERE tool_call_id IS NOT NULL;
CREATE INDEX agent_outbound_conversation_timeline
  ON agent_outbound_messages(conversation_id, created_at, sequence);

CREATE TABLE agent_run_budgets (
  job_id BIGINT PRIMARY KEY REFERENCES message_jobs(id) ON DELETE CASCADE,
  reserved_messages INTEGER NOT NULL DEFAULT 0,
  reserved_chars INTEGER NOT NULL DEFAULT 0,
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- The ledger cascades with its job, but recorded message history outlives the
-- job, so deleting a job clears the link instead of blocking the deletion.
-- 台账随任务级联删除，而消息历史比任务存续更久；删除任务时清除关联而非阻止删除。
ALTER TABLE messages
  ADD COLUMN outbound_id UUID REFERENCES agent_outbound_messages(id) ON DELETE SET NULL;
