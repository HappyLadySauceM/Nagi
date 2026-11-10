package infrastructure

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/HappyLadySauceM/Nagi/internal/memory/domain"
)

// ListUnregisteredMessages returns a bounded batch of incoming messages that
// have a durable authorization stamp and no extraction job. The NOT EXISTS
// predicate is the durable dedupe: it deliberately has no id or time lower
// bound, so a message committed after an earlier scan remains eligible. LIMIT
// bounds each page and prevents unbounded scans.
// ListUnregisteredMessages 返回一批有持久授权戳且尚未建立提取任务的入站消息。
// NOT EXISTS 是持久去重条件，刻意不设 id 或时间下界，因此晚于上次扫描才提交的
// 消息仍然可被发现；LIMIT 控制每页大小，避免无界扫描。
func (r *Repo) ListUnregisteredMessages(ctx context.Context, botID int64, conversationID string, limit int) ([]domain.ExtractionSourceMessage, error) {
	if botID <= 0 || conversationID == "" || limit <= 0 {
		return nil, fmt.Errorf("memory: invalid extraction scan input")
	}
	rows, err := r.pool.Query(ctx, `
		SELECT m.id, m.bot_id, m.conversation_id::text, c.kind, c.target_id, m.sender_id,
			m.text_content, m.created_at, m.memory_authorization_event_id
		FROM messages m
		JOIN conversations c ON c.id = m.conversation_id
		JOIN scope_memory_authorization_events e
			ON e.id = m.memory_authorization_event_id
			AND e.bot_id = m.bot_id
			AND e.scope_type = c.kind
			AND (e.scope_id = c.target_id OR e.scope_id = 0)
		WHERE m.bot_id=$1 AND m.conversation_id=$2 AND m.direction='incoming'
			AND e.authorized = true
			AND NOT EXISTS (SELECT 1 FROM memory_extraction_jobs j WHERE j.message_id=m.id)
		ORDER BY m.id
		LIMIT $3`, botID, conversationID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]domain.ExtractionSourceMessage, 0)
	for rows.Next() {
		var item domain.ExtractionSourceMessage
		var eventID sql.NullInt64
		if err := rows.Scan(&item.ID, &item.BotID, &item.ConversationID, &item.ScopeType, &item.ScopeID, &item.SenderID, &item.Text, &item.CreatedAt, &eventID); err != nil {
			return nil, err
		}
		if eventID.Valid {
			item.AuthorizationEventID = eventID.Int64
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return items, nil
}
