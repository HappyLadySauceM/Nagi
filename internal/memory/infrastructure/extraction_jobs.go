package infrastructure

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/HappyLadySauceM/Nagi/internal/memory/domain"
)

const extractionJobColumns = `
	j.id::text, j.bot_id, j.conversation_id::text, j.scope_type, j.scope_id,
	j.message_id, j.authorization_event_id, j.status, j.attempts, j.round_attempts,
	j.next_run_at, COALESCE(j.last_error_code, ''), j.manual_retries,
	(j.message_id IS NOT NULL AND EXISTS(SELECT 1 FROM messages m WHERE m.id=j.message_id)) AS source_available,
	COALESCE((SELECT m.memory_authorization_event_id FROM messages m WHERE m.id=j.message_id), 0) AS message_authorization_event_id,
	j.created_at, j.updated_at`

// ListExtractionJobs returns one page of extraction jobs for management.
// ListExtractionJobs 返回一页管理用提取任务。
func (r *Repo) ListExtractionJobs(ctx context.Context, f domain.ExtractionJobFilter) ([]domain.ExtractionJob, int64, error) {
	f = f.Normalized()
	if f.BotID <= 0 {
		return nil, 0, fmt.Errorf("memory: extraction job bot id required")
	}
	where := []string{"j.bot_id=$1"}
	args := []any{f.BotID}
	if f.ConversationID != "" {
		args = append(args, f.ConversationID)
		where = append(where, fmt.Sprintf("j.conversation_id::text=$%d", len(args)))
	}
	if f.Status != "" {
		args = append(args, f.Status)
		where = append(where, fmt.Sprintf("j.status=$%d", len(args)))
	}
	clause := strings.Join(where, " AND ")
	var total int64
	if err := r.pool.QueryRow(ctx, "SELECT count(*) FROM memory_extraction_jobs j WHERE "+clause, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	query := `SELECT ` + extractionJobColumns + ` FROM memory_extraction_jobs j WHERE ` + clause + ` ORDER BY j.created_at DESC, j.id DESC LIMIT $` + fmt.Sprint(len(args)+1) + ` OFFSET $` + fmt.Sprint(len(args)+2)
	args = append(args, f.Limit, f.Offset())
	rows, err := r.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	items := make([]domain.ExtractionJob, 0)
	for rows.Next() {
		item, err := scanExtractionJob(rows)
		if err != nil {
			return nil, 0, err
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}
	return items, total, nil
}

// GetExtractionJob returns one extraction job by identifier.
// GetExtractionJob 按 ID 返回一条提取任务。
func (r *Repo) GetExtractionJob(ctx context.Context, id string) (domain.ExtractionJob, bool, error) {
	row := r.pool.QueryRow(ctx, `SELECT `+extractionJobColumns+` FROM memory_extraction_jobs j WHERE j.id=$1`, id)
	item, err := scanExtractionJob(row)
	if err == sql.ErrNoRows {
		return domain.ExtractionJob{}, false, nil
	}
	if err != nil {
		return domain.ExtractionJob{}, false, err
	}
	return item, true, nil
}

// RetryExtractionJob resets one failed job to pending without resetting its
// cumulative attempt count or exceeding the manual retry limit.
// RetryExtractionJob 将一个失败任务重置为 pending，不重置累计尝试次数，
// 且不超过手动重试上限。
func (r *Repo) RetryExtractionJob(ctx context.Context, id string, maxManualRetries int) (bool, error) {
	result, err := r.pool.Exec(ctx, `
		UPDATE memory_extraction_jobs
		SET status='pending', round_attempts=0, next_run_at=clock_timestamp(),
			claim_token=NULL, lease_expires_at=NULL, last_error_code=NULL,
			manual_retries=manual_retries+1, updated_at=clock_timestamp()
		WHERE id=$1 AND status='failed' AND manual_retries < $2
			AND message_id IS NOT NULL
			AND EXISTS(SELECT 1 FROM messages m WHERE m.id=memory_extraction_jobs.message_id)`, id, maxManualRetries)
	if err != nil {
		return false, err
	}
	return result.RowsAffected() == 1, nil
}

type extractionJobScanner interface {
	Scan(dest ...any) error
}

func scanExtractionJob(row extractionJobScanner) (domain.ExtractionJob, error) {
	var item domain.ExtractionJob
	var messageID sql.NullInt64
	var nextRunAt, createdAt, updatedAt time.Time
	err := row.Scan(
		&item.ID, &item.BotID, &item.ConversationID, &item.ScopeType, &item.ScopeID,
		&messageID, &item.AuthorizationEventID, &item.Status, &item.Attempts, &item.RoundAttempts,
		&nextRunAt, &item.LastErrorCode, &item.ManualRetries, &item.SourceAvailable,
		&item.MessageAuthorizationEventID, &createdAt, &updatedAt,
	)
	if err != nil {
		return domain.ExtractionJob{}, err
	}
	if messageID.Valid {
		item.MessageID = messageID.Int64
	}
	item.NextRunAt = nextRunAt
	item.CreatedAt = createdAt
	item.UpdatedAt = updatedAt
	return item, nil
}
