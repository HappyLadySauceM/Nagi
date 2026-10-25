-- Restore the structures removed by the matching up migration, using the exact
-- definitions from 0001/0002 so a rollback reproduces the previous schema.
-- 按迁移前的精确定义（含默认值与索引）还原 up 删除的结构。

ALTER TABLE bot_accounts
    ADD COLUMN status TEXT NOT NULL DEFAULT 'unknown';

ALTER TABLE conversations
    ADD COLUMN summary TEXT NOT NULL DEFAULT '',
    ADD COLUMN summary_upto_message_id BIGINT;

CREATE TABLE active_threads (
  thread_id UUID PRIMARY KEY,
  conversation_id UUID NOT NULL REFERENCES conversations(id),
  participants BIGINT[] NOT NULL DEFAULT '{}',
  expires_at TIMESTAMPTZ NOT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX active_threads_conversation ON active_threads(conversation_id, expires_at);
