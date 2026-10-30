ALTER TABLE intent_rulesets ADD COLUMN builtin_rules JSONB NOT NULL DEFAULT '{}'::jsonb;
ALTER TABLE intent_rule_entries ADD COLUMN enabled BOOLEAN NOT NULL DEFAULT true;
