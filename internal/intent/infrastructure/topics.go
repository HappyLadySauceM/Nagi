package infrastructure

import (
	"context"
	"database/sql"
	"encoding/json"

	"github.com/HappyLadySauceM/Nagi/internal/intent/domain"
	ob "github.com/HappyLadySauceM/Nagi/internal/onebot/domain"
)

// Topics projects the latest completed decision per source message. The decision
// transaction is the only writer, and confirmed outbound messages resolve
// through their source job in the same conversation.
// Topics 从每条来源消息的最新已完成决策投影状态。由决策事务唯一写入，
// 已确认发送的消息通过同一会话中对应的来源任务关联话题。
func (r *DecisionsRepo) Topics(ctx context.Context, task domain.Task, reference int64, idleSeconds int) (*domain.TopicContext, error) {
	result := &domain.TopicContext{IdleSeconds: idleSeconds}
	rows, err := r.pool.Query(ctx, `
WITH current AS (
	SELECT m.id, m.bot_id, m.conversation_id, m.created_at
	FROM message_jobs j JOIN messages m ON m.id = j.incoming_message_id
	WHERE j.id = $1 AND m.bot_id = $2 AND m.conversation_id = $3::uuid
), reference AS (
	SELECT COALESCE(o.job_id, j.id) AS job_id, p.id AS message_id, p.direction = 'outgoing' AS bot
	FROM current c
	JOIN messages p ON p.conversation_id = c.conversation_id AND p.bot_id = c.bot_id
		AND p.platform_message_id = NULLIF($4::bigint, 0)
	LEFT JOIN message_jobs j ON j.incoming_message_id = p.id
	LEFT JOIN agent_outbound_messages o ON o.id = p.outbound_id AND o.conversation_id = c.conversation_id
	LEFT JOIN message_jobs reply_job ON reply_job.id = o.job_id
	LEFT JOIN messages origin ON origin.id = reply_job.incoming_message_id
		AND origin.conversation_id = c.conversation_id AND origin.bot_id = c.bot_id
	WHERE p.control_kind = '' AND ((p.direction = 'incoming' AND (p.created_at, p.id) < (c.created_at, c.id))
		OR (p.direction = 'outgoing' AND p.sent_status = 'sent' AND
			(CASE WHEN origin.id IS NULL THEN (p.created_at, p.id) < (c.created_at, c.id)
				ELSE (origin.created_at, origin.id) < (c.created_at, c.id) END)))
	ORDER BY p.created_at DESC, p.id DESC LIMIT 1
), quoted AS (
	SELECT x.bot, x.message_id, COALESCE((s.input->'TopicChange'->>'ID')::bigint, 0) AS topic_id
	FROM reference x
	LEFT JOIN LATERAL (
		SELECT d.id FROM intent_decisions d WHERE d.job_id = x.job_id ORDER BY id DESC LIMIT 1
	) d ON true
	LEFT JOIN intent_decision_snapshots s ON s.decision_id = d.id
), events AS MATERIALIZED (
	SELECT j.id AS job_id, p.id AS message_id, p.created_at,
		(s.input->'TopicChange'->>'ID')::bigint AS topic_id, s.input->'TopicChange' AS change
	FROM current c
	JOIN messages p ON p.conversation_id = c.conversation_id AND p.bot_id = c.bot_id
		AND p.direction = 'incoming' AND p.control_kind = '' AND (p.created_at, p.id) < (c.created_at, c.id)
		AND p.created_at >= c.created_at - interval '1 hour'
	JOIN message_jobs j ON j.incoming_message_id = p.id
	JOIN LATERAL (SELECT id FROM intent_decisions WHERE job_id = j.id ORDER BY id DESC LIMIT 1) d ON true
	JOIN intent_decision_snapshots s ON s.decision_id = d.id AND s.input ? 'TopicChange'
	WHERE j.status = 'done' OR EXISTS (
		SELECT 1 FROM agent_outbound_messages o WHERE o.job_id = j.id AND o.status = 'sent'
			AND NOT (o.payload @> '{"cross_group_fact":true}'::jsonb)
	)
), latest AS (
	SELECT DISTINCT ON (topic_id) topic_id, created_at, change
	FROM events ORDER BY topic_id, created_at DESC, message_id DESC
), candidates AS (
	SELECT l.topic_id, l.created_at, (l.change->>'ExpiresAt')::timestamptz AS expiry,
		COALESCE((l.change->>'Waiting')::boolean, false) AS waiting,
		l.change->>'Action' <> 'close' AND (l.change->>'ExpiresAt')::timestamptz > c.created_at AS active
	FROM latest l CROSS JOIN current c
	UNION ALL
	SELECT q.topic_id, NULL, NULL, false, false FROM quoted q
	WHERE q.topic_id <> 0 AND NOT EXISTS (SELECT 1 FROM latest l WHERE l.topic_id = q.topic_id)
), selected AS (
	SELECT s.* FROM candidates s
	WHERE (s.active AND NOT s.waiting) OR s.topic_id = (SELECT topic_id FROM quoted)
	ORDER BY COALESCE(s.topic_id = (SELECT topic_id FROM quoted), false) DESC,
		s.created_at DESC NULLS LAST, s.topic_id DESC LIMIT $5
)
SELECT COALESCE((SELECT topic_id FROM quoted), 0), COALESCE((SELECT bot FROM quoted), false),
	COALESCE(s.topic_id, 0), COALESCE(s.active, false), COALESCE(s.waiting, false), s.expiry,
	COALESCE(h.messages, '[]'::jsonb),
	(SELECT jsonb_build_object('MessageID', p.platform_message_id, 'SenderID', p.sender_id,
		'Text', p.text_content, 'CreatedAt', p.created_at, 'IsBot', q.bot, 'Wire', p.segments)
		FROM quoted q JOIN messages p ON p.id = q.message_id)
FROM current c LEFT JOIN selected s ON true
LEFT JOIN LATERAL (
	SELECT jsonb_agg(jsonb_build_object(
		'MessageID', COALESCE(p.platform_message_id, 0), 'SenderID', p.sender_id,
		'IsBot', p.direction = 'outgoing', 'CreatedAt', p.created_at,
		'Text', left(regexp_replace(p.text_content, '[[]CQ:[^]]*[]]', ' [附件] ', 'g'), 500)
	) ORDER BY p.created_at, p.id) AS messages
	FROM (
		SELECT m.* FROM (
				SELECT e.job_id FROM events e WHERE e.topic_id = s.topic_id
				ORDER BY e.created_at DESC, e.message_id DESC LIMIT 3
		) recent
		JOIN message_jobs j ON j.id = recent.job_id
		JOIN messages m ON m.id = j.incoming_message_id
		UNION
		SELECT outbound.* FROM (
			SELECT m.* FROM (
			SELECT e.job_id FROM events e WHERE e.topic_id = s.topic_id
			ORDER BY e.created_at DESC, e.message_id DESC LIMIT 3
			) recent
			JOIN agent_outbound_messages o ON o.job_id = recent.job_id AND o.status = 'sent'
			JOIN messages m ON m.outbound_id = o.id AND m.conversation_id = c.conversation_id
			WHERE m.control_kind = '' AND (m.created_at, m.id) < (c.created_at, c.id)
			ORDER BY m.created_at DESC, m.id DESC LIMIT 3
		) outbound
		UNION
		SELECT m.* FROM quoted q JOIN messages m ON m.id = q.message_id
		WHERE q.topic_id = s.topic_id
	) p
) h ON s.topic_id IS NOT NULL
ORDER BY COALESCE(s.topic_id = (SELECT topic_id FROM quoted), false) DESC,
	s.created_at DESC NULLS LAST, s.topic_id DESC`, task.JobID, task.BotID, task.ConversationID, reference, domain.TopicMaxCandidates)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var candidate domain.TopicCandidate
		var expiry sql.NullTime
		var history, quoted []byte
		if err := rows.Scan(&result.QuotedID, &result.QuotedBot, &candidate.ID, &candidate.Active, &candidate.Waiting, &expiry, &history, &quoted); err != nil {
			return nil, err
		}
		if result.Reference == nil && len(quoted) != 0 {
			var message struct {
				domain.Message
				Wire json.RawMessage
			}
			if err := json.Unmarshal(quoted, &message); err != nil {
				return nil, err
			}
			message.Segments, err = ob.SegmentsFromWire(message.Wire)
			if err != nil {
				return nil, err
			}
			result.Reference = &message.Message
		}
		if candidate.ID == 0 {
			continue
		}
		candidate.ExpiresAt = expiry.Time
		if err := json.Unmarshal(history, &candidate.History); err != nil {
			return nil, err
		}
		result.Candidates = append(result.Candidates, candidate)
	}
	return result, rows.Err()
}
