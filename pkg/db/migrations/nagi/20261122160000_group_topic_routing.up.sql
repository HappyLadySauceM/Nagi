CREATE UNIQUE INDEX messages_topic_inbound_key ON messages (id, conversation_id, direction);

CREATE TABLE group_topics (
  id bigint PRIMARY KEY,
  conversation_id uuid NOT NULL,
  version bigint NOT NULL,
  expires_at timestamptz NOT NULL,
  closed boolean NOT NULL DEFAULT false,
  CONSTRAINT group_topics_version_check CHECK (version > 0),
  CONSTRAINT group_topics_conversation_fk FOREIGN KEY (conversation_id) REFERENCES conversations (id) ON DELETE CASCADE
);
CREATE UNIQUE INDEX group_topics_scope_key ON group_topics (id, conversation_id);
CREATE INDEX group_topics_open_scope ON group_topics (conversation_id, expires_at, id) WHERE NOT closed;

CREATE TABLE group_topic_routes (
  id bigint PRIMARY KEY,
  conversation_id uuid NOT NULL,
  message_direction text NOT NULL DEFAULT 'incoming',
  deadline timestamptz NOT NULL,
  claim_token uuid,
  lease_expires_at timestamptz,
  status text NOT NULL DEFAULT 'pending',
  CONSTRAINT group_topic_routes_direction_check CHECK (message_direction = 'incoming'),
  CONSTRAINT group_topic_routes_status_check CHECK (status IN ('pending', 'routing', 'resolved', 'unrelated', 'expired', 'unavailable', 'stale')),
  CONSTRAINT group_topic_routes_lease_check CHECK (
    (status = 'routing' AND claim_token IS NOT NULL AND lease_expires_at IS NOT NULL AND lease_expires_at <= deadline)
    OR (status <> 'routing' AND claim_token IS NULL AND lease_expires_at IS NULL)
  ),
  CONSTRAINT group_topic_routes_inbound_fk FOREIGN KEY (id, conversation_id, message_direction) REFERENCES messages (id, conversation_id, direction) ON DELETE CASCADE
);
CREATE UNIQUE INDEX group_topic_routes_scope_key ON group_topic_routes (id, conversation_id);
CREATE INDEX group_topic_routes_live_scope ON group_topic_routes (conversation_id, id) WHERE status IN ('pending', 'routing');
CREATE INDEX group_topic_routes_due ON group_topic_routes (deadline, id) WHERE status IN ('pending', 'routing');

CREATE TABLE group_topic_route_candidates (
  id uuid PRIMARY KEY,
  conversation_id uuid NOT NULL,
  route_id bigint NOT NULL,
  topic_id bigint NOT NULL,
  version bigint NOT NULL,
  CONSTRAINT group_topic_route_candidates_version_check CHECK (version > 0),
  CONSTRAINT group_topic_candidates_route_fk FOREIGN KEY (route_id, conversation_id) REFERENCES group_topic_routes (id, conversation_id) ON DELETE CASCADE,
  CONSTRAINT group_topic_candidates_topic_fk FOREIGN KEY (topic_id, conversation_id) REFERENCES group_topics (id, conversation_id) ON DELETE CASCADE
);
CREATE UNIQUE INDEX group_topic_route_candidates_route_topic_key ON group_topic_route_candidates (route_id, topic_id);
CREATE INDEX group_topic_route_candidates_topic_routes ON group_topic_route_candidates (topic_id, route_id);

CREATE TABLE group_topic_inputs (
  id uuid PRIMARY KEY,
  conversation_id uuid NOT NULL,
  topic_id bigint NOT NULL,
  version bigint NOT NULL,
  route_id bigint NOT NULL,
  action text NOT NULL,
  CONSTRAINT group_topic_inputs_version_check CHECK (version > 0),
  CONSTRAINT group_topic_inputs_action_check CHECK (action IN ('opened', 'waiting', 'follow_up', 'contribution', 'corrected', 'replaced', 'merged', 'close')),
  CONSTRAINT group_topic_inputs_route_fk FOREIGN KEY (route_id, conversation_id) REFERENCES group_topic_routes (id, conversation_id) ON DELETE CASCADE,
  CONSTRAINT group_topic_inputs_topic_fk FOREIGN KEY (topic_id, conversation_id) REFERENCES group_topics (id, conversation_id) ON DELETE CASCADE
);
CREATE UNIQUE INDEX group_topic_inputs_topic_version_key ON group_topic_inputs (topic_id, version);
CREATE UNIQUE INDEX group_topic_inputs_route_key ON group_topic_inputs (route_id);

ALTER TABLE message_jobs ADD COLUMN delivery_continuation boolean NOT NULL DEFAULT false;
ALTER TABLE agent_outbound_messages ADD COLUMN resume_digest text, ADD COLUMN resume_result jsonb, ADD COLUMN memory_dependencies jsonb;

ALTER TABLE scope_policies
  ADD COLUMN send_permit_instance uuid NOT NULL DEFAULT gen_random_uuid(),
  ADD COLUMN send_permit_generations jsonb NOT NULL DEFAULT '{}'::jsonb;
ALTER TABLE system_settings
  ADD COLUMN send_permit_instance uuid NOT NULL DEFAULT gen_random_uuid(),
  ADD COLUMN send_permit_generations jsonb NOT NULL DEFAULT '{}'::jsonb;

CREATE TABLE send_scope_selections (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  bot_id bigint NOT NULL,
  scope_type text NOT NULL,
  scope_id bigint NOT NULL,
  CONSTRAINT send_scope_selections_exact_scope_check CHECK (bot_id > 0 AND scope_id > 0 AND scope_type IN ('group', 'private'))
);
CREATE UNIQUE INDEX send_scope_selections_scope_key ON send_scope_selections(bot_id, scope_type, scope_id);
INSERT INTO send_scope_selections(bot_id, scope_type, scope_id)
 SELECT bot_id, scope_type, scope_id FROM scope_policies WHERE scope_id > 0;

ALTER TABLE agent_outbound_messages ADD COLUMN send_permit jsonb,
  ADD CONSTRAINT agent_outbound_messages_send_permit_check CHECK (
    send_permit IS NULL OR CASE WHEN jsonb_typeof(send_permit) = 'array' THEN jsonb_array_length(send_permit) > 0 ELSE false END
  );

-- Close pre-upgrade unsent slots without reconstructing historical authorization.
-- 关闭无法核实原许可的迁移前未发槽。
WITH unsent AS MATERIALIZED (
  SELECT id, job_id, status, charge_chars FROM agent_outbound_messages
  WHERE send_permit IS NULL AND (status = 'prepared'
    OR (status = 'cancelled' AND COALESCE(error_code, '') IN ('', 'lease_recovered', 'lease_expired_prepared_refunded')))
), closed AS (
  UPDATE agent_outbound_messages o SET status = 'cancelled', error_code = 'authorization_revoked', updated_at = clock_timestamp()
  FROM unsent u WHERE o.id = u.id RETURNING u.job_id, u.status, u.charge_chars
), affected AS (
  SELECT job_id, count(*) AS slots, count(*) FILTER (WHERE status = 'prepared') AS reserved,
         COALESCE(sum(charge_chars) FILTER (WHERE status = 'prepared'), 0) AS chars
  FROM closed GROUP BY job_id
), refunded AS (
  UPDATE agent_run_budgets b SET reserved_messages = GREATEST(0, b.reserved_messages - a.reserved),
    reserved_chars = GREATEST(0, b.reserved_chars - a.chars), updated_at = clock_timestamp()
  FROM affected a WHERE b.job_id = a.job_id AND a.reserved > 0 RETURNING b.job_id
)
UPDATE message_jobs j SET delivery_outcome = 'blocked', delivery_reason = 'authorization_revoked',
  denied_count = j.denied_count + a.slots,
  status = CASE WHEN busy.sending THEN j.status ELSE 'failed' END,
  claim_token = CASE WHEN busy.sending THEN j.claim_token ELSE NULL END,
  lease_expires_at = CASE WHEN busy.sending THEN j.lease_expires_at ELSE NULL END,
  last_error_code = CASE WHEN busy.sending THEN j.last_error_code ELSE 'authorization_revoked' END,
  updated_at = clock_timestamp()
FROM affected a CROSS JOIN LATERAL (
  SELECT EXISTS (SELECT 1 FROM agent_outbound_messages WHERE job_id = a.job_id AND status = 'sending') AS sending
) busy
WHERE j.id = a.job_id AND j.status IN ('pending', 'processing');

INSERT INTO schema_migrations(version,name) VALUES (20261122160000,'group_topic_routing');
