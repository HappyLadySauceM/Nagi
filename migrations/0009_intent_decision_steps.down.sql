-- Remove the explainable decision steps and the ruleset version column.
-- 移除可解释的决策步骤与规则版本列。

DROP TABLE intent_decision_steps;

ALTER TABLE intent_decisions
  DROP COLUMN ruleset_version;
