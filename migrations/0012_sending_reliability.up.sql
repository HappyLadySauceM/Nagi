ALTER TABLE agent_sending_policies ADD COLUMN min_interval_ms INTEGER NOT NULL DEFAULT 700 CHECK (min_interval_ms >= 0);
CREATE UNIQUE INDEX messages_outbound_unique ON messages(outbound_id) WHERE outbound_id IS NOT NULL;
