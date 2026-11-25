package infrastructure

import (
	"context"
	"database/sql"
	"encoding/json"

	"github.com/HappyLadySauceM/Nagi/internal/intent/domain"
	ob "github.com/HappyLadySauceM/Nagi/internal/onebot/domain"
	outbound "github.com/HappyLadySauceM/Nagi/internal/outbound/domain"
)

const topicReferenceSQL = `reference AS (
 SELECT COALESCE(o.job_id,j.id) AS job_id,p.id AS message_id,p.direction='outgoing' AS bot
 FROM current c JOIN messages p ON p.conversation_id=c.conversation_id AND p.bot_id=c.bot_id
  AND p.platform_message_id=NULLIF($4::bigint,0)
 LEFT JOIN message_jobs j ON j.incoming_message_id=p.id
 LEFT JOIN agent_outbound_messages o ON o.id=p.outbound_id AND o.conversation_id=c.conversation_id
  AND o.bot_id=c.bot_id AND o.platform_message_id=p.platform_message_id AND o.status='sent'
 LEFT JOIN message_jobs reply_job ON reply_job.id=o.job_id
 LEFT JOIN messages origin ON origin.id=reply_job.incoming_message_id AND origin.conversation_id=c.conversation_id
  AND origin.bot_id=c.bot_id AND origin.direction='incoming' AND origin.control_kind=''
 WHERE p.control_kind='' AND (
  (p.direction='incoming' AND (c.id IS NULL OR p.id<c.id) AND p.created_at<=c.created_at)
  OR (p.direction='outgoing' AND p.sender_id=c.bot_id AND p.sent_status='sent' AND origin.id IS NOT NULL
   AND (c.id IS NULL OR origin.id<c.id) AND origin.created_at<=c.created_at))
 ORDER BY p.created_at DESC,p.id DESC LIMIT 1
)`

// Topics projects the latest completed decision per source message. The decision
// transaction is the only writer, and confirmed outbound messages resolve
// through their source job in the same conversation.
// Topics 从每条来源消息的最新已完成决策投影状态。由决策事务唯一写入，
// 已确认发送的消息通过同一会话中对应的来源任务关联话题。
func (r *DecisionsRepo) Topics(ctx context.Context, task domain.Task, reference int64, idleSeconds int) (*domain.TopicContext, error) {
	if task.JobID != 0 {
		result, err := r.boundTopic(ctx, task, reference, idleSeconds)
		if err != nil || result != nil {
			return result, err
		}
	}
	result := &domain.TopicContext{IdleSeconds: idleSeconds}
	rows, err := r.pool.Query(ctx, `
WITH current AS (
	SELECT m.id, m.bot_id, m.conversation_id, m.created_at
	FROM message_jobs j JOIN messages m ON m.id = j.incoming_message_id
	WHERE j.id = $1 AND m.bot_id = $2 AND m.conversation_id = $3::uuid
 UNION ALL
 SELECT NULL::bigint,$2,$3::uuid,$6::timestamptz WHERE $1=0
), `+topicReferenceSQL+`, quoted AS (
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
		AND p.direction = 'incoming' AND p.control_kind = '' AND ((c.id IS NULL AND p.created_at<c.created_at) OR (p.id<c.id AND p.created_at<=c.created_at))
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
	SELECT l.topic_id,l.created_at,COALESCE(t.expires_at,(l.change->>'ExpiresAt')::timestamptz) AS expiry,
		COALESCE((l.change->>'Waiting')::boolean,false) AS waiting,
		NOT COALESCE(t.closed,l.change->>'Action'='close') AND COALESCE(t.expires_at,(l.change->>'ExpiresAt')::timestamptz)>c.created_at AS active
	FROM latest l CROSS JOIN current c
	LEFT JOIN group_topics t ON t.id=l.topic_id AND t.conversation_id=c.conversation_id AND EXISTS (
		SELECT 1 FROM group_topic_inputs i JOIN messages m ON m.id=i.route_id
		WHERE (i.topic_id,i.conversation_id,i.version)=(t.id,t.conversation_id,t.version)
		AND (c.id IS NULL OR m.id<c.id) AND m.created_at<=c.created_at)
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
	(SELECT jsonb_build_object('RowID', p.id, 'MessageID', p.platform_message_id, 'SenderID', p.sender_id,
		'Text', p.text_content, 'CreatedAt', p.created_at, 'IsBot', q.bot, 'Wire', p.segments)
		FROM quoted q JOIN messages p ON p.id = q.message_id)
FROM current c LEFT JOIN selected s ON true
LEFT JOIN LATERAL (
	SELECT jsonb_agg(jsonb_build_object(
		'RowID', p.id, 'MessageID', COALESCE(p.platform_message_id, 0), 'SenderID', p.sender_id,
		'IsBot', p.direction = 'outgoing', 'CreatedAt', p.created_at,
		'Text', p.text_content, 'Wire', p.segments
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
			WHERE m.control_kind='' AND ((c.id IS NULL AND m.created_at<c.created_at) OR (m.id<c.id AND m.created_at<=c.created_at))
			ORDER BY m.created_at DESC, m.id DESC LIMIT 3
		) outbound
		UNION
		SELECT m.* FROM quoted q JOIN messages m ON m.id = q.message_id
		WHERE q.topic_id = s.topic_id
	) p
) h ON s.topic_id IS NOT NULL
ORDER BY COALESCE(s.topic_id = (SELECT topic_id FROM quoted), false) DESC,
	s.created_at DESC NULLS LAST, s.topic_id DESC`, task.JobID, task.BotID, task.ConversationID, reference, domain.TopicMaxCandidates, task.Message.CreatedAt)
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
		var entries []struct {
			domain.Message
			Wire json.RawMessage
		}
		if err := json.Unmarshal(history, &entries); err != nil {
			return nil, err
		}
		for _, entry := range entries {
			entry.Segments, err = ob.SegmentsFromWire(entry.Wire)
			if err != nil {
				return nil, err
			}
			candidate.History = append(candidate.History, entry.Message)
		}
		result.Candidates = append(result.Candidates, candidate)
	}
	return result, rows.Err()
}

// boundTopic loads the exact inputs seen by routing, including turns whose drafts never shipped.
// boundTopic 加载归属版本的精确输入，包含旧稿未送达的回合。
func (r *DecisionsRepo) boundTopic(ctx context.Context, task domain.Task, reference int64, idleSeconds int) (*domain.TopicContext, error) {
	rows, err := r.pool.Query(ctx, `WITH current AS (
 SELECT m.id,m.bot_id,m.conversation_id,m.created_at FROM message_jobs j JOIN messages m ON m.id=j.incoming_message_id
 WHERE j.id=$1 AND m.bot_id=$2 AND m.conversation_id=$3
), `+topicReferenceSQL+`, binding AS (
 SELECT i.topic_id,i.version,i.conversation_id,i.route_id,m.created_at,t.expires_at,t.closed,t.version AS current_version
 FROM message_jobs j JOIN messages m ON m.id=j.incoming_message_id
 JOIN group_topic_inputs i ON (i.route_id,i.conversation_id)=(m.id,m.conversation_id)
 JOIN group_topics t ON (t.id,t.conversation_id)=(i.topic_id,i.conversation_id)
 WHERE j.id=$1 AND m.bot_id=$2 AND m.conversation_id=$3
), related AS (
 SELECT m.id,m.platform_message_id,m.sender_id,m.text_content,m.segments,m.created_at,m.direction
 FROM binding b JOIN group_topic_inputs i ON (i.topic_id,i.conversation_id)=(b.topic_id,b.conversation_id) AND i.version<b.version
 JOIN messages m ON (m.id,m.conversation_id)=(i.route_id,b.conversation_id) AND m.bot_id=$2 AND m.direction='incoming' AND m.control_kind='' AND m.id<b.route_id AND m.created_at<=b.created_at
 UNION ALL
 SELECT m.id,m.platform_message_id,m.sender_id,m.text_content,m.segments,m.created_at,m.direction
 FROM binding b JOIN group_topic_inputs i ON (i.topic_id,i.conversation_id)=(b.topic_id,b.conversation_id) AND i.version<b.version
 JOIN message_jobs j ON j.incoming_message_id=i.route_id JOIN agent_outbound_messages o ON o.job_id=j.id AND o.status='sent'
 JOIN messages m ON m.outbound_id=o.id AND m.conversation_id=b.conversation_id AND m.bot_id=$2 AND m.sent_status='sent' AND m.control_kind=''
 CROSS JOIN LATERAL (SELECT created_at FROM messages WHERE id=b.route_id) current
 WHERE (m.created_at,m.id)<(current.created_at,b.route_id)
) SELECT b.topic_id,b.version,b.expires_at,NOT b.closed AND b.version=b.current_version AND b.expires_at>b.created_at,
 COALESCE((SELECT jsonb_agg(jsonb_build_object('RowID',m.id,'MessageID',COALESCE(m.platform_message_id,0),'SenderID',m.sender_id,
 'Text',m.text_content,'Wire',m.segments,'CreatedAt',m.created_at,'IsBot',m.direction='outgoing') ORDER BY m.created_at,m.id) FROM related m),'[]'::jsonb)
 , (SELECT jsonb_build_object('RowID',p.id,'MessageID',p.platform_message_id,'SenderID',p.sender_id,
 'Text',p.text_content,'Wire',p.segments,'CreatedAt',p.created_at,'IsBot',ref.bot)
 FROM reference ref JOIN messages p ON p.id=ref.message_id),
 COALESCE((SELECT i.topic_id FROM reference ref JOIN message_jobs j ON j.id=ref.job_id
 JOIN group_topic_inputs i ON i.route_id=j.incoming_message_id AND i.conversation_id=b.conversation_id),0)
 FROM binding b`, task.JobID, task.BotID, task.ConversationID, reference)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	if !rows.Next() {
		return nil, rows.Err()
	}
	var candidate domain.TopicCandidate
	var version int64
	var raw, quoted []byte
	var quotedTopic int64
	if err := rows.Scan(&candidate.ID, &version, &candidate.ExpiresAt, &candidate.Active, &raw, &quoted, &quotedTopic); err != nil {
		return nil, err
	}
	var history []struct {
		domain.Message
		Wire json.RawMessage
	}
	if err := json.Unmarshal(raw, &history); err != nil {
		return nil, err
	}
	result := &domain.TopicContext{BoundID: candidate.ID, BoundVersion: version, IdleSeconds: idleSeconds}
	for _, entry := range history {
		entry.Segments, err = ob.SegmentsFromWire(entry.Wire)
		if err != nil {
			return nil, err
		}
		candidate.History = append(candidate.History, entry.Message)
	}
	if len(quoted) != 0 {
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
		result.Reference, result.QuotedID, result.QuotedBot = &message.Message, quotedTopic, message.IsBot
	}
	result.Candidates = []domain.TopicCandidate{candidate}
	return result, rows.Err()
}

// CheckTopicGeneration verifies the original input binding before starting generation.
// CheckTopicGeneration 在启动生成前核验原输入绑定。
func (r *DecisionsRepo) CheckTopicGeneration(ctx context.Context, task domain.Task, topic, expectedVersion int64) error {
	var valid bool
	err := r.pool.QueryRow(ctx, `SELECT EXISTS(
 SELECT 1 FROM message_jobs j JOIN group_topic_inputs i ON i.route_id=j.incoming_message_id
 JOIN group_topics t ON (t.id,t.conversation_id)=(i.topic_id,i.conversation_id)
 JOIN messages m ON m.id=i.route_id
 WHERE j.id=$1 AND j.claim_token=$2 AND j.status='processing' AND j.lease_expires_at>clock_timestamp()
 AND i.conversation_id=$3 AND i.topic_id=$4 AND ($5=0 OR i.version=$5)
 AND t.version=i.version AND NOT t.closed AND t.expires_at>m.created_at)`, task.JobID, task.ClaimToken, task.ConversationID, topic, expectedVersion).Scan(&valid)
	if err != nil {
		return err
	}
	if !valid {
		return &outbound.GateError{Reason: outbound.GateStale}
	}
	return nil
}
