// Package infrastructure implements the PostgreSQL outbound delivery ledger.
// outbound/infrastructure 实现基于 PostgreSQL 的出站投递台账。
package infrastructure

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/HappyLadySauceM/Nagi/internal/outbound/application"
	"github.com/HappyLadySauceM/Nagi/internal/outbound/domain"
)

// Repo is the pgx-backed delivery ledger.
// Repo 是基于 pgx 的投递台账。
type Repo struct {
	pool *pgxpool.Pool
}

// NewRepo builds the repository; the pool must not be nil.
// NewRepo 构建仓库；pool 不能为 nil。
func NewRepo(pool *pgxpool.Pool) (*Repo, error) {
	if pool == nil {
		return nil, fmt.Errorf("outbound: pool required")
	}
	return &Repo{pool: pool}, nil
}

// Reserve allocates the next sequence and budget for one message in a single
// transaction. The job row is locked first so sequence allocation, budget
// accounting and the delivered check are serialized per job, no matter how many
// runs or workers touch it. A rejected reservation inserts nothing and leaves
// the budget untouched.
// Reserve 在单个事务内为一条消息分配下一个序号与预算。先锁任务行，使序号分配、
// 预算累加与投递检查按任务串行，不受运行或工作器并发影响。
// 被拒绝的预留不插入任何记录，也不占用预算。
func (r *Repo) Reserve(ctx context.Context, in domain.ReserveInput) (domain.Record, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return domain.Record{}, fmt.Errorf("outbound: begin reserve: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var jobID int64
	if err := tx.QueryRow(ctx,
		`SELECT id FROM message_jobs WHERE id = $1 FOR UPDATE`, in.JobID,
	).Scan(&jobID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.Record{}, fmt.Errorf("outbound: reserve job %d: %w", in.JobID, domain.ErrRecordNotFound)
		}
		return domain.Record{}, fmt.Errorf("outbound: lock job %d: %w", in.JobID, err)
	}

	var used domain.Usage
	if err := tx.QueryRow(ctx,
		`SELECT reserved_messages, reserved_chars FROM agent_run_budgets WHERE job_id = $1`, in.JobID,
	).Scan(&used.Messages, &used.Chars); err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return domain.Record{}, fmt.Errorf("outbound: read run budget: %w", err)
	}
	if err := domain.CheckBudget(in.Limits, used, in.Text); err != nil {
		return domain.Record{}, err
	}

	var sequence int
	if err := tx.QueryRow(ctx,
		`SELECT COALESCE(MAX(sequence), 0) + 1 FROM agent_outbound_messages WHERE job_id = $1`, in.JobID,
	).Scan(&sequence); err != nil {
		return domain.Record{}, fmt.Errorf("outbound: allocate sequence: %w", err)
	}

	record := domain.Record{
		ID:             uuid.NewString(),
		JobID:          in.JobID,
		ConversationID: in.ConversationID,
		BotID:          in.BotID,
		RunID:          in.RunID,
		Sequence:       sequence,
		ToolCallID:     in.ToolCallID,
		Text:           in.Text,
		Status:         domain.StatusPrepared,
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO agent_outbound_messages
			(id, job_id, conversation_id, bot_id, run_id, sequence, tool_call_id, text_content, status)
		VALUES ($1, $2, $3, $4, $5, $6, NULLIF($7, ''), $8, $9)
	`, record.ID, record.JobID, record.ConversationID, record.BotID, record.RunID,
		record.Sequence, record.ToolCallID, record.Text, string(record.Status),
	); err != nil {
		return domain.Record{}, fmt.Errorf("outbound: insert record: %w", err)
	}

	chars := len([]rune(in.Text))
	if _, err := tx.Exec(ctx, `
		INSERT INTO agent_run_budgets (job_id, reserved_messages, reserved_chars)
		VALUES ($1, 1, $2)
		ON CONFLICT (job_id) DO UPDATE SET
			reserved_messages = agent_run_budgets.reserved_messages + 1,
			reserved_chars = agent_run_budgets.reserved_chars + EXCLUDED.reserved_chars,
			updated_at = now()
	`, in.JobID, chars); err != nil {
		return domain.Record{}, fmt.Errorf("outbound: reserve budget: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return domain.Record{}, fmt.Errorf("outbound: commit reserve: %w", err)
	}
	return record, nil
}

// MarkSending advances one prepared record to sending. A record in another
// state reports a transition error so callers never send twice.
// MarkSending 把一条 prepared 记录推进为 sending；状态不符时返回迁移错误，
// 调用方因此不会重复发送。
func (r *Repo) MarkSending(ctx context.Context, id string) error {
	tag, err := r.pool.Exec(ctx, `
		UPDATE agent_outbound_messages SET status = 'sending', updated_at = now()
		WHERE id = $1 AND status = 'prepared'
	`, id)
	if err != nil {
		return fmt.Errorf("outbound: mark sending: %w", err)
	}
	if tag.RowsAffected() > 0 {
		return nil
	}
	current, err := r.currentStatus(ctx, id)
	if err != nil {
		return err
	}
	return &domain.TransitionError{RecordID: id, From: current, To: domain.StatusSending}
}

// Settle moves one sending record into sent, failed or unknown and stores the
// platform receipt or error code.
// Settle 把一条 sending 记录落定为 sent/failed/unknown，并写入平台回执或错误码。
func (r *Repo) Settle(ctx context.Context, id string, outcome application.Outcome) error {
	if !outcome.Status.Settleable() {
		return fmt.Errorf("outbound: settle %s: %w", id, domain.ErrInvalidSettlement)
	}
	tag, err := r.pool.Exec(ctx, `
		UPDATE agent_outbound_messages SET
			status = $2,
			platform_message_id = NULLIF($3, 0),
			error_code = NULLIF($4, ''),
			updated_at = now()
		WHERE id = $1 AND status = 'sending'
	`, id, string(outcome.Status), outcome.PlatformMessageID, outcome.ErrorCode)
	if err != nil {
		return fmt.Errorf("outbound: settle: %w", err)
	}
	if tag.RowsAffected() > 0 {
		return nil
	}
	current, err := r.currentStatus(ctx, id)
	if err != nil {
		return err
	}
	return &domain.TransitionError{RecordID: id, From: current, To: outcome.Status}
}

// HasDelivered reports whether the job already has a sent or unknown record.
// HasDelivered 报告该任务是否已有 sent 或 unknown 记录。
func (r *Repo) HasDelivered(ctx context.Context, jobID int64) (bool, error) {
	var delivered bool
	if err := r.pool.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM agent_outbound_messages
			WHERE job_id = $1 AND status IN ('sent', 'unknown')
		)
	`, jobID).Scan(&delivered); err != nil {
		return false, fmt.Errorf("outbound: has delivered: %w", err)
	}
	return delivered, nil
}

// RecoverStale converts every record left in sending into unknown with the
// recovered error code and reports how many were changed.
// RecoverStale 把所有遗留的 sending 记录改为 unknown（错误码 recovered），
// 并返回变更条数。
func (r *Repo) RecoverStale(ctx context.Context) (int, error) {
	tag, err := r.pool.Exec(ctx, `
		UPDATE agent_outbound_messages SET status = 'unknown', error_code = 'recovered', updated_at = now()
		WHERE status = 'sending'
	`)
	if err != nil {
		return 0, fmt.Errorf("outbound: recover stale records: %w", err)
	}
	return int(tag.RowsAffected()), nil
}

// currentStatus loads the stored status for a failed conditional update; a
// missing row is reported as ErrRecordNotFound.
// currentStatus 在条件更新未命中时读取已存状态；记录不存在时返回 ErrRecordNotFound。
func (r *Repo) currentStatus(ctx context.Context, id string) (domain.Status, error) {
	var current string
	err := r.pool.QueryRow(ctx,
		`SELECT status FROM agent_outbound_messages WHERE id = $1`, id,
	).Scan(&current)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", fmt.Errorf("outbound: record %s: %w", id, domain.ErrRecordNotFound)
	}
	if err != nil {
		return "", fmt.Errorf("outbound: read record status: %w", err)
	}
	return domain.Status(current), nil
}

// ListByJob returns the delivery records of one job in sequence order; the
// admin API uses it to explain what a run actually sent.
// ListByJob 按序号返回某个任务的投递记录；管理 API 用它解释一次运行究竟发了什么。
func (r *Repo) ListByJob(ctx context.Context, jobID int64) ([]domain.Record, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT id::text, job_id, conversation_id::text, bot_id, run_id, sequence, tool_call_id,
		       text_content, status, COALESCE(platform_message_id, 0), COALESCE(error_code, ''),
		       created_at, updated_at
		FROM agent_outbound_messages
		WHERE job_id = $1
		ORDER BY sequence
	`, jobID)
	if err != nil {
		return nil, fmt.Errorf("outbound: list records: %w", err)
	}
	defer rows.Close()
	var out []domain.Record
	for rows.Next() {
		var (
			record  domain.Record
			status  string
			created time.Time
			updated time.Time
		)
		if err := rows.Scan(&record.ID, &record.JobID, &record.ConversationID, &record.BotID,
			&record.RunID, &record.Sequence, &record.ToolCallID, &record.Text, &status,
			&record.PlatformMessageID, &record.ErrorCode, &created, &updated); err != nil {
			return nil, fmt.Errorf("outbound: scan record: %w", err)
		}
		record.Status = domain.Status(status)
		record.CreatedAt = created
		record.UpdatedAt = updated
		out = append(out, record)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("outbound: scan records: %w", err)
	}
	return out, nil
}
