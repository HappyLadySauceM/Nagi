-- Core tables: bot account, directory (contacts/groups/members), scope policies and admin audit log.
-- 核心表：机器人账号、目录（联系人/群/成员）、作用域策略与管理审计日志。

CREATE TABLE bot_accounts (
  bot_id BIGINT PRIMARY KEY,
  nickname TEXT NOT NULL DEFAULT '',
  status TEXT NOT NULL DEFAULT 'unknown',
  last_seen_at TIMESTAMPTZ
);

CREATE TABLE contacts (
  bot_id BIGINT NOT NULL REFERENCES bot_accounts(bot_id),
  user_id BIGINT NOT NULL,
  nickname TEXT NOT NULL DEFAULT '',
  remark TEXT NOT NULL DEFAULT '',
  last_seen_at TIMESTAMPTZ,
  PRIMARY KEY (bot_id, user_id)
);

CREATE TABLE qq_groups (
  bot_id BIGINT NOT NULL REFERENCES bot_accounts(bot_id),
  group_id BIGINT NOT NULL,
  name TEXT NOT NULL DEFAULT '',
  member_count INTEGER NOT NULL DEFAULT 0,
  last_seen_at TIMESTAMPTZ,
  PRIMARY KEY (bot_id, group_id)
);

CREATE TABLE group_members (
  bot_id BIGINT NOT NULL REFERENCES bot_accounts(bot_id),
  group_id BIGINT NOT NULL,
  user_id BIGINT NOT NULL,
  nickname TEXT NOT NULL DEFAULT '',
  card TEXT NOT NULL DEFAULT '',
  role TEXT NOT NULL DEFAULT 'member',
  last_seen_at TIMESTAMPTZ,
  PRIMARY KEY (bot_id, group_id, user_id)
);

CREATE TABLE scope_policies (
  bot_id BIGINT NOT NULL REFERENCES bot_accounts(bot_id),
  scope_type TEXT NOT NULL CHECK (scope_type IN ('group','private')),
  scope_id BIGINT NOT NULL,
  enabled BOOLEAN NOT NULL DEFAULT FALSE,
  reply_mode TEXT NOT NULL DEFAULT 'mention_only'
    CHECK (reply_mode IN ('mention_only','intent_based','always_reply','observe_only')),
  store_messages BOOLEAN NOT NULL DEFAULT FALSE,
  allow_memory_write BOOLEAN NOT NULL DEFAULT FALSE,
  allow_history_read BOOLEAN NOT NULL DEFAULT FALSE,
  tool_policy JSONB NOT NULL DEFAULT '[]'::jsonb,
  min_reply_interval_ms INTEGER NOT NULL DEFAULT 10000,
  max_replies_per_hour INTEGER NOT NULL DEFAULT 20,
  retention_days INTEGER NOT NULL DEFAULT 30,
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (bot_id, scope_type, scope_id)
);

CREATE TABLE admin_audit_logs (
  id BIGSERIAL PRIMARY KEY,
  actor TEXT NOT NULL,
  action TEXT NOT NULL,
  resource TEXT NOT NULL,
  before JSONB,
  after JSONB,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
