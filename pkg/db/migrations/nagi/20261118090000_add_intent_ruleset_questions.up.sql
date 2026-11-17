-- modify "intent_rulesets" table
ALTER TABLE "intent_rulesets" ADD COLUMN "questions" jsonb NOT NULL DEFAULT '{}';

INSERT INTO schema_migrations(version,name) VALUES (20261118090000,'add_intent_ruleset_questions');
