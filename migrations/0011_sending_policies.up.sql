-- Per-scope sending policy: the gray-release switch for autonomous replies.
-- 按作用域的出站策略：自主回复的灰度开关。

CREATE TABLE agent_sending_policies (
  bot_id BIGINT NOT NULL,
  scope_type TEXT NOT NULL CHECK (scope_type IN ('group','private')),
  scope_id BIGINT NOT NULL,
  allow_send_current BOOLEAN NOT NULL DEFAULT false,
  max_messages_per_run INTEGER NOT NULL DEFAULT 3 CHECK (max_messages_per_run > 0),
  max_chars_per_message INTEGER NOT NULL DEFAULT 500 CHECK (max_chars_per_message > 0),
  max_total_chars_per_run INTEGER NOT NULL DEFAULT 1500 CHECK (max_total_chars_per_run > 0),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (bot_id, scope_type, scope_id),
  CHECK (max_chars_per_message <= max_total_chars_per_run)
);
