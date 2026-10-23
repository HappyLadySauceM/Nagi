-- Pipeline tables: conversations, messages, jobs, active threads, intent decisions and memories.
-- 流水线表：会话、消息、任务、活跃线程、意图决策与长期记忆。

CREATE TABLE conversations (
  id UUID PRIMARY KEY,
  bot_id BIGINT NOT NULL REFERENCES bot_accounts(bot_id),
  kind TEXT NOT NULL CHECK (kind IN ('group','private')),
  target_id BIGINT NOT NULL,
  summary TEXT NOT NULL DEFAULT '',
  summary_upto_message_id BIGINT,
  last_message_at TIMESTAMPTZ,
  UNIQUE (bot_id, kind, target_id)
);

CREATE TABLE messages (
  id BIGSERIAL PRIMARY KEY,
  conversation_id UUID NOT NULL REFERENCES conversations(id),
  bot_id BIGINT NOT NULL REFERENCES bot_accounts(bot_id),
  direction TEXT NOT NULL CHECK (direction IN ('incoming','outgoing')),
  platform_message_id BIGINT,
  sender_id BIGINT NOT NULL,
  text_content TEXT NOT NULL DEFAULT '',
  segments JSONB NOT NULL DEFAULT '[]'::jsonb,
  sent_status TEXT,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Deduplicate the same platform event inside one conversation.
-- 同一会话内对同一平台事件去重。
CREATE UNIQUE INDEX messages_incoming_event_uniq
  ON messages(bot_id, conversation_id, platform_message_id)
  WHERE direction = 'incoming' AND platform_message_id IS NOT NULL;
CREATE INDEX messages_conversation_timeline
  ON messages(conversation_id, created_at, id);

CREATE TABLE message_jobs (
  id BIGSERIAL PRIMARY KEY,
  incoming_message_id BIGINT NOT NULL UNIQUE REFERENCES messages(id),
  status TEXT NOT NULL DEFAULT 'pending'
    CHECK (status IN ('pending','processing','done','failed','cancelled')),
  attempts INTEGER NOT NULL DEFAULT 0,
  next_run_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  last_error_code TEXT
);
CREATE INDEX message_jobs_ready ON message_jobs(status, next_run_at, id);

CREATE TABLE active_threads (
  thread_id UUID PRIMARY KEY,
  conversation_id UUID NOT NULL REFERENCES conversations(id),
  participants BIGINT[] NOT NULL DEFAULT '{}',
  expires_at TIMESTAMPTZ NOT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX active_threads_conversation ON active_threads(conversation_id, expires_at);

CREATE TABLE intent_decisions (
  id BIGSERIAL PRIMARY KEY,
  job_id BIGINT NOT NULL REFERENCES message_jobs(id) ON DELETE CASCADE,
  provider TEXT NOT NULL,
  model TEXT NOT NULL DEFAULT '',
  decision TEXT NOT NULL CHECK (decision IN ('reply','silent','defer')),
  reason_code TEXT NOT NULL,
  scores JSONB NOT NULL DEFAULT '{}'::jsonb,
  latency_ms INTEGER NOT NULL DEFAULT 0,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX intent_decisions_job ON intent_decisions(job_id);

-- Long-term memories. The embedding column is intentionally deferred until the
-- embedding model (and its vector dimension) is selected; retrieval filters by
-- bot_id + visibility + owner_key + status first.
-- 长期记忆。embedding 列延后到选定 embedding 模型（及其向量维度）后再添加；
-- 检索先按 bot_id + visibility + owner_key + status 过滤。
CREATE TABLE memories (
  memory_id UUID PRIMARY KEY,
  bot_id BIGINT NOT NULL REFERENCES bot_accounts(bot_id),
  visibility TEXT NOT NULL CHECK (visibility IN ('bot_shared','group','private_user','conversation')),
  owner_key TEXT NOT NULL DEFAULT '',
  content TEXT NOT NULL,
  source_message_id BIGINT REFERENCES messages(id) ON DELETE SET NULL,
  source TEXT NOT NULL DEFAULT '',
  confidence REAL NOT NULL DEFAULT 0,
  status TEXT NOT NULL DEFAULT 'candidate'
    CHECK (status IN ('candidate','active','superseded','deleted')),
  approved_by TEXT NOT NULL DEFAULT '',
  expires_at TIMESTAMPTZ,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX memories_visibility ON memories(bot_id, visibility, owner_key, status);
