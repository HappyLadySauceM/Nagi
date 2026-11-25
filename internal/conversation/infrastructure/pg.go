// Package infrastructure implements the conversation repository, ingress handler and worker.
// conversation/infrastructure 实现会话仓库、入站处理器与工作器。
package infrastructure

import (
	"context"
	"database/sql"
	"encoding/json"
	entsql "entgo.io/ent/dialect/sql"
	audit "github.com/HappyLadySauceM/Nagi/internal/audit/contracts"
	media "github.com/HappyLadySauceM/Nagi/internal/media/contracts"
	memorycontracts "github.com/HappyLadySauceM/Nagi/internal/memory/contracts"
	memorydomain "github.com/HappyLadySauceM/Nagi/internal/memory/domain"
	"github.com/HappyLadySauceM/Nagi/pkg/db/ent"
	ec "github.com/HappyLadySauceM/Nagi/pkg/db/ent/conversation"
	em "github.com/HappyLadySauceM/Nagi/pkg/db/ent/message"
	ej "github.com/HappyLadySauceM/Nagi/pkg/db/ent/messagejob"

	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/HappyLadySauceM/Nagi/pkg/db"

	ordering "github.com/HappyLadySauceM/Nagi/internal/conversation/contracts"
	"github.com/HappyLadySauceM/Nagi/internal/conversation/domain"
	obdomain "github.com/HappyLadySauceM/Nagi/internal/onebot/domain"
)

// Repo is the Ent-backed conversation repository.
// Repo 是基于 Ent 的会话仓库。
type Repo struct {
	pool                *db.Pool
	memoryAuthorization MemoryAuthorizationStamper
	attachmentRetention time.Duration
	consentIngress      ConsentIngress
	consentEnabled      bool
}

// MemoryAuthorizationStamper stamps the effective memory authorization event
// inside the message persistence transaction.
// MemoryAuthorizationStamper 在消息持久化事务内写入有效记忆授权事件。
type MemoryAuthorizationStamper interface {
	StampMemoryAuthorization(ctx context.Context, tx *ent.Tx, botID int64, scopeType string, scopeID int64) (int64, bool, error)
}

// RepoOption customizes the conversation repository.
// RepoOption 定制会话仓库。
type RepoOption func(*Repo) error

// WithMemoryAuthorizationStamper binds the memory authorization stamper.
// WithMemoryAuthorizationStamper 绑定记忆授权盖章器。
func WithMemoryAuthorizationStamper(stamper MemoryAuthorizationStamper) RepoOption {
	return func(r *Repo) error {
		if stamper == nil {
			return fmt.Errorf("conversation: memory authorization stamper required")
		}
		r.memoryAuthorization = stamper
		return nil
	}
}

type ConsentIngress interface {
	LockConsentIngress(context.Context, *ent.Tx, int64) error
	LockConsentCleanup(context.Context, *db.Tx, int64) error
	ConsumeStoredConsent(context.Context, *ent.Tx, int64, string, int64, bool) (memorycontracts.ConsentResult, error)
}

// WithConsentIngress consumes control messages in the existing ingress transaction.
// WithConsentIngress 在既有入站事务中处理控制消息。
func WithConsentIngress(repo ConsentIngress, enabled bool) RepoOption {
	return func(r *Repo) error { r.consentIngress = repo; r.consentEnabled = enabled; return nil }
}

// WithInboundAttachments records archive work in the ingress transaction.
func WithInboundAttachments(retention time.Duration) RepoOption {
	return func(r *Repo) error {
		if retention <= 0 {
			return fmt.Errorf("conversation: attachment retention must be positive")
		}
		r.attachmentRetention = retention
		return nil
	}
}

// NewRepo builds the repository; the pool must not be nil.
// NewRepo 构建仓库；pool 不能为 nil。
func NewRepo(pool *db.Pool, opts ...RepoOption) (*Repo, error) {
	if pool == nil {
		return nil, fmt.Errorf("conversation: pool required")
	}
	r := &Repo{pool: pool}
	for _, opt := range opts {
		if err := opt(r); err != nil {
			return nil, err
		}
	}
	return r, nil
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
	if in.ConsentControl && in.Kind == domain.KindGroup && r.consentIngress != nil {
		// Lock order is per-account mutation, then conversation, then message.
		// 锁顺序为账号变更锁、会话锁、消息锁。
		if err := r.consentIngress.LockConsentIngress(ctx, tx, in.BotID); err != nil {
			return domain.IngestResult{}, err
		}
	}
	if _, err := c.ExecContext(ctx, `INSERT INTO bot_accounts (bot_id) VALUES ($1) ON CONFLICT (bot_id) DO NOTHING`, in.BotID); err != nil {
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
	existing, err := c.Message.Query().Where(em.BotID(in.BotID), em.ConversationID(conv), em.PlatformMessageID(in.PlatformMessageID), em.Direction("incoming")).Only(ctx)
	if err != nil && !ent.IsNotFound(err) {
		return domain.IngestResult{}, err
	}
	if existing != nil {
		result := domain.IngestResult{ConversationID: conv.String(), Duplicate: true}
		if err := tx.Commit(); err != nil {
			return domain.IngestResult{}, err
		}
		return result, nil
	}
	var authorizationEventID *int64
	if r.memoryAuthorization != nil && !in.ConsentControl {
		id, authorized, err := r.memoryAuthorization.StampMemoryAuthorization(ctx, tx, in.BotID, string(in.Kind), in.TargetID)
		if err != nil {
			return domain.IngestResult{}, err
		}
		if authorized && id > 0 {
			authorizationEventID = &id
		}
	}
	summary, err := json.Marshal(media.InboundPictures(in.Segments, r.attachmentRetention > 0))
	if err != nil {
		return domain.IngestResult{}, err
	}
	messageCreate := c.Message.Create().SetInboundMediaSummary(summary).SetConversationID(conv).SetBotID(in.BotID).SetDirection("incoming").SetPlatformMessageID(in.PlatformMessageID).SetSenderID(in.SenderID).SetTextContent(in.Text).SetSegments(segments)
	if in.ConsentControl {
		messageCreate.SetControlKind(memorydomain.ConsentCommandKind)
	}
	if authorizationEventID != nil {
		messageCreate.SetMemoryAuthorizationEventID(*authorizationEventID)
	}
	stored, err := messageCreate.Save(ctx)
	if err != nil {
		return domain.IngestResult{}, err
	}
	message := stored.ID
	if in.ConsentControl {
		if in.Kind == domain.KindGroup && r.consentIngress != nil {
			if _, err = r.consentIngress.ConsumeStoredConsent(ctx, tx, in.BotID, conv.String(), message, r.consentEnabled); err != nil {
				return domain.IngestResult{}, err
			}
		}
		if err = tx.Commit(); err != nil {
			return domain.IngestResult{}, err
		}
		return domain.IngestResult{ConversationID: conv.String(), MessageID: message}, nil
	}
	if r.attachmentRetention > 0 {
		if err := registerInboundAttachments(ctx, c, message, stored.CreatedAt.Add(r.attachmentRetention), in.Segments); err != nil {
			return domain.IngestResult{}, err
		}
	}

	jobCreate := c.MessageJob.Create().SetIncomingMessageID(message).SetTraceparent(in.TraceParent).SetTracestate(in.TraceState)
	if in.ArchiveOnly {
		jobCreate.SetStatus("done").SetLastErrorCode("archive_only")
	}
	job, err := jobCreate.Save(ctx)
	if err != nil {
		return domain.IngestResult{}, err
	}
	if in.Kind == domain.KindGroup && !in.ArchiveOnly && in.SenderID > 0 && in.SenderID != in.BotID && in.PlatformMessageID != 0 {
		if err := registerTopicRoute(ctx, c, conv, message); err != nil {
			return domain.IngestResult{}, fmt.Errorf("conversation: register topic route: %w", err)
		}
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
	query := r.pool.Client.Message.Query().Where(em.ConversationID(id), em.ControlKindEQ(""))
	return recentMessages(ctx, query, limit)
}

func (r *Repo) RecentGroupMessagesBefore(ctx context.Context, botID int64, conversationID string, rowID int64, at time.Time, window time.Duration, limit int) ([]domain.Message, error) {
	id, err := uuid.Parse(conversationID)
	if err != nil {
		return nil, err
	}
	query := r.pool.Client.Message.Query().Where(em.ConversationID(id), em.BotID(botID), em.ControlKindEQ(""),
		em.Or(em.DirectionEQ(string(domain.Incoming)), em.SentStatusEQ("sent")))
	if rowID > 0 {
		anchor, err := r.pool.Client.Message.Query().Where(em.ID(rowID), em.BotID(botID), em.ConversationID(id), em.DirectionEQ(string(domain.Incoming))).Only(ctx)
		if err != nil {
			return nil, err
		}
		at = anchor.CreatedAt
		query.Where(em.IDLT(anchor.ID), em.CreatedAtLTE(at))
	} else {
		query.Where(em.CreatedAtLT(at))
	}
	return recentMessages(ctx, query.Where(em.CreatedAtGTE(at.Add(-window))), limit)
}

func recentMessages(ctx context.Context, query *ent.MessageQuery, limit int) ([]domain.Message, error) {
	rows, err := query.Order(ent.Desc(em.FieldCreatedAt, em.FieldID)).Limit(limit).All(ctx)
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

// JobIngress reads the inbound evidence for an account-scoped job.
// JobIngress 读取限于指定账号的任务入站证据。
func (r *Repo) JobIngress(ctx context.Context, botID, jobID int64) (*domain.JobIngress, error) {
	job, err := r.pool.Client.MessageJob.Query().
		Where(ej.ID(jobID), ej.HasIncomingMessageIDRefWith(em.BotID(botID), em.DirectionEQ(string(domain.Incoming)))).
		WithIncomingMessageIDRef(func(query *ent.MessageQuery) { query.WithConversationIDRef() }).Only(ctx)
	if ent.IsNotFound(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	row, err := job.Edges.IncomingMessageIDRefOrErr()
	if ent.IsNotFound(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	message, err := messageModel(row)
	if err != nil {
		return nil, err
	}
	conversation, err := row.Edges.ConversationIDRefOrErr()
	if err != nil {
		return nil, err
	}
	return &domain.JobIngress{
		Kind: domain.Kind(conversation.Kind), TargetID: conversation.TargetID,
		Message: message, TraceParent: job.Traceparent, Status: job.Status,
	}, nil
}

// MessageByID returns one stored message scoped to its bot account. A missing
// row is reported as not found, which is also the observable result after the
// retention sweep deletes an expired message.
// MessageByID 返回属于指定机器人账号的一条已存消息。行不存在返回未找到，
// 这也是留存清理删除过期消息后的可观察结果。
func (r *Repo) MessageByID(ctx context.Context, botID, messageID int64) (domain.SourceMessage, bool, error) {
	v, err := r.pool.Client.Message.Query().Where(em.ID(messageID), em.BotID(botID), em.ControlKindEQ("")).Only(ctx)
	if ent.IsNotFound(err) {
		return domain.SourceMessage{}, false, nil
	}
	if err != nil {
		return domain.SourceMessage{}, false, err
	}
	return domain.SourceMessage{ID: v.ID, ConversationID: v.ConversationID.String(), BotID: v.BotID, Text: v.TextContent, CreatedAt: v.CreatedAt}, true, nil
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
	const readyJob = `SELECT j.id FROM message_jobs j
 LEFT JOIN messages m ON m.id=j.incoming_message_id
 LEFT JOIN memory_consent_proposals p ON p.id=j.consent_proposal_id
 LEFT JOIN LATERAL (` + ordering.GroupPendingHead + `) head ON c.kind='group' AND j.source_kind='incoming'
 WHERE COALESCE(m.conversation_id,p.source_conversation_id)=c.id AND j.status='pending' AND NOT j.delivery_continuation
 AND (c.kind<>'group' OR j.source_kind<>'incoming' OR j.id=head.id)
 AND NOT EXISTS (SELECT 1 FROM group_topic_routes route WHERE route.id=m.id AND route.status IN ('pending','routing')
  AND route.deadline>clock_timestamp() AND EXISTS(SELECT 1 FROM group_topic_route_candidates candidate WHERE candidate.route_id=route.id))
 AND j.next_run_at<=clock_timestamp()`
	const noBusyJob = `NOT EXISTS (SELECT 1 FROM message_jobs busy LEFT JOIN messages bm ON bm.id=busy.incoming_message_id
 LEFT JOIN memory_consent_proposals bp ON bp.id=busy.consent_proposal_id
 WHERE COALESCE(bm.conversation_id,bp.source_conversation_id)=c.id AND busy.status='processing')`
	locked, err := tx.Query(ctx, `SELECT c.id FROM conversations c WHERE EXISTS (`+readyJob+`) AND `+noBusyJob+`
 ORDER BY c.id LIMIT $1 FOR UPDATE OF c SKIP LOCKED`, limit)
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
 JOIN LATERAL (`+readyJob+` ORDER BY j.next_run_at,j.id LIMIT 1) chosen ON true
 JOIN message_jobs j ON j.id=chosen.id
 WHERE c.id IN(SELECT value::uuid FROM jsonb_array_elements_text($1::jsonb)) AND `+noBusyJob+`
 FOR UPDATE OF j SKIP LOCKED
 ),claimed AS(
 UPDATE message_jobs j SET status='processing',claim_token=gen_random_uuid(),lease_expires_at=clock_timestamp()+interval '2 minutes',updated_at=clock_timestamp() FROM picked WHERE j.id=picked.id AND j.status='pending'
 RETURNING j.id,j.incoming_message_id,j.traceparent,j.tracestate,j.claim_token,j.lease_expires_at,j.consent_proposal_id
 ) SELECT cl.id,COALESCE(cl.incoming_message_id,0),cl.traceparent,cl.tracestate,c.id,c.bot_id,c.kind,c.target_id,COALESCE(m.id,0),COALESCE(m.platform_message_id,0),COALESCE(m.sender_id,0),COALESCE(m.text_content,''),COALESCE(m.segments,'[]'::jsonb),COALESCE(m.created_at,p.created_at),cl.claim_token,cl.lease_expires_at,COALESCE(p.id::text,'')
 FROM claimed cl LEFT JOIN messages m ON m.id=cl.incoming_message_id LEFT JOIN memory_consent_proposals p ON p.id=cl.consent_proposal_id JOIN conversations c ON c.id=COALESCE(m.conversation_id,p.source_conversation_id) ORDER BY cl.id`, encoded)
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
			&segJSON, &j.Message.CreatedAt, &j.ClaimToken, &j.LeaseExpiresAt, &j.ConsentProposalID); err != nil {
			return nil, fmt.Errorf("conversation: scan job: %w", err)
		}
		j.Kind = domain.Kind(kindStr)
		j.Message.ConversationID = j.ConversationID
		j.Message.BotID = j.BotID
		if j.ConsentProposalID == "" {
			j.Message.Direction = domain.Incoming
		}
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
	for {
		err := r.failJob(ctx, jobID, code, retryAfter, maxAttempts, claimValue(claim))
		if err != errFailureScopeChanged {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
	}
}

func (r *Repo) failJob(ctx context.Context, jobID int64, code string, retryAfter time.Duration, maxAttempts int, claim any) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	lockedConversations, err := r.lockFailure(ctx, tx, jobID, claim)
	if err != nil {
		return err
	}
	lockedTargets, _ := json.Marshal(lockedConversations)
	if _, err := tx.Exec(ctx, `SELECT id FROM agent_outbound_messages WHERE job_id=$1 AND status IN ('prepared','sending') ORDER BY id FOR UPDATE`, jobID); err != nil {
		return err
	}
	// Close uncertain sends and unused reservations before releasing this claim.
	// 释放认领前，先收拢不确定发送并退还未使用的预留。
	if _, err := tx.Exec(ctx, `WITH cancelled AS (UPDATE agent_outbound_messages SET status='cancelled',updated_at=clock_timestamp() WHERE job_id=$1 AND status='prepared' RETURNING job_id,charge_chars chars),refund AS(SELECT job_id,count(*) messages,sum(chars) chars FROM cancelled GROUP BY job_id) UPDATE agent_run_budgets b SET reserved_messages=GREATEST(0,reserved_messages-r.messages),reserved_chars=GREATEST(0,reserved_chars-r.chars) FROM refund r WHERE b.job_id=r.job_id`, jobID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE agent_outbound_messages SET status=CASE WHEN platform_message_id IS NULL THEN 'unknown' ELSE 'sent' END,error_code=CASE WHEN platform_message_id IS NULL THEN 'finalization_failed' END,updated_at=clock_timestamp() WHERE job_id=$1 AND status='sending'`, jobID); err != nil {
		return err
	}
	// History follows the actual target, matching Settle; a cross-conversation
	// message must never be filed under the source conversation.
	// 历史归属实际目标，与 Settle 一致；跨会话消息不得记到来源会话名下。
	if _, err := tx.Exec(ctx, `INSERT INTO messages(conversation_id,bot_id,direction,platform_message_id,sender_id,text_content,segments,sent_status,outbound_id,created_at,control_kind)
 SELECT c.id,o.bot_id,'outgoing',o.platform_message_id,o.bot_id,o.text_content,COALESCE(o.payload->'segments','[]'::jsonb),o.status,o.id,o.updated_at,CASE WHEN j.source_kind='memory_consent_prompt' THEN j.source_kind WHEN o.payload @> '{"cross_group_fact":true}'::jsonb THEN 'memory_consent_reply' ELSE '' END
 FROM agent_outbound_messages o JOIN message_jobs j ON j.id=o.job_id JOIN conversations c ON c.bot_id=o.bot_id AND c.kind=o.target_kind AND c.target_id=o.target_id
 JOIN LATERAL(SELECT enabled,store_messages FROM scope_policies WHERE bot_id=c.bot_id AND scope_type=c.kind AND scope_id IN(c.target_id,0) ORDER BY scope_id DESC LIMIT 1)p ON p.enabled AND p.store_messages
 WHERE o.job_id=$1 AND o.status IN('sent','unknown')
 AND c.id IN (SELECT value::uuid FROM jsonb_array_elements_text($2::jsonb))
 ON CONFLICT(outbound_id) WHERE outbound_id IS NOT NULL DO NOTHING`, jobID, lockedTargets); err != nil {
		return err
	}

	result, err := tx.Exec(ctx, `
		WITH delivered AS (
			SELECT j.id, EXISTS (
				SELECT 1 FROM agent_outbound_messages o
				WHERE o.job_id = j.id AND o.status IN ('sent', 'unknown', 'sending', 'failed')
			) OR j.delivery_outcome='blocked' AS has_delivered,
			j.source_kind='incoming' AND EXISTS (
				SELECT 1 FROM intent_decision_runs r WHERE r.job_id=j.id AND r.state='completed'
				AND r.decision_id IS NOT NULL AND (r.claim_token=j.claim_token OR j.last_error_code='decision_resume')
			) AS decided
			FROM message_jobs j
			WHERE j.id = $1 AND j.status = 'processing' AND j.claim_token=$5::uuid AND j.lease_expires_at>clock_timestamp()
		)
		UPDATE message_jobs j SET
			claim_token=NULL,lease_expires_at=NULL,attempts = j.attempts + 1,
			last_error_code = CASE WHEN j.delivery_outcome='blocked' THEN 'blocked' WHEN delivered.has_delivered THEN 'partial' ELSE $2 END,
			status = CASE
				WHEN delivered.has_delivered OR delivered.decided OR j.attempts + 1 >= $3 THEN 'failed'
				ELSE 'pending'
			END,
			next_run_at = CASE
				WHEN delivered.has_delivered OR delivered.decided OR j.attempts + 1 >= $3 THEN j.next_run_at
				ELSE now() + make_interval(secs => $4)
			END,
			updated_at = now()
		FROM delivered
		WHERE j.id=delivered.id AND j.status='processing' AND j.claim_token=$5::uuid AND j.lease_expires_at>clock_timestamp()
	`, jobID, code, maxAttempts, retryAfter.Seconds(), claim)
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
	rows, err := tx.Query(ctx, `SELECT c.id FROM conversations c WHERE EXISTS(SELECT 1 FROM message_jobs j LEFT JOIN messages m ON m.id=j.incoming_message_id LEFT JOIN memory_consent_proposals p ON p.id=j.consent_proposal_id WHERE COALESCE(m.conversation_id,p.source_conversation_id)=c.id AND j.status='processing' AND NOT j.delivery_continuation AND j.lease_expires_at<=clock_timestamp()) ORDER BY c.id LIMIT 100 FOR UPDATE OF c SKIP LOCKED`)
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
	rows, err = tx.Query(ctx, `SELECT j.id FROM message_jobs j LEFT JOIN messages m ON m.id=j.incoming_message_id LEFT JOIN memory_consent_proposals p ON p.id=j.consent_proposal_id
 WHERE COALESCE(m.conversation_id,p.source_conversation_id) IN (SELECT value::uuid FROM jsonb_array_elements_text($1::jsonb))
 AND j.status='processing' AND NOT j.delivery_continuation AND j.lease_expires_at<=clock_timestamp() ORDER BY j.id FOR UPDATE OF j`, encoded)
	if err != nil {
		return err
	}
	jobs := []int64{}
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		jobs = append(jobs, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	if len(jobs) == 0 {
		return tx.Commit(ctx)
	}
	encoded, _ = json.Marshal(jobs)
	if _, err := tx.Exec(ctx, `SELECT id FROM agent_outbound_messages WHERE job_id IN (
 SELECT value::bigint FROM jsonb_array_elements_text($1::jsonb)) AND status IN ('prepared','sending','cancelled') ORDER BY id FOR UPDATE`, encoded); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `SELECT job_id FROM agent_run_budgets WHERE job_id IN (
 SELECT value::bigint FROM jsonb_array_elements_text($1::jsonb)) ORDER BY job_id FOR UPDATE`, encoded); err != nil {
		return err
	}
	_, err = tx.Exec(ctx, recoverExpiredJobsSQL, encoded, policy.MaxAttempts, policy.RetryBackoff.Seconds(), audit.ModelMediaStartFailureCode)
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
	_, err = tx.Exec(ctx, `INSERT INTO messages(conversation_id,bot_id,direction,platform_message_id,sender_id,text_content,sent_status,outbound_id,segments) SELECT o.conversation_id,o.bot_id,'outgoing',o.platform_message_id,o.bot_id,o.text_content,o.status,o.id,CASE WHEN o.action='qq.send_voice_current' THEN jsonb_build_array(jsonb_build_object('type','record','data',jsonb_build_object('file','outbound:'||o.id::text))) ELSE '[]'::jsonb END FROM agent_outbound_messages o JOIN message_jobs j ON j.id=o.job_id JOIN conversations c ON c.id=o.conversation_id JOIN LATERAL(SELECT enabled,store_messages FROM scope_policies WHERE bot_id=c.bot_id AND scope_type=c.kind AND scope_id IN(c.target_id,0) ORDER BY scope_id DESC LIMIT 1)p ON p.enabled AND p.store_messages WHERE o.id=$1 AND j.status='processing' AND j.claim_token=$2::uuid AND j.lease_expires_at>clock_timestamp() AND NOT (o.payload @> '{"cross_group_fact":true}'::jsonb) ON CONFLICT(outbound_id) WHERE outbound_id IS NOT NULL DO NOTHING`, outboundID, claimValue(claim))
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
			if k == media.TranscriptMarker {
				continue
			}
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
		Type string                     `json:"type"`
		Data map[string]json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(b, &wire); err != nil {
		return nil, fmt.Errorf("conversation: decode segments: %w", err)
	}
	segs := make([]obdomain.Segment, 0, len(wire))
	for _, w := range wire {
		data := make(map[string]string, len(w.Data))
		for k, raw := range w.Data {
			var text string
			if err := json.Unmarshal(raw, &text); err == nil {
				data[k] = text
				continue
			}
			// Composite values such as node.content arrive either as JSON text
			// (inbound encoder) or as arrays (outbound wire payload); store the
			// text form so both paths decode into the same segment shape.
			// node.content 等复合值可能以 JSON 文本（入站）或数组（出站线上载荷）
			// 形式写入；统一保留文本形态，两条写入路径因此解码一致。
			data[k] = string(raw)
		}
		segs = append(segs, obdomain.Segment{Type: obdomain.SegmentType(w.Type), Data: data})
	}
	return segs, nil
}

// DeliveryError reports terminal delivery problems to the worker.
// DeliveryError 向工作器报告投递终态问题。
func (r *Repo) DeliveryError(ctx context.Context, jobID int64) error {
	var bad bool
	err := r.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM agent_outbound_messages WHERE job_id=$1 AND status NOT IN ('sent','cancelled')) OR EXISTS(SELECT 1 FROM message_jobs WHERE id=$1 AND delivery_outcome='blocked')`, jobID).Scan(&bad)
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
	botRows, err := tx.Query(ctx, `SELECT DISTINCT c.bot_id FROM conversations c JOIN (
 SELECT c.id FROM conversations c WHERE EXISTS(SELECT 1 FROM messages m WHERE m.conversation_id=c.id AND m.created_at<$1)
 AND NOT EXISTS(SELECT 1 FROM message_jobs j LEFT JOIN messages incoming ON incoming.id=j.incoming_message_id LEFT JOIN memory_consent_proposals p ON p.id=j.consent_proposal_id WHERE COALESCE(incoming.conversation_id,p.source_conversation_id)=c.id AND j.status='processing')
 ORDER BY c.id LIMIT $2
 ) eligible ON eligible.id=c.id ORDER BY c.bot_id`, before, limit)
	if err != nil {
		return 0, err
	}
	bots := []int64{}
	for botRows.Next() {
		var bot int64
		if err := botRows.Scan(&bot); err != nil {
			botRows.Close()
			return 0, err
		}
		bots = append(bots, bot)
	}
	err = botRows.Err()
	botRows.Close()
	if err != nil {
		return 0, err
	}
	if r.consentIngress != nil {
		for _, bot := range bots {
			if err := r.consentIngress.LockConsentCleanup(ctx, tx, bot); err != nil {
				return 0, err
			}
		}
	}
	accounts, _ := json.Marshal(bots)
	rows, err := tx.Query(ctx, `SELECT c.id FROM conversations c WHERE c.bot_id IN(SELECT value::bigint FROM jsonb_array_elements_text($3::jsonb)) AND EXISTS(SELECT 1 FROM messages m WHERE m.conversation_id=c.id AND m.created_at<$1) AND NOT EXISTS(SELECT 1 FROM message_jobs j LEFT JOIN messages incoming ON incoming.id=j.incoming_message_id LEFT JOIN memory_consent_proposals p ON p.id=j.consent_proposal_id WHERE COALESCE(incoming.conversation_id,p.source_conversation_id)=c.id AND j.status='processing') ORDER BY c.id LIMIT $2 FOR UPDATE OF c SKIP LOCKED`, before, limit, accounts)
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
	rows, err = tx.Query(ctx, `SELECT m.id FROM messages m WHERE m.created_at<$1 AND m.conversation_id IN(SELECT value::uuid FROM jsonb_array_elements_text($2::jsonb)) AND NOT EXISTS(SELECT 1 FROM message_jobs j LEFT JOIN messages incoming ON incoming.id=j.incoming_message_id LEFT JOIN memory_consent_proposals p ON p.id=j.consent_proposal_id WHERE COALESCE(incoming.conversation_id,p.source_conversation_id)=m.conversation_id AND j.status='processing') ORDER BY m.created_at,m.id LIMIT $3`, before, encoded, limit)
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
	// Invalidate retained views and advance the resume floor before cascading deletion.
	// 级联删除前使留存视图失效，并推进续传下界。
	if _, err := tx.Exec(ctx, `WITH lost AS (
	 SELECT e.bot_id,MAX(e.position) AS pos FROM intent_run_events e JOIN intent_decision_runs r ON r.id=e.run_id
	 JOIN message_jobs j ON j.id=r.job_id WHERE j.incoming_message_id IN(SELECT value::bigint FROM jsonb_array_elements_text($1::jsonb)) GROUP BY e.bot_id
	), accounts AS (
	 SELECT DISTINCT bot_id FROM messages WHERE id IN(SELECT value::bigint FROM jsonb_array_elements_text($1::jsonb))
	), changed AS (
	 INSERT INTO intent_stream_heads(bot_id,position,retained_after,retention_revision)
	 SELECT a.bot_id,COALESCE(l.pos,0),COALESCE(l.pos,0),1 FROM accounts a LEFT JOIN lost l USING(bot_id) ORDER BY a.bot_id
	 ON CONFLICT(bot_id) DO UPDATE SET retained_after=GREATEST(intent_stream_heads.retained_after,EXCLUDED.retained_after),
	 retention_revision=intent_stream_heads.retention_revision+1 RETURNING bot_id
	) SELECT pg_notify('intent_live',bot_id::text) FROM changed`, selected); err != nil {
		return 0, err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM message_jobs WHERE incoming_message_id IN(SELECT value::bigint FROM jsonb_array_elements_text($1::jsonb))`, selected); err != nil {
		return 0, err
	}
	// Close affected versions before dropping source evidence. Confirmation and
	// prompt deletion are checked directly by the consent read predicate.
	// 删除原事实来源前先关闭受影响版本。确认和提示凭据由读取谓词直接校验。
	if _, err := tx.Exec(ctx, `WITH changed AS (
  UPDATE memories SET authorization_version=authorization_version+1,updated_at=clock_timestamp()
  WHERE source_message_id IN (SELECT value::bigint FROM jsonb_array_elements_text($1::jsonb)) RETURNING memory_id
 ), invalid AS (
  UPDATE memory_consent_proposals p SET status='invalidated',state_version=state_version+1
  WHERE p.memory_id IN(SELECT memory_id FROM changed) AND p.status IN('pending','confirmed') RETURNING id
 ) UPDATE memory_consent_grants g SET revoked_at=clock_timestamp(),revocation_source='source_removed',state_version=g.state_version+1
 WHERE g.proposal_id IN(SELECT id FROM invalid) AND g.revoked_at IS NULL`, selected); err != nil {
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
	_, err := r.pool.Exec(ctx, `INSERT INTO messages(conversation_id,bot_id,direction,platform_message_id,sender_id,text_content,segments,sent_status,outbound_id,created_at)
 SELECT c.id,o.bot_id,'outgoing',o.platform_message_id,o.bot_id,o.text_content,COALESCE(o.payload->'segments','[]'::jsonb),o.status,o.id,o.updated_at
 FROM agent_outbound_messages o JOIN conversations c ON c.bot_id=o.bot_id AND c.kind=o.target_kind AND c.target_id=o.target_id
 JOIN LATERAL (
 SELECT sp.enabled,sp.store_messages FROM scope_policies sp
 WHERE sp.bot_id=c.bot_id AND sp.scope_type=c.kind AND sp.scope_id IN (c.target_id,0)
 ORDER BY sp.scope_id DESC LIMIT 1
 ) p ON true
 WHERE o.tool_call_id IS DISTINCT FROM 'memory_consent_prompt' AND NOT (o.payload @> '{"cross_group_fact":true}'::jsonb) AND o.status IN ('sent','unknown') AND p.enabled AND p.store_messages
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

// SaveTranscript persists derived text only with an active lease and current storage authorization.
// SaveTranscript 仅在租约和当前存储授权均有效时保存衍生文本。
func (r *Repo) SaveTranscript(ctx context.Context, jobID int64, claim, text string) error {
	res, err := r.pool.Exec(ctx, `UPDATE messages m SET text_content=$3,
 segments=(SELECT COALESCE(jsonb_agg(CASE WHEN s.value->>'type'='record' THEN jsonb_set(s.value,'{data}',COALESCE(s.value->'data','{}'::jsonb)||jsonb_build_object($4::text,'true')) ELSE s.value END ORDER BY s.ordinality),'[]'::jsonb) FROM jsonb_array_elements(m.segments) WITH ORDINALITY AS s(value,ordinality))
 FROM message_jobs j
 WHERE j.id=$1 AND j.incoming_message_id=m.id AND j.claim_token=$2::uuid
 AND j.status='processing' AND j.lease_expires_at>clock_timestamp()
 AND EXISTS (
   SELECT 1 FROM conversations c
   JOIN LATERAL (
     SELECT enabled,store_messages,reply_mode,tool_permissions FROM scope_policies
     WHERE bot_id=c.bot_id AND scope_type=c.kind AND scope_id IN(c.target_id,0)
     ORDER BY scope_id DESC LIMIT 1
   ) p ON p.enabled AND p.store_messages AND p.reply_mode<>'observe_only' AND COALESCE((p.tool_permissions->>'media.voice.transcribe')::boolean,true)
   WHERE c.id=m.conversation_id
 )`, jobID, claim, text, media.TranscriptMarker)
	if err != nil {
		return err
	}
	if res.RowsAffected() != 1 {
		return fmt.Errorf("transcript: lease or authorization lost")
	}
	return nil
}

const ordinaryMessage = ` FROM messages m JOIN conversations c ON c.id=m.conversation_id AND c.bot_id=m.bot_id
 JOIN LATERAL (SELECT enabled,store_messages,allow_history_read FROM scope_policies
 WHERE bot_id=c.bot_id AND scope_type=c.kind AND scope_id IN(c.target_id,0) ORDER BY scope_id DESC LIMIT 1) policy
 ON policy.enabled AND policy.store_messages
 WHERE m.bot_id=$1 AND m.conversation_id=$2::uuid AND m.platform_message_id=$3 AND m.platform_message_id<>0
 AND m.direction='incoming' AND m.control_kind='' AND m.sender_id>0 AND m.sender_id<>m.bot_id
 AND NOT EXISTS(SELECT 1 FROM messages conflicting WHERE conflicting.bot_id=m.bot_id AND conflicting.conversation_id=m.conversation_id AND conflicting.platform_message_id=m.platform_message_id AND conflicting.id<>m.id)`

// StoredPublicMessage returns only directly stored text without resolving platform references.
// StoredPublicMessage 只返回直接入库的文字，不展开平台引用。
func (r *Repo) StoredPublicMessage(ctx context.Context, bot int64, conversation string, id int64) (map[string]any, error) {
	var kind string
	var sender int64
	var segments []byte
	var created time.Time
	err := r.pool.QueryRow(ctx, `SELECT c.kind,m.sender_id,m.segments,m.created_at`+ordinaryMessage+`
 AND policy.allow_history_read
 AND NOT EXISTS(SELECT 1 FROM jsonb_array_elements(m.segments) seg WHERE seg->>'type' IS DISTINCT FROM 'text')`, bot, conversation, id).Scan(&kind, &sender, &segments, &created)
	if err != nil {
		return nil, err
	}
	if len(segments) == 0 || string(segments) == "null" {
		return nil, sql.ErrNoRows
	}
	return map[string]any{"message_id": id, "message_type": kind, "sender": map[string]int64{"user_id": sender}, "message": json.RawMessage(segments), "time": created.Unix()}, nil
}

// HistoryImages returns persisted text and image segments of an authorized ordinary history message.
// HistoryImages 返回当前允许读取的普通历史消息中已持久化的文字和图片段。
func (r *Repo) HistoryImages(ctx context.Context, bot int64, conversation, kind string, target, id int64) ([]obdomain.Segment, error) {
	var data []byte
	err := r.pool.QueryRow(ctx, `SELECT (SELECT COALESCE(jsonb_agg(seg.value ORDER BY seg.ordinality),'[]'::jsonb)
 FROM jsonb_array_elements(m.segments) WITH ORDINALITY AS seg(value,ordinality)
 WHERE seg.value->>'type' IN ('text','image'))`+ordinaryMessage+`
 AND policy.allow_history_read AND c.kind=$4 AND c.target_id=$5
 AND EXISTS(SELECT 1 FROM jsonb_array_elements(m.segments) seg WHERE seg->>'type'='image')`, bot, conversation, id, kind, target).Scan(&data)
	if err != nil {
		return nil, err
	}
	return obdomain.SegmentsFromWire(data)
}

// MediaAttachment returns only an incoming message in the bound account and conversation.
// MediaAttachment 仅返回绑定账号和会话内的入站消息。
func (r *Repo) MediaAttachment(ctx context.Context, bot int64, conversation string, id int64) ([]obdomain.Segment, error) {
	var data []byte
	err := r.pool.QueryRow(ctx, `SELECT m.segments`+ordinaryMessage+` AND (policy.allow_history_read OR EXISTS(SELECT 1 FROM message_jobs WHERE id=$4 AND incoming_message_id=m.id AND claim_token::text=$5 AND status='processing' AND lease_expires_at>clock_timestamp()))`, bot, conversation, id, memorycontracts.ReplyJobFrom(ctx).ID, memorycontracts.ReplyJobFrom(ctx).Claim).Scan(&data)
	if err != nil {
		return nil, err
	}
	return obdomain.SegmentsFromWire(data)
}

// HistoryIndexBatch scans source rows in ID order for the rotating index rebuild.
// HistoryIndexBatch 按 ID 正序扫描原始消息，供轮转重建搜索索引。
func (r *Repo) HistoryIndexBatch(ctx context.Context, afterID int64, limit int) ([]domain.IndexedMessage, error) {
	rows, err := r.pool.Client.Message.Query().Where(em.IDGT(afterID), em.ControlKindEQ("")).
		Order(ent.Asc(em.FieldID)).Limit(limit).WithConversationIDRef().All(ctx)
	if err != nil {
		return nil, err
	}
	result := make([]domain.IndexedMessage, 0, len(rows))
	for _, row := range rows {
		conversation, err := row.Edges.ConversationIDRefOrErr()
		if err != nil {
			return nil, err
		}
		message, err := messageModel(row)
		if err != nil {
			return nil, err
		}
		result = append(result, domain.IndexedMessage{Message: message, Kind: domain.Kind(conversation.Kind), TargetID: conversation.TargetID})
	}
	return result, nil
}

// HistoryMessages resolves ranked candidates in the exact source conversation.
// HistoryMessages 在确切的来源会话中读取检索候选。
func (r *Repo) HistoryMessages(ctx context.Context, search domain.HistorySearch, ids []int64) ([]domain.Message, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	conversation, err := uuid.Parse(search.ConversationID)
	if err != nil {
		return nil, err
	}
	query := r.pool.Client.Message.Query().Where(
		em.IDIn(ids...), em.BotID(search.BotID), em.ConversationID(conversation), em.ControlKindEQ(""),
		em.Or(em.CreatedAtLT(search.Before), em.And(em.CreatedAtEQ(search.Before), em.IDLT(search.BeforeID))),
	)
	if search.SenderID > 0 {
		query.Where(em.SenderID(search.SenderID))
	}
	if !search.After.IsZero() {
		query.Where(em.CreatedAtGTE(search.After))
	}
	rows, err := query.All(ctx)
	if err != nil {
		return nil, err
	}
	byID := make(map[int64]*ent.Message, len(rows))
	for _, row := range rows {
		byID[row.ID] = row
	}
	messages := make([]domain.Message, 0, len(rows))
	for _, id := range ids {
		if row := byID[id]; row != nil {
			delete(byID, id)
			message, err := messageModel(row)
			if err != nil {
				return nil, err
			}
			messages = append(messages, message)
		}
	}
	return messages, nil
}
