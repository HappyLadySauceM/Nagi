// Package infrastructure implements the conversation repository, ingress handler and worker.
// conversation/infrastructure 实现会话仓库、入站处理器与工作器。
package infrastructure

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/HappyLadySauceM/Nagi/internal/conversation/domain"
	obdomain "github.com/HappyLadySauceM/Nagi/internal/onebot/domain"
)

// Repo is the pgx-backed conversation repository.
// Repo 是基于 pgx 的会话仓库。
type Repo struct {
	pool *pgxpool.Pool
}

// NewRepo builds the repository; the pool must not be nil.
// NewRepo 构建仓库；pool 不能为 nil。
func NewRepo(pool *pgxpool.Pool) (*Repo, error) {
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
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return domain.IngestResult{}, fmt.Errorf("conversation: begin ingest: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := tx.Exec(ctx,
		`INSERT INTO bot_accounts (bot_id) VALUES ($1) ON CONFLICT (bot_id) DO NOTHING`, in.BotID,
	); err != nil {
		return domain.IngestResult{}, fmt.Errorf("conversation: ensure bot account: %w", err)
	}

	var convID string
	if err := tx.QueryRow(ctx, `
		INSERT INTO conversations (id, bot_id, kind, target_id, last_message_at)
		VALUES ($1, $2, $3, $4, now())
		ON CONFLICT (bot_id, kind, target_id) DO UPDATE SET last_message_at = now()
		RETURNING id
	`, uuid.NewString(), in.BotID, string(in.Kind), in.TargetID).Scan(&convID); err != nil {
		return domain.IngestResult{}, fmt.Errorf("conversation: upsert conversation: %w", err)
	}

	segJSON, err := encodeSegments(in.Segments)
	if err != nil {
		return domain.IngestResult{}, err
	}
	var msgID int64
	err = tx.QueryRow(ctx, `
		INSERT INTO messages (conversation_id, bot_id, direction, platform_message_id, sender_id, text_content, segments)
		VALUES ($1, $2, 'incoming', $3, $4, $5, $6)
		ON CONFLICT (bot_id, conversation_id, platform_message_id)
			WHERE direction = 'incoming' AND platform_message_id IS NOT NULL
		DO NOTHING
		RETURNING id
	`, convID, in.BotID, in.PlatformMessageID, in.SenderID, in.Text, segJSON).Scan(&msgID)
	if errors.Is(err, pgx.ErrNoRows) {
		// Duplicate platform event: commit the no-op and report it.
		// 重复的平台事件：提交空操作并报告。
		if err := tx.Commit(ctx); err != nil {
			return domain.IngestResult{}, fmt.Errorf("conversation: commit duplicate: %w", err)
		}
		return domain.IngestResult{ConversationID: convID, Duplicate: true}, nil
	}
	if err != nil {
		return domain.IngestResult{}, fmt.Errorf("conversation: insert message: %w", err)
	}

	var jobID int64
	if err := tx.QueryRow(ctx,
		`INSERT INTO message_jobs (incoming_message_id, traceparent, tracestate)
		 VALUES ($1, $2, $3) RETURNING id`,
		msgID, in.TraceParent, in.TraceState,
	).Scan(&jobID); err != nil {
		return domain.IngestResult{}, fmt.Errorf("conversation: insert job: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.IngestResult{}, fmt.Errorf("conversation: commit ingest: %w", err)
	}
	return domain.IngestResult{ConversationID: convID, MessageID: msgID, JobID: jobID}, nil
}

// RecentMessages returns up to limit newest messages of one conversation, oldest first.
// RecentMessages 返回一个会话最多 limit 条最新消息，按时间正序。
func (r *Repo) RecentMessages(ctx context.Context, conversationID string, limit int) ([]domain.Message, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT id, conversation_id, bot_id, direction, COALESCE(platform_message_id, 0), sender_id,
		       text_content, segments, COALESCE(sent_status, ''), created_at
		FROM messages
		WHERE conversation_id = $1
		ORDER BY created_at DESC, id DESC
		LIMIT $2
	`, conversationID, limit)
	if err != nil {
		return nil, fmt.Errorf("conversation: recent messages: %w", err)
	}
	defer rows.Close()
	msgs := []domain.Message{}
	for rows.Next() {
		m, scanErr := scanMessage(rows.Scan)
		if scanErr != nil {
			return nil, scanErr
		}
		msgs = append(msgs, m)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("conversation: scan messages: %w", err)
	}
	// Reverse into chronological order for context building.
	// 反转为时间正序，供上下文构建使用。
	for i, j := 0, len(msgs)-1; i < j; i, j = i+1, j-1 {
		msgs[i], msgs[j] = msgs[j], msgs[i]
	}
	return msgs, nil
}

// GetConversation returns one conversation when it exists.
// GetConversation 返回已存在的会话。
func (r *Repo) GetConversation(ctx context.Context, botID int64, kind domain.Kind, targetID int64) (domain.Conversation, bool, error) {
	var c domain.Conversation
	var kindStr string
	err := r.pool.QueryRow(ctx, `
		SELECT id, bot_id, kind, target_id, COALESCE(last_message_at, to_timestamp(0))
		FROM conversations
		WHERE bot_id = $1 AND kind = $2 AND target_id = $3
	`, botID, string(kind), targetID).Scan(&c.ID, &c.BotID, &kindStr, &c.TargetID, &c.LastMsgAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Conversation{}, false, nil
	}
	if err != nil {
		return domain.Conversation{}, false, fmt.Errorf("conversation: get conversation: %w", err)
	}
	c.Kind = domain.Kind(kindStr)
	return c, true, nil
}

// ClaimReadyJobs atomically claims due pending jobs by moving them to the
// 'processing' state and returns their execution context. FOR UPDATE SKIP LOCKED
// keeps concurrent polls or workers from claiming the same job twice, so a job
// sitting in a memory queue is never re-dispatched on the next poll.
// ClaimReadyJobs 将到期任务原子置为 processing 并返回执行上下文。
// SKIP LOCKED 保证并发轮询不会重复认领同一任务，内存队列中的任务
// 不会在下一个轮询周期被重复投递。
func (r *Repo) ClaimReadyJobs(ctx context.Context, limit int) ([]domain.Job, error) {
	rows, err := r.pool.Query(ctx, `
		WITH picked AS (
			SELECT id FROM message_jobs
			WHERE status = 'pending' AND next_run_at <= now()
			ORDER BY next_run_at, id
			LIMIT $1
			FOR UPDATE SKIP LOCKED
		), claimed AS (
			UPDATE message_jobs j
			SET status = 'processing', updated_at = now()
			FROM picked
			WHERE j.id = picked.id
			RETURNING j.id, j.incoming_message_id, j.traceparent, j.tracestate
		)
		SELECT cl.id, cl.incoming_message_id, cl.traceparent, cl.tracestate,
		       m.conversation_id, c.bot_id, c.kind, c.target_id,
		       m.id, COALESCE(m.platform_message_id, 0), m.sender_id, m.text_content, m.segments, m.created_at
		FROM claimed cl
		JOIN messages m ON m.id = cl.incoming_message_id
		JOIN conversations c ON c.id = m.conversation_id
		ORDER BY cl.id
	`, limit)
	if err != nil {
		return nil, fmt.Errorf("conversation: claim ready jobs: %w", err)
	}
	defer rows.Close()
	jobs := []domain.Job{}
	for rows.Next() {
		var j domain.Job
		var kindStr string
		var segJSON []byte
		if err := rows.Scan(&j.ID, &j.MessageID, &j.TraceParent, &j.TraceState,
			&j.ConversationID, &j.BotID, &kindStr, &j.TargetID,
			&j.Message.ID, &j.Message.PlatformMessageID, &j.Message.SenderID, &j.Message.Text,
			&segJSON, &j.Message.CreatedAt); err != nil {
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
			if failErr := r.FailJob(ctx, j.ID, "bad_message_data", 0, 1); failErr != nil {
				return nil, fmt.Errorf("conversation: fail poison job %d: %w", j.ID, failErr)
			}
			continue
		}
		j.Message.Segments = segs
		jobs = append(jobs, j)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("conversation: scan jobs: %w", err)
	}
	return jobs, nil
}

// CompleteJob marks one claimed job done; the status guard keeps the write from
// clobbering a job another run has already recovered.
// CompleteJob 将已认领任务标记为完成；状态前置条件防止覆盖已被
// 其他运行实例恢复的任务。
func (r *Repo) CompleteJob(ctx context.Context, jobID int64) error {
	_, err := r.pool.Exec(ctx,
		`UPDATE message_jobs SET status = 'done', updated_at = now()
		 WHERE id = $1 AND status = 'processing'`, jobID)
	if err != nil {
		return fmt.Errorf("conversation: complete job: %w", err)
	}
	return nil
}

// FailJob records a failure for a claimed job; jobs are retried with backoff
// until maxAttempts.
// FailJob 记录已认领任务的失败；任务按退避重试直至 maxAttempts。
func (r *Repo) FailJob(ctx context.Context, jobID int64, code string, retryAfter time.Duration, maxAttempts int) error {
	_, err := r.pool.Exec(ctx, `
		UPDATE message_jobs SET
			attempts = attempts + 1,
			last_error_code = $2,
			updated_at = now(),
			status = CASE WHEN attempts + 1 >= $3 THEN 'failed' ELSE 'pending' END,
			next_run_at = CASE WHEN attempts + 1 >= $3 THEN next_run_at ELSE now() + make_interval(secs => $4) END
		WHERE id = $1 AND status = 'processing'
	`, jobID, code, maxAttempts, retryAfter.Seconds())
	if err != nil {
		return fmt.Errorf("conversation: fail job: %w", err)
	}
	return nil
}

// ReleaseJob returns one claimed job to the pending state, e.g. when the worker
// queue for its conversation is full.
// ReleaseJob 将一个已认领任务退回待处理状态，例如其会话队列已满时。
func (r *Repo) ReleaseJob(ctx context.Context, jobID int64) error {
	_, err := r.pool.Exec(ctx,
		`UPDATE message_jobs SET status = 'pending', updated_at = now()
		 WHERE id = $1 AND status = 'processing'`, jobID)
	if err != nil {
		return fmt.Errorf("conversation: release job: %w", err)
	}
	return nil
}

// RecoverStaleJobs requeues jobs left in processing by a previous run.
// RecoverStaleJobs 将上次运行遗留的处理中任务重新排队。
func (r *Repo) RecoverStaleJobs(ctx context.Context) error {
	_, err := r.pool.Exec(ctx, `
		UPDATE message_jobs SET status = 'pending', updated_at = now() WHERE status = 'processing'
	`)
	if err != nil {
		return fmt.Errorf("conversation: recover stale jobs: %w", err)
	}
	return nil
}

// RecordOutgoing persists one bot reply — including the platform message id so
// reply-to-bot detection can match later quotes — and refreshes the conversation
// activity atomically.
// RecordOutgoing 持久化一条机器人回复（含 platform message id，供后续引用判定匹配），
// 并在单事务内刷新会话活跃时间。
func (r *Repo) RecordOutgoing(ctx context.Context, conversationID string, botID int64, text string, platformMessageID int64, sentStatus string) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("conversation: begin record outgoing: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `
		INSERT INTO messages (conversation_id, bot_id, direction, platform_message_id, sender_id, text_content, sent_status)
		VALUES ($1, $2, 'outgoing', NULLIF($3, 0), $2, $4, $5)
	`, conversationID, botID, platformMessageID, text, sentStatus); err != nil {
		return fmt.Errorf("conversation: record outgoing: %w", err)
	}
	if _, err := tx.Exec(ctx,
		`UPDATE conversations SET last_message_at = now() WHERE id = $1`, conversationID,
	); err != nil {
		return fmt.Errorf("conversation: touch conversation: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("conversation: commit record outgoing: %w", err)
	}
	return nil
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
		SELECT COALESCE(MAX(created_at), '0001-01-01 00:00:00+00'::timestamptz),
		       COUNT(*) FILTER (WHERE created_at > now() - interval '1 hour')
		FROM messages
		WHERE conversation_id = $1 AND direction = 'outgoing'
	`, conversationID).Scan(&last, &count)
	if err != nil {
		return time.Time{}, 0, fmt.Errorf("conversation: bot reply meta: %w", err)
	}
	return last, count, nil
}

type rowScanner func(dest ...any) error

// scanMessage scans the shared message column order.
// scanMessage 扫描共享的消息列顺序。
func scanMessage(scan rowScanner) (domain.Message, error) {
	var m domain.Message
	var direction string
	var segJSON []byte
	if err := scan(&m.ID, &m.ConversationID, &m.BotID, &direction, &m.PlatformMessageID,
		&m.SenderID, &m.Text, &segJSON, &m.SentStatus, &m.CreatedAt); err != nil {
		return domain.Message{}, fmt.Errorf("conversation: scan message: %w", err)
	}
	m.Direction = domain.Direction(direction)
	segs, err := decodeSegments(segJSON)
	if err != nil {
		return domain.Message{}, err
	}
	m.Segments = segs
	return m, nil
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
