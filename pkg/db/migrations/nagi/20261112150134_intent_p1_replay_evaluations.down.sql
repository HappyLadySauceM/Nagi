-- reverse: create index "intent_replay_evaluations_decision" to table: "intent_replay_evaluations"
DROP INDEX "intent_replay_evaluations_decision";
-- reverse: create index "intent_replay_evaluations_bot_id_id" to table: "intent_replay_evaluations"
DROP INDEX "intent_replay_evaluations_bot_id_id";
-- reverse: create "intent_replay_evaluations" table
DROP TABLE "intent_replay_evaluations";

DELETE FROM schema_migrations WHERE version=20261112150134;
