DROP TABLE intent_run_events;
DROP TABLE intent_decision_runs;
DROP TABLE intent_stream_heads;
DELETE FROM schema_migrations WHERE version=20261122170000;
