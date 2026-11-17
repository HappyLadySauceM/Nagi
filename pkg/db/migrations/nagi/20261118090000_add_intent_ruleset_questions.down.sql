-- reverse: modify "intent_rulesets" table
ALTER TABLE "intent_rulesets" DROP COLUMN "questions";

DELETE FROM schema_migrations WHERE version=20261118090000;
