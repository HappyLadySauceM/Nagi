// Package infrastructure implements the PostgreSQL outbound delivery ledger.
// outbound/infrastructure 实现基于 PostgreSQL 的出站投递台账。
package infrastructure

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/HappyLadySauceM/Nagi/pkg/db/ent"
	eo "github.com/HappyLadySauceM/Nagi/pkg/db/ent/agentoutboundmessage"
	"reflect"
	"time"

	"github.com/google/uuid"

	"github.com/HappyLadySauceM/Nagi/pkg/db"

	"github.com/HappyLadySauceM/Nagi/internal/outbound/application"
	"github.com/HappyLadySauceM/Nagi/internal/outbound/domain"
)

// Repo is the Ent-backed delivery ledger.
// Repo 是基于 Ent 的投递台账。
type Repo struct {
	pool *db.Pool
}

// NewRepo builds the repository; the pool must not be nil.
// NewRepo 构建仓库；pool 不能为 nil。
func NewRepo(pool *db.Pool) (*Repo, error) {
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

	if in.ClaimToken == "" {
		return domain.Record{}, ErrLeaseLost
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	var conversation string
	if err := tx.QueryRow(ctx, `SELECT id::text FROM conversations WHERE id=$1 FOR UPDATE`, in.ConversationID).Scan(&conversation); err != nil {
		return domain.Record{}, err
	}
	var jobID int64
	if err := tx.QueryRow(ctx,
		`SELECT j.id FROM message_jobs j JOIN messages m ON m.id=j.incoming_message_id WHERE j.id=$1 AND m.bot_id=$2 AND m.conversation_id=$3 AND j.claim_token=$4::uuid AND j.status='processing' AND j.lease_expires_at>clock_timestamp() FOR UPDATE OF j`, in.JobID, in.BotID, in.ConversationID, in.ClaimToken,
	).Scan(&jobID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return domain.Record{}, fmt.Errorf("outbound: reserve job %d: %w", in.JobID, domain.ErrRecordNotFound)
		}
		return domain.Record{}, fmt.Errorf("outbound: lock job %d: %w", in.JobID, err)
	}

	var sourceKind string
	var sourceTarget int64
	if err := tx.QueryRow(ctx, "SELECT kind,target_id FROM conversations WHERE id=$1 AND bot_id=$2", in.ConversationID, in.BotID).Scan(&sourceKind, &sourceTarget); err != nil {
		return domain.Record{}, err
	}
	if in.TargetKind == "" {
		in.TargetKind = sourceKind
		in.TargetID = sourceTarget
	}
	if (in.TargetKind != "group" && in.TargetKind != "private") || in.TargetID <= 0 {
		return domain.Record{}, fmt.Errorf("outbound: invalid target")
	}
	if in.Action == "" {
		in.Action = "send_" + in.TargetKind + "_msg"
	}
	if in.Payload == "" {
		raw, _ := json.Marshal(map[string]any{"message": in.Text, "segments": []map[string]any{{"type": "text", "data": map[string]string{"text": in.Text}}}})
		in.Payload = string(raw)
	}
	if !json.Valid([]byte(in.Payload)) || len(in.Payload) > 65536 {
		return domain.Record{}, fmt.Errorf("outbound: invalid payload")
	}
	if in.ChargeChars == 0 {
		in.ChargeChars = len([]rune(in.Text))
	}
	if in.ChargeChars < 0 {
		return domain.Record{}, fmt.Errorf("outbound: invalid cost")
	}
	var reusable *domain.Record
	if in.ToolCallID != "" {
		var old domain.Record
		var status string
		err := tx.QueryRow(ctx, `SELECT id::text,sequence,text_content,status,COALESCE(platform_message_id,0),target_kind,target_id,action,payload::text,charge_chars FROM agent_outbound_messages WHERE job_id=$1 AND tool_call_id=$2`, in.JobID, in.ToolCallID).Scan(&old.ID, &old.Sequence, &old.Text, &status, &old.PlatformMessageID, &old.TargetKind, &old.TargetID, &old.Action, &old.Payload, &old.ChargeChars)
		if err == nil {
			var oldPayload, newPayload any
			_ = json.Unmarshal([]byte(old.Payload), &oldPayload)
			_ = json.Unmarshal([]byte(in.Payload), &newPayload)
			if old.Text != in.Text || old.TargetKind != in.TargetKind || old.TargetID != in.TargetID || old.Action != in.Action || !reflect.DeepEqual(oldPayload, newPayload) || old.ChargeChars != in.ChargeChars {
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
				var live bool
				if err := tx.QueryRow(ctx, "SELECT status='processing' AND claim_token=$2::uuid AND lease_expires_at>clock_timestamp() FROM message_jobs WHERE id=$1", in.JobID, in.ClaimToken).Scan(&live); err != nil {
					return domain.Record{}, err
				}
				if !live {
					return domain.Record{}, ErrLeaseLost
				}
				old.ClaimToken = in.ClaimToken
				old.Status = domain.Status(status)
				return old, nil
			}
		}
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
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
	).Scan(&used.Messages, &used.Chars); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return domain.Record{}, fmt.Errorf("outbound: read run budget: %w", err)
	}
	if err := domain.CheckBudgetChars(in.Limits, used, in.ChargeChars); err != nil {
		return domain.Record{}, err
	}

	var sequence int
	if err := tx.QueryRow(ctx,
		`SELECT COALESCE(MAX(sequence), 0) + 1 FROM agent_outbound_messages WHERE job_id = $1`, in.JobID,
	).Scan(&sequence); err != nil {
		return domain.Record{}, fmt.Errorf("outbound: allocate sequence: %w", err)
	}

	record := domain.Record{
		ClaimToken:     in.ClaimToken,
		ID:             uuid.NewString(),
		JobID:          in.JobID,
		ConversationID: in.ConversationID,
		BotID:          in.BotID,
		RunID:          in.RunID,
		Sequence:       sequence,
		ToolCallID:     in.ToolCallID,
		Text:           in.Text,
		TargetKind:     in.TargetKind,
		TargetID:       in.TargetID,
		Action:         in.Action,
		Payload:        in.Payload,
		ChargeChars:    in.ChargeChars,
		Status:         domain.StatusPrepared,
	}
	if reusable != nil {
		record.ID = reusable.ID
		record.Sequence = reusable.Sequence
		if _, err := tx.Exec(ctx, "UPDATE agent_outbound_messages SET status='prepared',platform_message_id=NULL,error_code=NULL,run_id=$2,claim_token=$3::uuid,created_at=now(),updated_at=now() WHERE id=$1 AND status='cancelled'", record.ID, record.RunID, in.ClaimToken); err != nil {
			return domain.Record{}, err
		}
	} else {
		if _, err := tx.Exec(ctx, `
		INSERT INTO agent_outbound_messages
			(id, job_id, conversation_id, bot_id, run_id, sequence, tool_call_id, text_content, status,claim_token,target_kind,target_id,action,payload,charge_chars)
		VALUES ($1, $2, $3, $4, $5, $6, NULLIF($7, ''), $8, $9,$10::uuid,$11,$12,$13,$14::jsonb,$15)
	`, record.ID, record.JobID, record.ConversationID, record.BotID, record.RunID,
			record.Sequence, record.ToolCallID, record.Text, string(record.Status), in.ClaimToken, in.TargetKind, in.TargetID, in.Action, in.Payload, in.ChargeChars,
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
	chars := in.ChargeChars
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

	var live bool
	if err := tx.QueryRow(ctx, "SELECT status='processing' AND claim_token=$2::uuid AND lease_expires_at>clock_timestamp() FROM message_jobs WHERE id=$1", in.JobID, in.ClaimToken).Scan(&live); err != nil {
		return domain.Record{}, err
	}
	if !live {
		return domain.Record{}, ErrLeaseLost
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
func (r *Repo) MarkSending(ctx context.Context, id string, claim ...string) error {
	if len(claim) != 1 || claim[0] == "" {
		return ErrLeaseLost
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if err := lockDelivery(ctx, tx, id, claim[0]); err != nil {
		return err
	}
	result, err := tx.Exec(ctx, `UPDATE agent_outbound_messages SET status='sending',updated_at=clock_timestamp() WHERE id=$1 AND status='prepared' AND claim_token=$2::uuid AND EXISTS(SELECT 1 FROM message_jobs j WHERE j.id=agent_outbound_messages.job_id AND j.status='processing' AND j.claim_token=agent_outbound_messages.claim_token AND j.lease_expires_at>clock_timestamp())`, id, claim[0])
	if err != nil {
		return err
	}
	if result.RowsAffected() != 1 {
		var current string
		if err := tx.QueryRow(ctx, "SELECT status FROM agent_outbound_messages WHERE id=$1", id).Scan(&current); err != nil {
			return err
		}
		return &domain.TransitionError{RecordID: id, From: domain.Status(current), To: domain.StatusSending}
	}
	if err := lockDelivery(ctx, tx, id, claim[0]); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// Settle moves one sending record into sent, failed or unknown and stores the
// platform receipt or error code.
// Settle 把一条 sending 记录落定为 sent/failed/unknown，并写入平台回执或错误码。
func (r *Repo) Settle(ctx context.Context, id string, outcome application.Outcome, claim ...string) error {
	if !outcome.Status.Settleable() {
		return domain.ErrInvalidSettlement
	}
	if len(claim) != 1 || claim[0] == "" {
		return ErrLeaseLost
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `INSERT INTO conversations(id,bot_id,kind,target_id) SELECT gen_random_uuid(),bot_id,target_kind,target_id FROM agent_outbound_messages WHERE id=$1 ON CONFLICT(bot_id,kind,target_id) DO NOTHING`, id); err != nil {
		return err
	}
	if err := lockDelivery(ctx, tx, id, claim[0], true); err != nil {
		return err
	}
	result, err := tx.Exec(ctx, `UPDATE agent_outbound_messages SET status=$2,platform_message_id=NULLIF($3,0),error_code=NULLIF($4,''),updated_at=clock_timestamp() WHERE id=$1 AND status='sending' AND claim_token=$5::uuid AND EXISTS(SELECT 1 FROM message_jobs j WHERE j.id=agent_outbound_messages.job_id AND j.status='processing' AND j.claim_token=agent_outbound_messages.claim_token AND j.lease_expires_at>clock_timestamp())`, id, string(outcome.Status), outcome.PlatformMessageID, outcome.ErrorCode, claim[0])
	if err != nil {
		return err
	}
	if result.RowsAffected() != 1 {
		var state string
		if err := tx.QueryRow(ctx, "SELECT status FROM agent_outbound_messages WHERE id=$1", id).Scan(&state); err != nil {
			return err
		}
		return &domain.TransitionError{RecordID: id, From: domain.Status(state), To: outcome.Status}
	}

	// History follows the target conversation's own policy: a disabled or
	// non-storing target must not receive the text or an activity timestamp.
	// 历史写入沿用目标会话自己的策略：停用或不留存的会话不接收正文与活跃时间。
	_, err = tx.Exec(ctx, `INSERT INTO messages(conversation_id,bot_id,direction,platform_message_id,sender_id,text_content,segments,sent_status,outbound_id,created_at) SELECT c.id,o.bot_id,'outgoing',o.platform_message_id,o.bot_id,o.text_content,COALESCE(o.payload->'segments','[]'::jsonb),o.status,o.id,o.updated_at FROM agent_outbound_messages o JOIN conversations c ON c.bot_id=o.bot_id AND c.kind=o.target_kind AND c.target_id=o.target_id JOIN LATERAL(SELECT enabled,store_messages FROM scope_policies WHERE bot_id=c.bot_id AND scope_type=c.kind AND scope_id IN(c.target_id,0) ORDER BY scope_id DESC LIMIT 1)p ON p.enabled AND p.store_messages WHERE o.id=$1 ON CONFLICT(outbound_id) WHERE outbound_id IS NOT NULL DO NOTHING`, id)
	if err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE conversations c SET last_message_at=GREATEST(c.last_message_at,o.updated_at) FROM agent_outbound_messages o JOIN LATERAL(SELECT enabled,store_messages FROM scope_policies WHERE bot_id=o.bot_id AND scope_type=o.target_kind AND scope_id IN(o.target_id,0) ORDER BY scope_id DESC LIMIT 1)p ON p.enabled AND p.store_messages WHERE o.id=$1 AND c.bot_id=o.bot_id AND c.kind=o.target_kind AND c.target_id=o.target_id`, id); err != nil {
		return err
	}

	if err := lockDelivery(ctx, tx, id, claim[0], true); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// HasDelivered reports whether the job already has a sent or unknown record.
// HasDelivered 报告该任务是否已有 sent 或 unknown 记录。
func (r *Repo) HasDelivered(ctx context.Context, jobID int64) (bool, error) {
	return r.pool.Client.AgentOutboundMessage.Query().Where(eo.JobID(jobID), eo.StatusIn("sent", "unknown")).Exist(ctx)
}

// HasDeliveredTo reports whether this job delivered to one conversation target.
// HasDeliveredTo 报告该任务是否已向指定目标会话投递。
func (r *Repo) HasDeliveredTo(ctx context.Context, jobID int64, kind string, targetID int64) (bool, error) {
	return r.pool.Client.AgentOutboundMessage.Query().Where(
		eo.JobID(jobID),
		eo.StatusIn("sent", "unknown"),
		eo.TargetKind(kind),
		eo.TargetID(targetID),
	).Exist(ctx)
}

// ListByJob returns the delivery records of one job in sequence order; the
// admin API uses it to explain what a run actually sent.
// ListByJob 按序号返回某个任务的投递记录；管理 API 用它解释一次运行究竟发了什么。
func (r *Repo) ListByJob(ctx context.Context, jobID int64) ([]domain.Record, error) {
	rows, err := r.pool.Client.AgentOutboundMessage.Query().Where(eo.JobID(jobID)).Order(ent.Asc(eo.FieldSequence)).All(ctx)
	if err != nil {
		return nil, err
	}
	out := []domain.Record{}
	for _, v := range rows {
		record := domain.Record{ID: v.ID.String(), JobID: v.JobID, ConversationID: v.ConversationID.String(), BotID: v.BotID, RunID: v.RunID, Sequence: v.Sequence, Text: v.TextContent, Status: domain.Status(v.Status), ClaimToken: v.ClaimToken.String(), CreatedAt: v.CreatedAt, UpdatedAt: v.UpdatedAt}
		if v.ToolCallID != nil {
			record.ToolCallID = *v.ToolCallID
		}
		if v.PlatformMessageID != nil {
			record.PlatformMessageID = *v.PlatformMessageID
		}
		if v.ErrorCode != nil {
			record.ErrorCode = *v.ErrorCode
		}
		record.TargetKind = v.TargetKind
		record.TargetID = v.TargetID
		record.Action = v.Action
		record.ChargeChars = int(v.ChargeChars)
		raw, err := json.Marshal(v.Payload)
		if err != nil {
			return nil, err
		}
		record.Payload = string(raw)
		out = append(out, record)
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
func (r *Repo) Cancel(ctx context.Context, id string, claim ...string) error {
	if len(claim) != 1 || claim[0] == "" {
		return ErrLeaseLost
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if err := lockDelivery(ctx, tx, id, claim[0]); err != nil {
		return err
	}
	var state string
	if err := tx.QueryRow(ctx, "SELECT status FROM agent_outbound_messages WHERE id=$1", id).Scan(&state); err != nil {
		return err
	}
	if state == "cancelled" {
		return nil
	}
	if state != "prepared" {
		return &domain.TransitionError{RecordID: id, From: domain.Status(state), To: domain.StatusCancelled}
	}
	result, err := tx.Exec(ctx, `WITH cancelled AS(UPDATE agent_outbound_messages SET status='cancelled',updated_at=clock_timestamp() WHERE id=$1 AND status='prepared' AND claim_token=$2::uuid AND EXISTS(SELECT 1 FROM message_jobs j WHERE j.id=agent_outbound_messages.job_id AND j.status='processing' AND j.claim_token=agent_outbound_messages.claim_token AND j.lease_expires_at>clock_timestamp()) RETURNING job_id,charge_chars chars) UPDATE agent_run_budgets b SET reserved_messages=GREATEST(0,reserved_messages-1),reserved_chars=GREATEST(0,reserved_chars-c.chars) FROM cancelled c WHERE b.job_id=c.job_id`, id, claim[0])
	if err != nil {
		return err
	}
	if result.RowsAffected() != 1 {
		return ErrLeaseLost
	}
	if err := lockDelivery(ctx, tx, id, claim[0]); err != nil {
		return err
	}
	return tx.Commit(ctx)
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
func (r *Repo) RecordDenied(ctx context.Context, jobID int64, reason string, claim ...string) error {
	if len(claim) != 1 || claim[0] == "" {
		return ErrLeaseLost
	}
	result, err := r.pool.Exec(ctx, `UPDATE message_jobs SET delivery_outcome='blocked',delivery_reason=$2,denied_count=denied_count+1 WHERE id=$1 AND status='processing' AND claim_token=$3::uuid AND lease_expires_at>clock_timestamp()`, jobID, reason, claim[0])
	if err != nil {
		return err
	}
	if result.RowsAffected() != 1 {
		return ErrLeaseLost
	}
	return nil
}

// RecordRefusal attributes a refused tool call without blocking the job; the
// caller may still answer in the source conversation.
// RecordRefusal 记录被拒绝的工具调用但不阻断任务；调用方仍可回复来源会话。
func (r *Repo) RecordRefusal(ctx context.Context, jobID int64, reason string, claim ...string) error {
	if jobID <= 0 || reason == "" {
		return nil
	}
	if len(claim) != 1 || claim[0] == "" {
		return ErrLeaseLost
	}
	result, err := r.pool.Exec(ctx, `UPDATE message_jobs SET delivery_reason=$2,denied_count=denied_count+1 WHERE id=$1 AND status='processing' AND claim_token=$3::uuid AND lease_expires_at>clock_timestamp()`, jobID, reason, claim[0])
	if err != nil {
		return err
	}
	if result.RowsAffected() != 1 {
		return ErrLeaseLost
	}
	return nil
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
	err := r.pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM agent_outbound_messages p WHERE p.job_id=o.job_id AND p.sequence<=o.sequence AND p.status<>'cancelled'),o.charge_chars,COALESCE((SELECT sum(p.charge_chars) FROM agent_outbound_messages p WHERE p.job_id=o.job_id AND p.sequence<=o.sequence AND p.status<>'cancelled'),0) FROM agent_outbound_messages o WHERE o.id=$1 AND o.status='prepared'`, id).Scan(&sequence, &chars, &total)
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
