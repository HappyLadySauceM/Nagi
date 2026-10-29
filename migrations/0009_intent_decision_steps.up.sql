-- Explainable decision steps: every decision keeps the policy, rules, semantic
-- and final stages so the console can answer "why did the bot reply".
-- 可解释的决策步骤：每条决策保留策略、规则、语义与最终四个阶段，
-- 控制台因此能回答「机器人为什么回复」。

ALTER TABLE intent_decisions
  ADD COLUMN ruleset_version TEXT NOT NULL DEFAULT '';

CREATE TABLE intent_decision_steps (
  id BIGSERIAL PRIMARY KEY,
  decision_id BIGINT NOT NULL REFERENCES intent_decisions(id) ON DELETE CASCADE,
  step_index INTEGER NOT NULL CHECK (step_index >= 0),
  phase TEXT NOT NULL,
  reason_code TEXT NOT NULL DEFAULT '',
  detail JSONB NOT NULL DEFAULT '{}'::jsonb,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (decision_id, step_index)
);

CREATE INDEX intent_decision_steps_decision
  ON intent_decision_steps(decision_id, step_index);
