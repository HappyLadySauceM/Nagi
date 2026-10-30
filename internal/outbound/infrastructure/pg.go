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

	var conversation string
	if err := tx.QueryRow(ctx, `SELECT id::text FROM conversations WHERE id=$1 FOR UPDATE`, in.ConversationID).Scan(&conversation); err != nil {
		return domain.Record{}, err
	}
	var jobID int64
	if err := tx.QueryRow(ctx,
		`SELECT j.id FROM message_jobs j JOIN messages m ON m.id=j.incoming_message_id WHERE j.id=$1 AND m.bot_id=$2 AND m.conversation_id=$3 FOR UPDATE OF j`, in.JobID, in.BotID, in.ConversationID,
	).Scan(&jobID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.Record{}, fmt.Errorf("outbound: reserve job %d: %w", in.JobID, domain.ErrRecordNotFound)
		}
		return domain.Record{}, fmt.Errorf("outbound: lock job %d: %w", in.JobID, err)
	}

	var reusable *domain.Record
	if in.ToolCallID != "" {
		var old domain.Record
		var status string
		err := tx.QueryRow(ctx, `SELECT id::text,sequence,text_content,status,COALESCE(platform_message_id,0) FROM agent_outbound_messages WHERE job_id=$1 AND tool_call_id=$2`, in.JobID, in.ToolCallID).Scan(&old.ID, &old.Sequence, &old.Text, &status, &old.PlatformMessageID)
		if err == nil {
			if old.Text != in.Text {
				return domain.Record{}, fmt.Errorf("outbound: conflicting tool call")
			}
			if status == string(domain.StatusCancelled) {
				reusable = &old
			} else {
				if in.Limits.MinIntervalMS > 0 {
					var last time.Time
					if err := tx.QueryRow(ctx, `SELECT COALESCE(MAX(updated_at),'0001-01-01'::timestamptz) FROM agent_outbound_messages WHERE conversation_id=$1 AND id<>$2 AND status IN ('sent','unknown')`, in.ConversationID, old.ID).Scan(&last); err != nil {
						return domain.Record{}, err
					}
					if !last.IsZero() {
						old.NotBefore = last.Add(time.Duration(in.Limits.MinIntervalMS) * time.Millisecond)
					}
				}
				old.Status = domain.Status(status)
				return old, nil
			}
		}
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return domain.Record{}, err
		}
	}
	var stopped bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM agent_outbound_messages WHERE job_id=$1 AND status IN ('failed','unknown','sending')) OR EXISTS(SELECT 1 FROM message_jobs WHERE id=$1 AND delivery_outcome='blocked')`, in.JobID).Scan(&stopped); err != nil {
		return domain.Record{}, err
	}
	if stopped && in.Limits.StopAfterFailure {
		return domain.Record{}, fmt.Errorf("outbound: run stopped after uncertain or failed delivery")
	}
	if in.Limits.MaxPerHour > 0 {
		var hourly int
		if err := tx.QueryRow(ctx, `SELECT (SELECT count(*) FROM agent_outbound_messages WHERE conversation_id=$1 AND created_at>now()-interval '1 hour' AND status IN ('prepared','sending','sent','unknown')) + (SELECT count(*) FROM messages WHERE conversation_id=$1 AND direction='outgoing' AND outbound_id IS NULL AND sent_status IN ('sent','unknown') AND created_at>now()-interval '1 hour')`, in.ConversationID).Scan(&hourly); err != nil {
			return domain.Record{}, err
		}
		if hourly >= in.Limits.MaxPerHour {
			return domain.Record{}, &domain.BudgetError{Dimension: "hourly_messages", Limit: in.Limits.MaxPerHour, Requested: hourly + 1}
		}
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
	if reusable != nil {
		record.ID = reusable.ID
		record.Sequence = reusable.Sequence
		if _, err := tx.Exec(ctx, "UPDATE agent_outbound_messages SET status='prepared',platform_message_id=NULL,error_code=NULL,run_id=$2,created_at=now(),updated_at=now() WHERE id=$1 AND status='cancelled'", record.ID, record.RunID); err != nil {
			return domain.Record{}, err
		}
	} else {
		if _, err := tx.Exec(ctx, `
		INSERT INTO agent_outbound_messages
			(id, job_id, conversation_id, bot_id, run_id, sequence, tool_call_id, text_content, status)
		VALUES ($1, $2, $3, $4, $5, $6, NULLIF($7, ''), $8, $9)
	`, record.ID, record.JobID, record.ConversationID, record.BotID, record.RunID,
			record.Sequence, record.ToolCallID, record.Text, string(record.Status),
		); err != nil {
			return domain.Record{}, fmt.Errorf("outbound: insert record: %w", err)
		}

	}

	if in.Limits.MinIntervalMS > 0 {
		var last time.Time
		if err := tx.QueryRow(ctx, `SELECT COALESCE(MAX(updated_at),'0001-01-01'::timestamptz) FROM agent_outbound_messages WHERE conversation_id=$1 AND id<>$2 AND status IN ('sent','unknown')`, in.ConversationID, record.ID).Scan(&last); err != nil {
			return domain.Record{}, err
		}
		if !last.IsZero() {
			record.NotBefore = last.Add(time.Duration(in.Limits.MinIntervalMS) * time.Millisecond)
		}
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

// DeliveryError distinguishes successful completion from delivery failure.
// DeliveryError 区分正常完成与投递失败。
func (r *Repo) DeliveryError(ctx context.Context, jobID int64) error {
	var incomplete bool
	if err := r.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM agent_outbound_messages WHERE job_id=$1 AND status NOT IN ('sent','cancelled')) OR EXISTS(SELECT 1 FROM message_jobs WHERE id=$1 AND delivery_outcome='blocked')`, jobID).Scan(&incomplete); err != nil {
		return err
	}
	if incomplete {
		return fmt.Errorf("outbound: incomplete delivery")
	}
	return nil
}

// Cancel atomically cancels a prepared record and releases its run budget.
// Cancel 原子取消 prepared 记录并释放运行预算。
func (r *Repo) Cancel(ctx context.Context, id string) error {
	_, err := r.pool.Exec(ctx, `WITH cancelled AS (UPDATE agent_outbound_messages SET status='cancelled',updated_at=now() WHERE id=$1 AND status='prepared' RETURNING job_id,char_length(text_content) AS chars) UPDATE agent_run_budgets b SET reserved_messages=GREATEST(0,reserved_messages-1),reserved_chars=GREATEST(0,reserved_chars-c.chars) FROM cancelled c WHERE b.job_id=c.job_id`, id)
	return err
}

// OwnsJob checks the account boundary before exposing delivery contents.
// OwnsJob 在暴露投递内容前检查账号边界。
func (r *Repo) OwnsJob(ctx context.Context, botID, jobID int64) (bool, error) {
	var owns bool
	err := r.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM message_jobs j JOIN messages m ON m.id=j.incoming_message_id WHERE j.id=$1 AND m.bot_id=$2)`, jobID, botID).Scan(&owns)
	return owns, err
}

// RecordDenied stores an administrative explanation for blocked output.
// RecordDenied 保存输出被阻止时的管理诊断信息。
func (r *Repo) RecordDenied(ctx context.Context, jobID int64, reason string) error {
	_, err := r.pool.Exec(ctx, `UPDATE message_jobs SET delivery_outcome='blocked',delivery_reason=$2,denied_count=denied_count+1 WHERE id=$1`, jobID, reason)
	return err
}

// RunOutcome reads persisted refusal metadata for the delivery timeline.
// RunOutcome 读取投递时间线所需的持久化拒绝信息。
func (r *Repo) RunOutcome(ctx context.Context, jobID int64) (string, string, int, error) {
	var outcome, reason string
	var count int
	err := r.pool.QueryRow(ctx, `SELECT CASE WHEN delivery_outcome<>'' THEN delivery_outcome WHEN status='failed' THEN 'failed' WHEN status IN ('pending','processing') THEN 'processing' ELSE '' END,CASE WHEN delivery_reason<>'' THEN delivery_reason ELSE COALESCE(last_error_code,'') END,denied_count FROM message_jobs WHERE id=$1`, jobID).Scan(&outcome, &reason, &count)
	return outcome, reason, count, err
}

// ValidateReserved prevents lowered limits from being bypassed during a wait.
// ValidateReserved 防止等待期间下调的额度被旧预留绕过。
func (r *Repo) ValidateReserved(ctx context.Context, id string, limits domain.Limits) error {
	var sequence, chars, total int
	err := r.pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM agent_outbound_messages p WHERE p.job_id=o.job_id AND p.sequence<=o.sequence AND p.status<>'cancelled'),char_length(o.text_content),COALESCE((SELECT sum(char_length(p.text_content)) FROM agent_outbound_messages p WHERE p.job_id=o.job_id AND p.sequence<=o.sequence AND p.status<>'cancelled'),0) FROM agent_outbound_messages o WHERE o.id=$1 AND o.status='prepared'`, id).Scan(&sequence, &chars, &total)
	if err != nil {
		return err
	}
	// Zero means unlimited; only positive limits are enforced.
	// 0 表示不限，只对正数上限做校验。
	if limits.MaxMessages > 0 && sequence > limits.MaxMessages {
		return &domain.BudgetError{Dimension: domain.DimensionMessages, Limit: limits.MaxMessages, Requested: sequence}
	}
	if limits.MaxCharsPerMessage > 0 && chars > limits.MaxCharsPerMessage {
		return &domain.BudgetError{Dimension: domain.DimensionMessageChars, Limit: limits.MaxCharsPerMessage, Requested: chars}
	}
	if limits.MaxTotalChars > 0 && total > limits.MaxTotalChars {
		return &domain.BudgetError{Dimension: domain.DimensionTotalChars, Limit: limits.MaxTotalChars, Requested: total}
	}
	if limits.MaxPerHour > 0 {
		var hourly int
		err := r.pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM agent_outbound_messages p WHERE p.conversation_id=o.conversation_id AND p.created_at>now()-interval '1 hour' AND p.status IN ('prepared','sending','sent','unknown')) + (SELECT count(*) FROM messages m WHERE m.conversation_id=o.conversation_id AND m.direction='outgoing' AND m.outbound_id IS NULL AND m.sent_status IN ('sent','unknown') AND m.created_at>now()-interval '1 hour') FROM agent_outbound_messages o WHERE o.id=$1`, id).Scan(&hourly)
		if err != nil {
			return err
		}
		if hourly > limits.MaxPerHour {
			return &domain.BudgetError{Dimension: "hourly_messages", Limit: limits.MaxPerHour, Requested: hourly}
		}
	}
	return nil
}
