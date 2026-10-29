-- Versioned intent rulesets: drafts are editable, published versions are
-- immutable, and an activation row points at the version in force.
-- 版本化的意图规则：草稿可编辑，已发布版本不可变，激活行指向生效版本。

CREATE TABLE intent_rulesets (
  id BIGSERIAL PRIMARY KEY,
  bot_id BIGINT NOT NULL,
  scope_type TEXT NOT NULL CHECK (scope_type IN ('group','private')),
  scope_id BIGINT NOT NULL,
  version INTEGER NOT NULL CHECK (version > 0),
  status TEXT NOT NULL CHECK (status IN ('draft','published','archived')),
  note TEXT NOT NULL DEFAULT '',
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  published_at TIMESTAMPTZ,
  UNIQUE (bot_id, scope_type, scope_id, version)
);

CREATE INDEX intent_rulesets_scope ON intent_rulesets(bot_id, scope_type, scope_id, version DESC);

CREATE TABLE intent_rule_entries (
  id BIGSERIAL PRIMARY KEY,
  ruleset_id BIGINT NOT NULL REFERENCES intent_rulesets(id) ON DELETE CASCADE,
  priority INTEGER NOT NULL,
  match JSONB NOT NULL DEFAULT '{}'::jsonb,
  action JSONB NOT NULL,
  UNIQUE (ruleset_id, priority)
);

CREATE TABLE intent_rule_activations (
  bot_id BIGINT NOT NULL,
  scope_type TEXT NOT NULL,
  scope_id BIGINT NOT NULL,
  ruleset_id BIGINT NOT NULL REFERENCES intent_rulesets(id) ON DELETE RESTRICT,
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (bot_id, scope_type, scope_id)
);
