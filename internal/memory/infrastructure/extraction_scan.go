package infrastructure

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/HappyLadySauceM/Nagi/internal/memory/domain"
)

// ListUnregisteredMessages returns a bounded batch of incoming messages that
// have a durable authorization stamp and no extraction job. The id watermark is
// only an accelerator; the created-at overlap lets a late commit with a lower
// id be found by a bounded backfill scan instead of being excluded forever.
// ListUnregisteredMessages 返回一批有持久授权戳且尚未建立提取任务的入站消息。
// message id 水位只用于加速；created_at 重叠窗口让晚提交的低 id 消息能被有界补扫，
// 而不是被永久排除。
func (r *Repo) ListUnregisteredMessages(ctx context.Context, botID int64, conversationID string, afterID int64, scannedAfter time.Time, limit int) ([]domain.ExtractionSourceMessage, error) {
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
			AND (m.id > $3 OR m.created_at >= $4)
		ORDER BY m.id
		LIMIT $5`, botID, conversationID, afterID, scannedAfter, limit)
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
