-- Remove the agent outbound ledger, its run budgets and the message link.
-- 移除 Agent 出站台账、回合预算与消息关联列。

ALTER TABLE messages
  DROP COLUMN outbound_id;

DROP TABLE agent_run_budgets;

DROP TABLE agent_outbound_messages;
