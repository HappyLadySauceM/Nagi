DELETE FROM memory_extraction_cursors;
UPDATE scope_policies SET memory_authorization_event_id = NULL;
DELETE FROM scope_memory_authorization_events WHERE event_kind = 'bootstrap';

DELETE FROM schema_migrations WHERE version=20261110170000;
