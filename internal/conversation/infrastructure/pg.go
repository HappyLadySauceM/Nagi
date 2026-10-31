// Package infrastructure implements the conversation repository, ingress handler and worker.
// conversation/infrastructure 实现会话仓库、入站处理器与工作器。
package infrastructure

import (
	"context"
	"encoding/json"
	entsql "entgo.io/ent/dialect/sql"
	"github.com/HappyLadySauceM/Nagi/pkg/db/ent"
	ec "github.com/HappyLadySauceM/Nagi/pkg/db/ent/conversation"
	em "github.com/HappyLadySauceM/Nagi/pkg/db/ent/message"

	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/HappyLadySauceM/Nagi/pkg/db"

	"github.com/HappyLadySauceM/Nagi/internal/conversation/domain"
	obdomain "github.com/HappyLadySauceM/Nagi/internal/onebot/domain"
)

// Repo is the Ent-backed conversation repository.
// Repo 是基于 Ent 的会话仓库。
type Repo struct {
	pool *db.Pool
}

// NewRepo builds the repository; the pool must not be nil.
// NewRepo 构建仓库；pool 不能为 nil。
func NewRepo(pool *db.Pool) (*Repo, error) {
	if pool == nil {
		return nil, fmt.Errorf("conversation: pool required")
	}
	return &Repo{pool: pool}, nil
}

// Ingest persists one inbound message and creates its decision job atomically.
// The messages unique index deduplicates replayed platform events.
// Ingest 原子地持久化一条入站消息并创建其决策任务。
// messages 唯一索引对重复的平台事件去重。
func (r *Repo) Ingest(ctx context.Context, in domain.IngestInput) (domain.IngestResult, error) {
	tx, err := r.pool.Client.Tx(ctx)
	if err != nil {
		return domain.IngestResult{}, err
	}
	defer tx.Rollback()
	c := tx.Client()
	if err := c.BotAccount.Create().SetID(in.BotID).OnConflict(entsql.ConflictColumns("bot_id")).Ignore().Exec(ctx); err != nil {
		return domain.IngestResult{}, err
	}
	conv, err := c.Conversation.Create().SetID(uuid.New()).SetBotID(in.BotID).SetKind(string(in.Kind)).SetTargetID(in.TargetID).SetLastMessageAt(time.Now()).OnConflict(entsql.ConflictColumns("bot_id", "kind", "target_id")).UpdateLastMessageAt().ID(ctx)
	if err != nil {
		return domain.IngestResult{}, err
	}
	segments, err := encodeSegments(in.Segments)
	if err != nil {
		return domain.IngestResult{}, err
	}
	// The conversation upsert holds its row lock until commit, serializing event deduplication.
	// 会话 upsert 持有行锁直至提交，串行化同一会话的事件去重。
	exists, err := c.Message.Query().Where(em.BotID(in.BotID), em.ConversationID(conv), em.PlatformMessageID(in.PlatformMessageID), em.Direction("incoming")).Exist(ctx)
	if err != nil {
		return domain.IngestResult{}, err
	}
	if exists {
		if err := tx.Commit(); err != nil {
			return domain.IngestResult{}, err
		}
		return domain.IngestResult{ConversationID: conv.String(), Duplicate: true}, nil
	}
	stored, err := c.Message.Create().SetConversationID(conv).SetBotID(in.BotID).SetDirection("incoming").SetPlatformMessageID(in.PlatformMessageID).SetSenderID(in.SenderID).SetTextContent(in.Text).SetSegments(segments).Save(ctx)
	if err != nil {
		return domain.IngestResult{}, err
	}
	message := stored.ID

	job, err := c.MessageJob.Create().SetIncomingMessageID(message).SetTraceparent(in.TraceParent).SetTracestate(in.TraceState).Save(ctx)
	if err != nil {
		return domain.IngestResult{}, err
	}
	if err := tx.Commit(); err != nil {
		return domain.IngestResult{}, err
	}
	return domain.IngestResult{ConversationID: conv.String(), MessageID: message, JobID: job.ID}, nil
}

// RecentMessages returns up to limit newest messages of one conversation, oldest first.
// RecentMessages 返回一个会话最多 limit 条最新消息，按时间正序。
func (r *Repo) RecentMessages(ctx context.Context, conversationID string, limit int) ([]domain.Message, error) {
	id, err := uuid.Parse(conversationID)
	if err != nil {
		return nil, err
	}
	rows, err := r.pool.Client.Message.Query().Where(em.ConversationID(id)).Order(ent.Desc(em.FieldCreatedAt, em.FieldID)).Limit(limit).All(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]domain.Message, 0, len(rows))
	for i := len(rows) - 1; i >= 0; i-- {
		v, err := messageModel(rows[i])
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, nil
}

// GetConversation returns one conversation when it exists.
// GetConversation 返回已存在的会话。
func (r *Repo) GetConversation(ctx context.Context, botID int64, kind domain.Kind, targetID int64) (domain.Conversation, bool, error) {
	v, err := r.pool.Client.Conversation.Query().Where(ec.BotID(botID), ec.Kind(string(kind)), ec.TargetID(targetID)).Only(ctx)
	if ent.IsNotFound(err) {
		return domain.Conversation{}, false, nil
	}
	if err != nil {
		return domain.Conversation{}, false, err
	}
	out := domain.Conversation{ID: v.ID.String(), BotID: v.BotID, Kind: domain.Kind(v.Kind), TargetID: v.TargetID, LastMsgAt: time.Unix(0, 0)}
	if v.LastMessageAt != nil {
		out.LastMsgAt = *v.LastMessageAt
	}
	return out, true, nil
}

// ClaimReadyJobs atomically claims due pending jobs by moving them to the
// 'processing' state and returns their execution context. FOR UPDATE SKIP LOCKED
// keeps concurrent polls or workers from claiming the same job twice, so a job
// sitting in a memory queue is never re-dispatched on the next poll.
// ClaimReadyJobs 将到期任务原子置为 processing 并返回执行上下文。
// SKIP LOCKED 保证并发轮询不会重复认领同一任务，内存队列中的任务
// 不会在下一个轮询周期被重复投递。
func (r *Repo) ClaimReadyJobs(ctx context.Context, limit int) ([]domain.Job, error) {
	if limit <= 0 {
		return []domain.Job{}, nil
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	locked, err := tx.Query(ctx, `SELECT c.id FROM conversations c WHERE EXISTS(SELECT 1 FROM message_jobs j JOIN messages m ON m.id=j.incoming_message_id WHERE m.conversation_id=c.id AND j.status='pending' AND j.next_run_at<=clock_timestamp()) AND NOT EXISTS(SELECT 1 FROM message_jobs busy JOIN messages bm ON bm.id=busy.incoming_message_id WHERE bm.conversation_id=c.id AND busy.status='processing') ORDER BY c.id LIMIT $1 FOR UPDATE OF c SKIP LOCKED`, limit)
	if err != nil {
		return nil, err
	}
	ids := []string{}
	for locked.Next() {
		var id string
		if err := locked.Scan(&id); err != nil {
			locked.Close()
			return nil, err
		}
		ids = append(ids, id)
	}
	err = locked.Err()
	locked.Close()
	if err != nil {
		return nil, err
	}
	encoded, _ := json.Marshal(ids)
	rows, err := tx.Query(ctx, `
 WITH picked AS (
 SELECT j.id FROM conversations c
 JOIN LATERAL(SELECT j.id FROM message_jobs j JOIN messages m ON m.id=j.incoming_message_id WHERE m.conversation_id=c.id AND j.status='pending' AND j.next_run_at<=clock_timestamp() ORDER BY j.next_run_at,j.id LIMIT 1) chosen ON true
 JOIN message_jobs j ON j.id=chosen.id
 WHERE c.id IN(SELECT value::uuid FROM jsonb_array_elements_text($1::jsonb))
 AND NOT EXISTS(SELECT 1 FROM message_jobs busy JOIN messages bm ON bm.id=busy.incoming_message_id WHERE bm.conversation_id=c.id AND busy.status='processing')
 FOR UPDATE OF j SKIP LOCKED
 ),claimed AS(
 UPDATE message_jobs j SET status='processing',claim_token=gen_random_uuid(),lease_expires_at=clock_timestamp()+interval '2 minutes',updated_at=clock_timestamp() FROM picked WHERE j.id=picked.id AND j.status='pending'
 RETURNING j.id,j.incoming_message_id,j.traceparent,j.tracestate,j.claim_token,j.lease_expires_at
 ) SELECT cl.id,cl.incoming_message_id,cl.traceparent,cl.tracestate,m.conversation_id,c.bot_id,c.kind,c.target_id,m.id,COALESCE(m.platform_message_id,0),m.sender_id,m.text_content,m.segments,m.created_at,cl.claim_token,cl.lease_expires_at
 FROM claimed cl JOIN messages m ON m.id=cl.incoming_message_id JOIN conversations c ON c.id=m.conversation_id ORDER BY cl.id`, encoded)
	if err != nil {
		return nil, fmt.Errorf("conversation: claim ready jobs: %w", err)
	}
	defer rows.Close()
	jobs := []domain.Job{}
	poison := []domain.Job{}
	for rows.Next() {
		var j domain.Job
		var kindStr string
		var segJSON []byte
		if err := rows.Scan(&j.ID, &j.MessageID, &j.TraceParent, &j.TraceState,
			&j.ConversationID, &j.BotID, &kindStr, &j.TargetID,
			&j.Message.ID, &j.Message.PlatformMessageID, &j.Message.SenderID, &j.Message.Text,
			&segJSON, &j.Message.CreatedAt, &j.ClaimToken, &j.LeaseExpiresAt); err != nil {
			return nil, fmt.Errorf("conversation: scan job: %w", err)
		}
		j.Kind = domain.Kind(kindStr)
		j.Message.ConversationID = j.ConversationID
		j.Message.BotID = j.BotID
		j.Message.Direction = domain.Incoming
		segs, err := decodeSegments(segJSON)
		if err != nil {
			// Poison payload: fail only this job in a single attempt so the rest
			// of the batch keeps flowing instead of wedging the conversation.
			// 坏数据只以单次尝试失败当前任务，不阻塞整批与会话队列。
			poison = append(poison, j)
			continue
		}
		j.Message.Segments = segs
		jobs = append(jobs, j)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("conversation: scan jobs: %w", err)
	}
	rows.Close()
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	for _, bad := range poison {
		if err := r.FailJob(ctx, bad.ID, "bad_message_data", 0, 1, bad.ClaimToken); err != nil {
			return nil, err
		}
	}
	return jobs, nil
}

// CompleteJob marks one claimed job done; the status guard keeps the write from
// clobbering a job another run has already recovered.
// CompleteJob 将已认领任务标记为完成；状态前置条件防止覆盖已被
// 其他运行实例恢复的任务。
func (r *Repo) CompleteJob(ctx context.Context, jobID int64, claim ...string) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if err := r.lockClaim(ctx, tx, jobID, claimValue(claim)); err != nil {
		return err
	}
	result, err := tx.Exec(ctx, `UPDATE message_jobs SET status='done',claim_token=NULL,lease_expires_at=NULL,updated_at=clock_timestamp() WHERE id=$1 AND status='processing' AND claim_token=$2::uuid AND lease_expires_at>clock_timestamp() AND delivery_outcome<>'blocked' AND NOT EXISTS(SELECT 1 FROM agent_outbound_messages o WHERE o.job_id=message_jobs.id AND o.status NOT IN('sent','cancelled'))`, jobID, claimValue(claim))
	if err != nil {
		return err
	}
	if result.RowsAffected() != 1 {
		return ErrLeaseLost
	}
	return tx.Commit(ctx)
}

// FailJob records a failure for a claimed job; jobs are retried with backoff
// until maxAttempts. A job whose outbound ledger already holds a sent or
// unknown record is never retried: replaying it would resend a delivered
// message, so it fails immediately with the partial error code. An in-flight
// sending record counts too, because its outcome is unconfirmed and only
// startup recovery may settle it.
// FailJob 记录已认领任务的失败；任务按退避重试直至 maxAttempts。
// 出站台账已有 sent/unknown 记录的任务绝不重试：重放会重复发送已送达的消息，
// 因此直接以 partial 错误码失败。仍在 sending 的记录同样计入：其结果尚未确认，
// 只能由启动恢复落定。
func (r *Repo) FailJob(ctx context.Context, jobID int64, code string, retryAfter time.Duration, maxAttempts int, claim ...string) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if err := r.lockClaim(ctx, tx, jobID, claimValue(claim)); err != nil {
		return err
	}
	// Close uncertain sends and unused reservations before releasing this claim.
	// 释放认领前，先收拢不确定发送并退还未使用的预留。
	if _, err := tx.Exec(ctx, `WITH cancelled AS (UPDATE agent_outbound_messages SET status='cancelled',updated_at=clock_timestamp() WHERE job_id=$1 AND status='prepared' RETURNING job_id,char_length(text_content) chars),refund AS(SELECT job_id,count(*) messages,sum(chars) chars FROM cancelled GROUP BY job_id) UPDATE agent_run_budgets b SET reserved_messages=GREATEST(0,reserved_messages-r.messages),reserved_chars=GREATEST(0,reserved_chars-r.chars) FROM refund r WHERE b.job_id=r.job_id`, jobID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE agent_outbound_messages SET status='unknown',error_code='finalization_failed',updated_at=clock_timestamp() WHERE job_id=$1 AND status='sending'`, jobID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO messages(conversation_id,bot_id,direction,platform_message_id,sender_id,text_content,sent_status,outbound_id,created_at)
 SELECT o.conversation_id,o.bot_id,'outgoing',o.platform_message_id,o.bot_id,o.text_content,o.status,o.id,o.updated_at
 FROM agent_outbound_messages o JOIN conversations c ON c.id=o.conversation_id
 JOIN LATERAL(SELECT enabled,store_messages FROM scope_policies WHERE bot_id=c.bot_id AND scope_type=c.kind AND scope_id IN(c.target_id,0) ORDER BY scope_id DESC LIMIT 1)p ON p.enabled AND p.store_messages
 WHERE o.job_id=$1 AND o.status IN('sent','unknown')
 ON CONFLICT(outbound_id) WHERE outbound_id IS NOT NULL DO NOTHING`, jobID); err != nil {
		return err
	}

	result, err := tx.Exec(ctx, `
		WITH delivered AS (
			SELECT j.id, EXISTS (
				SELECT 1 FROM agent_outbound_messages o
				WHERE o.job_id = j.id AND o.status IN ('sent', 'unknown', 'sending', 'failed')
			) OR j.delivery_outcome='blocked' AS has_delivered
			FROM message_jobs j
			WHERE j.id = $1 AND j.status = 'processing' AND j.claim_token=$5::uuid AND j.lease_expires_at>clock_timestamp()
		)
		UPDATE message_jobs j SET
			claim_token=NULL,lease_expires_at=NULL,attempts = j.attempts + 1,
			last_error_code = CASE WHEN j.delivery_outcome='blocked' THEN 'blocked' WHEN delivered.has_delivered THEN 'partial' ELSE $2 END,
			status = CASE
				WHEN delivered.has_delivered OR j.attempts + 1 >= $3 THEN 'failed'
				ELSE 'pending'
			END,
			next_run_at = CASE
				WHEN delivered.has_delivered OR j.attempts + 1 >= $3 THEN j.next_run_at
				ELSE now() + make_interval(secs => $4)
			END,
			updated_at = now()
		FROM delivered
		WHERE j.id=delivered.id AND j.status='processing' AND j.claim_token=$5::uuid AND j.lease_expires_at>clock_timestamp()
	`, jobID, code, maxAttempts, retryAfter.Seconds(), claimValue(claim))
	if err != nil {
		return fmt.Errorf("conversation: fail job: %w", err)
	}
	if result.RowsAffected() != 1 {
		return ErrLeaseLost
	}
	return tx.Commit(ctx)
}

// ReleaseJob returns one claimed job to the pending state, e.g. when the worker
// queue for its conversation is full.
// ReleaseJob 将一个已认领任务退回待处理状态，例如其会话队列已满时。
func (r *Repo) ReleaseJob(ctx context.Context, jobID int64, claim ...string) error {
	result, err := r.pool.Exec(ctx,
		`UPDATE message_jobs SET status='pending',claim_token=NULL,lease_expires_at=NULL,updated_at=clock_timestamp() WHERE id=$1 AND status='processing' AND claim_token=$2::uuid AND lease_expires_at>clock_timestamp()`, jobID, claimValue(claim))
	if err != nil {
		return fmt.Errorf("conversation: release job: %w", err)
	}
	if result.RowsAffected() != 1 {
		return ErrLeaseLost
	}
	return nil
}

// RecoverStaleJobs reconciles jobs left in processing by a previous run with
// the outbound ledger: records stuck in sending become unknown, and any job
// with a sent or unknown record fails as partial instead of being requeued,
// because replaying it could resend a delivered message. The remaining jobs
// return to pending.
// RecoverStaleJobs 把上次运行遗留的处理中任务与出站台账对账：
// 停留在 sending 的记录改为 unknown；已有 sent/unknown 记录的任务以 partial
// 失败而不重新排队，避免重放导致重复发送；其余任务回到待处理状态。
func (r *Repo) RecoverStaleJobs(ctx context.Context, policies ...domain.RecoveryPolicy) error {
	policy := domain.RecoveryPolicy{MaxAttempts: 5, RetryBackoff: 5 * time.Second}
	if len(policies) > 0 {
		policy = policies[0]
	}
	if policy.MaxAttempts <= 0 || policy.RetryBackoff < 0 {
		return fmt.Errorf("conversation: invalid recovery policy")
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	rows, err := tx.Query(ctx, `SELECT c.id FROM conversations c WHERE EXISTS(SELECT 1 FROM message_jobs j JOIN messages m ON m.id=j.incoming_message_id WHERE m.conversation_id=c.id AND j.status='processing' AND j.lease_expires_at<=clock_timestamp()) ORDER BY c.id LIMIT 100 FOR UPDATE OF c SKIP LOCKED`)
	if err != nil {
		return err
	}
	ids := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	encoded, _ := json.Marshal(ids)
	_, err = tx.Exec(ctx, `WITH expired AS MATERIALIZED(SELECT j.id FROM message_jobs j JOIN messages m ON m.id=j.incoming_message_id WHERE m.conversation_id IN(SELECT value::uuid FROM jsonb_array_elements_text($1::jsonb)) AND j.status='processing' AND j.lease_expires_at<=clock_timestamp() FOR UPDATE OF j),cancelled AS(UPDATE agent_outbound_messages o SET status='cancelled',updated_at=clock_timestamp() WHERE o.job_id IN(SELECT id FROM expired) AND o.status='prepared' RETURNING job_id,char_length(text_content) chars),refund AS(SELECT job_id,count(*) messages,sum(chars) chars FROM cancelled GROUP BY job_id),budgets AS(UPDATE agent_run_budgets b SET reserved_messages=GREATEST(0,reserved_messages-r.messages),reserved_chars=GREATEST(0,reserved_chars-r.chars) FROM refund r WHERE b.job_id=r.job_id),uncertain AS(UPDATE agent_outbound_messages o SET status='unknown',error_code='lease_expired',updated_at=clock_timestamp() WHERE o.job_id IN(SELECT id FROM expired) AND o.status='sending' RETURNING job_id) UPDATE message_jobs j SET status=CASE WHEN EXISTS(SELECT 1 FROM agent_outbound_messages o WHERE o.job_id=j.id AND o.status IN('sent','unknown','sending','failed')) OR j.delivery_outcome='blocked' OR j.attempts+1>=$2 THEN 'failed' ELSE 'pending' END,last_error_code=CASE WHEN EXISTS(SELECT 1 FROM agent_outbound_messages o WHERE o.job_id=j.id AND o.status IN('sent','unknown','sending','failed')) THEN 'partial' ELSE 'lease_expired' END,attempts=j.attempts+1,next_run_at=clock_timestamp()+make_interval(secs=>$3),claim_token=NULL,lease_expires_at=NULL,updated_at=clock_timestamp() WHERE j.id IN(SELECT id FROM expired)`, encoded, policy.MaxAttempts, policy.RetryBackoff.Seconds())
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// RecordOutgoing persists one bot reply — including the platform message id so
// reply-to-bot detection can match later quotes and the delivery ledger id so
// the logical message and its receipt stay linked — and refreshes the
// conversation activity atomically.
// RecordOutgoing 持久化一条机器人回复（含 platform message id，供后续引用判定匹配；
// 以及投递台账 ID，使逻辑消息与回执保持关联），并在单事务内刷新会话活跃时间。
func (r *Repo) RecordOutgoing(ctx context.Context, conversationID string, botID int64, text string, platformMessageID int64, sentStatus string, outboundID string, claim ...string) error {
	id, err := uuid.Parse(conversationID)
	if err != nil {
		return err
	}
	if outboundID == "" {
		tx, err := r.pool.Client.Tx(ctx)
		if err != nil {
			return err
		}
		defer tx.Rollback()
		b := tx.Client().Message.Create().SetConversationID(id).SetBotID(botID).SetDirection("outgoing").SetSenderID(botID).SetTextContent(text).SetSentStatus(sentStatus)
		if platformMessageID != 0 {
			b.SetPlatformMessageID(platformMessageID)
		}
		if err := b.Exec(ctx); err != nil {
			return err
		}
		if err := tx.Client().Conversation.UpdateOneID(id).SetLastMessageAt(time.Now()).Exec(ctx); err != nil {
			return err
		}
		return tx.Commit()
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var jobID int64
	if err := tx.QueryRow(ctx, `SELECT job_id FROM agent_outbound_messages WHERE id=$1 AND conversation_id=$2 AND bot_id=$3 AND status=$4 AND status IN('sent','failed','unknown') AND claim_token=$5::uuid`, outboundID, conversationID, botID, sentStatus, claimValue(claim)).Scan(&jobID); err != nil {
		return ErrLeaseLost
	}
	if err := r.lockClaim(ctx, tx, jobID, claimValue(claim)); err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO messages(conversation_id,bot_id,direction,platform_message_id,sender_id,text_content,sent_status,outbound_id) SELECT o.conversation_id,o.bot_id,'outgoing',o.platform_message_id,o.bot_id,o.text_content,o.status,o.id FROM agent_outbound_messages o JOIN message_jobs j ON j.id=o.job_id JOIN conversations c ON c.id=o.conversation_id JOIN LATERAL(SELECT enabled,store_messages FROM scope_policies WHERE bot_id=c.bot_id AND scope_type=c.kind AND scope_id IN(c.target_id,0) ORDER BY scope_id DESC LIMIT 1)p ON p.enabled AND p.store_messages WHERE o.id=$1 AND j.status='processing' AND j.claim_token=$2::uuid AND j.lease_expires_at>clock_timestamp() ON CONFLICT(outbound_id) WHERE outbound_id IS NOT NULL DO NOTHING`, outboundID, claimValue(claim))
	if err != nil {
		return err
	}
	result, err := tx.Exec(ctx, `UPDATE conversations SET last_message_at=clock_timestamp() WHERE id=$1 AND EXISTS(SELECT 1 FROM message_jobs WHERE id=$2 AND status='processing' AND claim_token=$3::uuid AND lease_expires_at>clock_timestamp())`, conversationID, jobID, claimValue(claim))
	if err != nil {
		return err
	}
	if result.RowsAffected() != 1 {
		return ErrLeaseLost
	}
	return tx.Commit(ctx)
}

// BotReplyMeta returns the bot's last outgoing message time (zero when it never
// spoke) and its reply count in the last hour for one conversation. It reads
// timestamps and counts only, never message bodies, so callers may use it
// regardless of chat-history authorization.
// BotReplyMeta 返回会话内机器人最近一条出站消息的时间（从未发言为零值）与
// 最近一小时的发言数。仅读取时间戳与计数，不含正文，因此调用方可
// 在任意历史授权配置下使用。
func (r *Repo) BotReplyMeta(ctx context.Context, conversationID string) (time.Time, int, error) {
	var last time.Time
	var count int
	err := r.pool.QueryRow(ctx, `
 WITH activity AS (
 SELECT CASE WHEN status IN ('sent','unknown','sending') THEN updated_at END AS spoke_at,created_at AS reserved_at
 FROM agent_outbound_messages WHERE conversation_id=$1 AND status IN ('prepared','sending','sent','unknown')
 UNION ALL
 SELECT created_at,created_at FROM messages WHERE conversation_id=$1 AND direction='outgoing' AND outbound_id IS NULL AND sent_status IN ('sent','unknown')
 )
 SELECT COALESCE(MAX(spoke_at),'0001-01-01 00:00:00+00'::timestamptz),count(*) FILTER(WHERE reserved_at>now()-interval '1 hour') FROM activity
 `, conversationID).Scan(&last, &count)
	if err != nil {
		return time.Time{}, 0, fmt.Errorf("conversation: bot reply meta: %w", err)
	}
	return last, count, nil
}

// encodeSegments serializes OneBot segments into the stored JSON shape.
// encodeSegments 将 OneBot 消息段序列化为存储 JSON 形态。
func encodeSegments(segs []obdomain.Segment) ([]byte, error) {
	wire := make([]map[string]any, 0, len(segs))
	for _, s := range segs {
		data := make(map[string]string, len(s.Data))
		for k, v := range s.Data {
			data[k] = v
		}
		wire = append(wire, map[string]any{"type": string(s.Type), "data": data})
	}
	b, err := json.Marshal(wire)
	if err != nil {
		return nil, fmt.Errorf("conversation: encode segments: %w", err)
	}
	return b, nil
}

// decodeSegments restores OneBot segments from the stored JSON shape.
// decodeSegments 从存储 JSON 形态还原 OneBot 消息段。
func decodeSegments(b []byte) ([]obdomain.Segment, error) {
	if len(b) == 0 {
		return nil, nil
	}
	var wire []struct {
		Type string            `json:"type"`
		Data map[string]string `json:"data"`
	}
	if err := json.Unmarshal(b, &wire); err != nil {
		return nil, fmt.Errorf("conversation: decode segments: %w", err)
	}
	segs := make([]obdomain.Segment, 0, len(wire))
	for _, w := range wire {
		segs = append(segs, obdomain.Segment{Type: obdomain.SegmentType(w.Type), Data: w.Data})
	}
	return segs, nil
}

// DeliveryError reports terminal delivery problems to the worker.
// DeliveryError 向工作器报告投递终态问题。
func (r *Repo) DeliveryError(ctx context.Context, jobID int64) error {
	var bad bool
	err := r.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM agent_outbound_messages WHERE job_id=$1 AND status NOT IN ('sent','cancelled'))`, jobID).Scan(&bad)
	if err != nil {
		return err
	}
	if bad {
		return fmt.Errorf("conversation: delivery incomplete")
	}
	return nil
}

// PurgeBefore deletes one batch of expired messages. Message jobs reference
// their inbound message without a cascade, so they are removed first; their
// intent decisions, outbound records and run budgets cascade from the job.
// 删除一批过期消息。message_jobs 对入站消息没有级联，因此先删任务；
// 意图决策、出站记录与运行预算再随任务级联删除。
func (r *Repo) PurgeBefore(ctx context.Context, before time.Time, limit int) (int, error) {
	if limit <= 0 {
		return 0, fmt.Errorf("conversation: purge limit must be positive")
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback(ctx)
	rows, err := tx.Query(ctx, `SELECT c.id FROM conversations c WHERE EXISTS(SELECT 1 FROM messages m WHERE m.conversation_id=c.id AND m.created_at<$1) AND NOT EXISTS(SELECT 1 FROM message_jobs j JOIN messages incoming ON incoming.id=j.incoming_message_id WHERE incoming.conversation_id=c.id AND j.status='processing') ORDER BY c.id LIMIT $2 FOR UPDATE OF c SKIP LOCKED`, before, limit)
	if err != nil {
		return 0, err
	}
	conversations := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return 0, err
		}
		conversations = append(conversations, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return 0, err
	}
	encoded, _ := json.Marshal(conversations)
	rows, err = tx.Query(ctx, `SELECT m.id FROM messages m WHERE m.created_at<$1 AND m.conversation_id IN(SELECT value::uuid FROM jsonb_array_elements_text($2::jsonb)) AND NOT EXISTS(SELECT 1 FROM message_jobs j JOIN messages incoming ON incoming.id=j.incoming_message_id WHERE incoming.conversation_id=m.conversation_id AND j.status='processing') ORDER BY m.created_at,m.id LIMIT $3`, before, encoded, limit)
	if err != nil {
		return 0, err
	}
	ids := []int64{}
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return 0, err
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return 0, err
	}
	selected, _ := json.Marshal(ids)
	if _, err := tx.Exec(ctx, `DELETE FROM message_jobs WHERE incoming_message_id IN(SELECT value::bigint FROM jsonb_array_elements_text($1::jsonb))`, selected); err != nil {
		return 0, err
	}
	result, err := tx.Exec(ctx, `DELETE FROM messages WHERE id IN(SELECT value::bigint FROM jsonb_array_elements_text($1::jsonb))`, selected)
	if err != nil {
		return 0, err
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, err
	}
	return int(result.RowsAffected()), nil
}

// ReconcileOutgoing repairs acknowledged message history without sending again.
// ReconcileOutgoing 补齐已有回执的历史记录，不会再次发送。
func (r *Repo) ReconcileOutgoing(ctx context.Context) error {
	_, err := r.pool.Exec(ctx, `INSERT INTO messages(conversation_id,bot_id,direction,platform_message_id,sender_id,text_content,sent_status,outbound_id,created_at)
 SELECT o.conversation_id,o.bot_id,'outgoing',o.platform_message_id,o.bot_id,o.text_content,o.status,o.id,o.updated_at
 FROM agent_outbound_messages o JOIN conversations c ON c.id=o.conversation_id
 JOIN LATERAL (
 SELECT sp.enabled,sp.store_messages FROM scope_policies sp
 WHERE sp.bot_id=c.bot_id AND sp.scope_type=c.kind AND sp.scope_id IN (c.target_id,0)
 ORDER BY sp.scope_id DESC LIMIT 1
 ) p ON true
 WHERE o.status IN ('sent','unknown') AND p.enabled AND p.store_messages
 AND NOT EXISTS(SELECT 1 FROM messages m WHERE m.outbound_id=o.id)
 ON CONFLICT(outbound_id) WHERE outbound_id IS NOT NULL DO NOTHING`)
	return err
}

func messageModel(v *ent.Message) (domain.Message, error) {
	out := domain.Message{ID: v.ID, ConversationID: v.ConversationID.String(), BotID: v.BotID, Direction: domain.Direction(v.Direction), SenderID: v.SenderID, Text: v.TextContent, CreatedAt: v.CreatedAt}
	if v.PlatformMessageID != nil {
		out.PlatformMessageID = *v.PlatformMessageID
	}
	if v.SentStatus != nil {
		out.SentStatus = *v.SentStatus
	}
	segments, err := decodeSegments(v.Segments)
	out.Segments = segments
	return out, err
}
