package infrastructure

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	memoryapp "github.com/HappyLadySauceM/Nagi/internal/memory/application"
	"github.com/HappyLadySauceM/Nagi/internal/memory/domain"
	scopeinfra "github.com/HappyLadySauceM/Nagi/internal/scope/infrastructure"
	"github.com/HappyLadySauceM/Nagi/pkg/db"
)

// ListAuthorizedScopes returns conversations whose effective policy currently
// authorizes extraction and whose stamped messages still need jobs.
// ListAuthorizedScopes 返回当前有效策略仍授权提取、且带授权戳消息尚未建任务的会话。
func (r *Repo) ListAuthorizedScopes(ctx context.Context, limit int) ([]domain.ExtractionScope, error) {
	if limit <= 0 {
		return nil, fmt.Errorf("memory: extraction scan limit must be positive")
	}
	// The event expression mirrors ResolveMemoryAuthorization: an independent
	// policy wins; without one, the deleted policy's removed generation
	// participates before falling back to the template generation.
	// 事件表达式与 ResolveMemoryAuthorization 一致：独立策略优先；没有独立策略时，
	// 被删除策略的 removed 代际先参与比较，再回落到模板代际。
	rows, err := r.pool.Query(ctx, `
		SELECT resolved.bot_id, resolved.conversation_id, resolved.scope_type, resolved.scope_id, resolved.event_id
		FROM (
			SELECT c.bot_id, c.id::text AS conversation_id, c.kind AS scope_type, c.target_id AS scope_id,
				c.last_message_at,
				CASE
					WHEN dp.bot_id IS NOT NULL THEN
						CASE WHEN dp.memory_authorization_event_id > 0 THEN dp.memory_authorization_event_id
							ELSE COALESCE((SELECT MAX(de.id) FROM scope_memory_authorization_events de WHERE de.bot_id=c.bot_id AND de.scope_type=c.kind AND de.scope_id=c.target_id), 0)
						END
					ELSE GREATEST(
						COALESCE((SELECT MAX(dr.id) FROM scope_memory_authorization_events dr WHERE dr.bot_id=c.bot_id AND dr.scope_type=c.kind AND dr.scope_id=c.target_id AND dr.event_kind='removed'), 0),
						CASE
							WHEN tp.bot_id IS NOT NULL AND tp.memory_authorization_event_id > 0 THEN tp.memory_authorization_event_id
							WHEN tp.bot_id IS NOT NULL THEN COALESCE((SELECT MAX(te.id) FROM scope_memory_authorization_events te WHERE te.bot_id=c.bot_id AND te.scope_type=c.kind AND te.scope_id=0), 0)
							ELSE COALESCE((SELECT MAX(tr.id) FROM scope_memory_authorization_events tr WHERE tr.bot_id=c.bot_id AND tr.scope_type=c.kind AND tr.scope_id=0 AND tr.event_kind='removed'), 0)
						END
					)
				END AS event_id,
				COALESCE(dp.enabled, tp.enabled, false) AS enabled,
				COALESCE(dp.store_messages, tp.store_messages, true) AS store_messages,
				COALESCE(dp.allow_memory_write, tp.allow_memory_write, false) AS allow_memory_write
			FROM conversations c
			LEFT JOIN scope_policies dp
				ON dp.bot_id=c.bot_id AND dp.scope_type=c.kind AND dp.scope_id=c.target_id
			LEFT JOIN scope_policies tp
				ON tp.bot_id=c.bot_id AND tp.scope_type=c.kind AND tp.scope_id=0
		) resolved
		JOIN scope_memory_authorization_events e ON e.id=resolved.event_id
		WHERE resolved.enabled AND resolved.store_messages AND resolved.allow_memory_write
			AND e.authorized=true
			AND EXISTS (
				SELECT 1 FROM messages m
				WHERE m.bot_id=resolved.bot_id AND m.conversation_id=resolved.conversation_id::uuid AND m.direction='incoming'
					AND m.memory_authorization_event_id=e.id
					AND NOT EXISTS (SELECT 1 FROM memory_extraction_jobs j WHERE j.message_id=m.id)
			)
		ORDER BY resolved.last_message_at NULLS LAST, resolved.conversation_id
		LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]domain.ExtractionScope, 0)
	for rows.Next() {
		var item domain.ExtractionScope
		if err := rows.Scan(&item.BotID, &item.ConversationID, &item.ScopeType, &item.ScopeID, &item.AuthorizationEventID); err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// CreateExtractionJobs creates one durable job per message under the current
// authorization generation. Jobs wait MaxWait before becoming claimable so
// nearby messages can be coalesced into one model call.
// CreateExtractionJobs 在当前授权代际下为每条消息建立持久任务。任务等待
// MaxWait 后才可认领，使相邻消息能合并为一次模型调用。
func (r *Repo) CreateExtractionJobs(ctx context.Context, scope domain.ExtractionScope, messages []domain.ExtractionSourceMessage, maxWait time.Duration) (int, error) {
	if len(messages) == 0 {
		return 0, nil
	}
	if maxWait < 0 {
		return 0, fmt.Errorf("memory: extraction max wait must not be negative")
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback(ctx)
	// Lock the conversation row before the authorization lock. Ingest upserts
	// the conversation and then stamps authorization in the same transaction, so
	// both paths must use conversation -> authorization order to avoid a
	// cross-transaction deadlock.
	// 先锁会话行再取授权锁；Ingest 在同一事务内先锁会话、后盖章，
	// 两条路径保持 conversation -> authorization 的统一顺序以避免死锁。
	var lockedBotID, lockedTargetID int64
	var lockedScopeType string
	if err := tx.QueryRow(ctx, `SELECT bot_id, kind, target_id FROM conversations WHERE id=$1::uuid FOR UPDATE`, scope.ConversationID).Scan(&lockedBotID, &lockedScopeType, &lockedTargetID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return 0, nil
		}
		return 0, err
	}
	if lockedBotID != scope.BotID || lockedScopeType != scope.ScopeType || lockedTargetID != scope.ScopeID {
		return 0, nil
	}
	if err := scopeinfra.LockMemoryAuthorization(ctx, tx, scope.BotID, scope.ScopeType, scope.ScopeID); err != nil {
		return 0, err
	}
	currentEventID, authorized, err := scopeinfra.ResolveMemoryAuthorizationInTx(ctx, tx, scope.BotID, scope.ScopeType, scope.ScopeID)
	if err != nil {
		return 0, err
	}
	if !authorized || currentEventID != scope.AuthorizationEventID {
		return 0, nil
	}
	created := 0
	maxMessageID := int64(0)
	for _, message := range messages {
		if message.AuthorizationEventID != currentEventID || message.BotID != scope.BotID {
			continue
		}
		if message.ID > maxMessageID {
			maxMessageID = message.ID
		}
		result, err := tx.Exec(ctx, `
			INSERT INTO memory_extraction_jobs
				(id, bot_id, conversation_id, scope_type, scope_id, message_id,
				 authorization_event_id, status, next_run_at)
			VALUES ($1,$2,$3::uuid,$4,$5,$6,$7,'pending',clock_timestamp()+make_interval(secs=>$8))
			ON CONFLICT DO NOTHING`,
			uuid.NewString(), scope.BotID, scope.ConversationID, scope.ScopeType, scope.ScopeID,
			message.ID, currentEventID, maxWait.Seconds())
		if err != nil {
			return 0, err
		}
		created += int(result.RowsAffected())
	}
	if maxMessageID > 0 {
		if _, err := tx.Exec(ctx, `
			INSERT INTO memory_extraction_cursors
				(bot_id, conversation_id, last_message_id, last_authorization_event_id, last_scanned_at)
			VALUES ($1,$2::uuid,$3,$4,clock_timestamp())
			ON CONFLICT (bot_id, conversation_id) DO UPDATE SET
				last_message_id=GREATEST(memory_extraction_cursors.last_message_id, EXCLUDED.last_message_id),
				last_authorization_event_id=EXCLUDED.last_authorization_event_id,
				last_scanned_at=clock_timestamp(),
				updated_at=clock_timestamp()`,
			scope.BotID, scope.ConversationID, maxMessageID, currentEventID); err != nil {
			return 0, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, err
	}
	return created, nil
}

// ClaimReadyExtractionBatch claims one bounded, per-conversation batch. The
// authorization lock is acquired before the conversation/job locks; when the
// effective generation changed, pending jobs are cancelled instead of run.
// ClaimReadyExtractionBatch 认领一个有界、按会话隔离的批次。先取得授权锁，
// 再锁定会话与任务；有效代际变化时取消待处理任务而不是继续执行。
func (r *Repo) ClaimReadyExtractionBatch(ctx context.Context, options domain.ExtractionClaimOptions) (domain.ExtractionBatch, bool, error) {
	if options.MaxMessages <= 0 || options.MaxRunes <= 0 || options.Lease <= 0 {
		return domain.ExtractionBatch{}, false, fmt.Errorf("memory: invalid extraction claim options")
	}
	// A source message can disappear through retention after the job was
	// created. Mark such jobs failed here so they cannot hold the head of the
	// claim queue or block other conversations in the same worker loop.
	// 来源消息可能在任务创建后被留存清理；这里先把无来源任务置为失败，
	// 避免它占住认领队首并阻塞同一轮其它会话。
	if _, err := r.pool.Exec(ctx, `
		UPDATE memory_extraction_jobs
		SET status='failed', claim_token=NULL, lease_expires_at=NULL,
			last_error_code='source_missing', updated_at=clock_timestamp()
		WHERE status='pending' AND message_id IS NULL`); err != nil {
		return domain.ExtractionBatch{}, false, err
	}
	var conversationID string
	var botID, targetID int64
	var scopeType string
	err := r.pool.QueryRow(ctx, `
		SELECT c.id::text, c.bot_id, c.kind, c.target_id
		FROM conversations c
		WHERE EXISTS (
			SELECT 1 FROM memory_extraction_jobs j
			JOIN messages m ON m.id=j.message_id
			WHERE j.conversation_id=c.id AND j.status='pending' AND j.next_run_at<=clock_timestamp()
		)
		AND NOT EXISTS (
			SELECT 1 FROM memory_extraction_jobs busy
			WHERE busy.conversation_id=c.id AND busy.status='processing'
		)
		ORDER BY c.last_message_at NULLS LAST, c.id
		LIMIT 1`).Scan(&conversationID, &botID, &scopeType, &targetID)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.ExtractionBatch{}, false, nil
	}
	if err != nil {
		return domain.ExtractionBatch{}, false, err
	}

	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return domain.ExtractionBatch{}, false, err
	}
	defer tx.Rollback(ctx)
	// Lock the conversation row before the authorization lock. Ingest upserts
	// the conversation and then stamps authorization in the same transaction, so
	// both paths must use conversation -> authorization order to avoid a
	// cross-transaction deadlock.
	// 先锁会话行再取授权锁；Ingest 在同一事务内先锁会话、后盖章，
	// 两条路径保持 conversation -> authorization 的统一顺序以避免死锁。
	var lockedBotID, lockedTargetID int64
	var lockedScopeType string
	err = tx.QueryRow(ctx, `SELECT bot_id, kind, target_id FROM conversations WHERE id=$1::uuid FOR UPDATE`, conversationID).Scan(&lockedBotID, &lockedScopeType, &lockedTargetID)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.ExtractionBatch{}, false, nil
	}
	if err != nil {
		return domain.ExtractionBatch{}, false, err
	}
	if lockedBotID != botID || lockedScopeType != scopeType || lockedTargetID != targetID {
		return domain.ExtractionBatch{}, false, nil
	}
	if err := scopeinfra.LockMemoryAuthorization(ctx, tx, botID, scopeType, targetID); err != nil {
		return domain.ExtractionBatch{}, false, err
	}
	var busy bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM memory_extraction_jobs WHERE conversation_id=$1::uuid AND status='processing')`, conversationID).Scan(&busy); err != nil {
		return domain.ExtractionBatch{}, false, err
	}
	if busy {
		return domain.ExtractionBatch{}, false, nil
	}
	if lockedBotID != botID || lockedScopeType != scopeType || lockedTargetID != targetID {
		return domain.ExtractionBatch{}, false, nil
	}
	currentEventID, authorized, err := scopeinfra.ResolveMemoryAuthorizationInTx(ctx, tx, botID, scopeType, targetID)
	if err != nil {
		return domain.ExtractionBatch{}, false, err
	}
	if !authorized || currentEventID == 0 {
		if _, err := tx.Exec(ctx, `UPDATE memory_extraction_jobs SET status='cancelled', claim_token=NULL, lease_expires_at=NULL, last_error_code='authorization_changed', updated_at=clock_timestamp() WHERE conversation_id=$1::uuid AND status='pending'`, conversationID); err != nil {
			return domain.ExtractionBatch{}, false, err
		}
		if err := tx.Commit(ctx); err != nil {
			return domain.ExtractionBatch{}, false, err
		}
		return domain.ExtractionBatch{}, false, nil
	}
	// A stale pending job from an older authorization generation must not make
	// this conversation the permanent head of the claim queue.
	// 旧授权代际的待处理任务不能沉淀为认领队列的永久队首。
	if _, err := tx.Exec(ctx, `UPDATE memory_extraction_jobs SET status='cancelled', claim_token=NULL, lease_expires_at=NULL, last_error_code='authorization_changed', updated_at=clock_timestamp() WHERE conversation_id=$1::uuid AND status='pending' AND authorization_event_id<>$2`, conversationID, currentEventID); err != nil {
		return domain.ExtractionBatch{}, false, err
	}

	rows, err := tx.Query(ctx, `
		SELECT j.id::text, j.bot_id, j.conversation_id::text, j.scope_type, j.scope_id,
			j.message_id, j.authorization_event_id, j.status, j.attempts, j.round_attempts,
			j.next_run_at, COALESCE(j.last_error_code, ''), j.manual_retries,
			j.created_at, j.updated_at,
			m.id, m.sender_id, m.text_content, m.created_at
		FROM memory_extraction_jobs j
		JOIN messages m ON m.id=j.message_id
		WHERE j.conversation_id=$1::uuid
			AND j.authorization_event_id=$2
			AND j.status='pending' AND j.next_run_at<=clock_timestamp()
			AND m.memory_authorization_event_id=$2
		ORDER BY j.next_run_at, j.id
		LIMIT $3
		FOR UPDATE OF j`, conversationID, currentEventID, options.MaxMessages)
	if err != nil {
		return domain.ExtractionBatch{}, false, err
	}
	jobs := make([]domain.ExtractionJob, 0, options.MaxMessages)
	messages := make([]domain.ExtractionSourceMessage, 0, options.MaxMessages)
	runes := 0
	for rows.Next() {
		var job domain.ExtractionJob
		var message domain.ExtractionSourceMessage
		var messageID sql.NullInt64
		if err := rows.Scan(&job.ID, &job.BotID, &job.ConversationID, &job.ScopeType, &job.ScopeID,
			&messageID, &job.AuthorizationEventID, &job.Status, &job.Attempts, &job.RoundAttempts,
			&job.NextRunAt, &job.LastErrorCode, &job.ManualRetries, &job.CreatedAt, &job.UpdatedAt,
			&message.ID, &message.SenderID, &message.Text, &message.CreatedAt); err != nil {
			rows.Close()
			return domain.ExtractionBatch{}, false, err
		}
		if !messageID.Valid {
			continue
		}
		job.MessageID = messageID.Int64
		message.BotID = botID
		message.ConversationID = conversationID
		message.ScopeType = scopeType
		message.ScopeID = targetID
		message.AuthorizationEventID = currentEventID
		messageRunes := len([]rune(fmt.Sprintf("[source_message_id=%d][sender_id=%d]\n", message.ID, message.SenderID))) + len([]rune(message.Text))
		if len(jobs) > 0 && runes+messageRunes > options.MaxRunes {
			break
		}
		jobs = append(jobs, job)
		messages = append(messages, message)
		runes += messageRunes
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return domain.ExtractionBatch{}, false, err
	}
	rows.Close()
	if len(jobs) == 0 {
		return domain.ExtractionBatch{}, false, nil
	}
	claimToken := uuid.NewString()
	leaseExpiresAt := time.Now().Add(options.Lease)
	for _, job := range jobs {
		result, err := tx.Exec(ctx, `
			UPDATE memory_extraction_jobs
			SET status='processing', claim_token=$1::uuid, lease_expires_at=$2, updated_at=clock_timestamp()
			WHERE id=$3::uuid AND status='pending' AND next_run_at<=clock_timestamp()`, claimToken, leaseExpiresAt, job.ID)
		if err != nil {
			return domain.ExtractionBatch{}, false, err
		}
		if result.RowsAffected() != 1 {
			return domain.ExtractionBatch{}, false, memoryapp.ErrExtractionLeaseLost
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.ExtractionBatch{}, false, err
	}
	return domain.ExtractionBatch{
		Scope: domain.ExtractionScope{BotID: botID, ConversationID: conversationID, ScopeType: scopeType, ScopeID: targetID, AuthorizationEventID: currentEventID},
		Jobs:  jobs, Messages: messages, ClaimToken: claimToken, LeaseExpiresAt: leaseExpiresAt,
	}, true, nil
}

// FinalizeExtractionBatch writes all candidate memories and marks every job
// done in one transaction after rechecking authorization and lease ownership.
// FinalizeExtractionBatch 在重新核对授权与租约归属后，于同一事务写入全部候选并
// 将所有任务标为完成。
func (r *Repo) FinalizeExtractionBatch(ctx context.Context, batch domain.ExtractionBatch, candidates []domain.Memory) (domain.FinalizeResult, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return domain.FinalizeResult{}, err
	}
	defer tx.Rollback(ctx)
	if err := scopeinfra.LockMemoryAuthorization(ctx, tx, batch.Scope.BotID, batch.Scope.ScopeType, batch.Scope.ScopeID); err != nil {
		return domain.FinalizeResult{}, err
	}
	currentEventID, authorized, err := scopeinfra.ResolveMemoryAuthorizationInTx(ctx, tx, batch.Scope.BotID, batch.Scope.ScopeType, batch.Scope.ScopeID)
	if err != nil {
		return domain.FinalizeResult{}, err
	}
	if !authorized || currentEventID != batch.Scope.AuthorizationEventID {
		if err := cancelClaimedBatchTx(ctx, tx, batch, "authorization_changed"); err != nil {
			return domain.FinalizeResult{}, err
		}
		if err := tx.Commit(ctx); err != nil {
			return domain.FinalizeResult{}, err
		}
		return domain.FinalizeResult{}, memoryapp.ErrExtractionAuthorizationChanged
	}
	if err := r.lockClaimedJobs(ctx, tx, batch); err != nil {
		return domain.FinalizeResult{}, err
	}
	sources := make(map[int64]domain.ExtractionSourceMessage, len(batch.Messages))
	for _, message := range batch.Messages {
		sources[message.ID] = message
	}
	result := domain.FinalizeResult{}
	for _, candidate := range candidates {
		source, ok := sources[candidate.SourceMessageID]
		if !ok {
			return domain.FinalizeResult{}, fmt.Errorf("memory: extraction candidate source %d is outside the batch", candidate.SourceMessageID)
		}
		if candidate.BotID != batch.Scope.BotID || candidate.Status != domain.StatusCandidate {
			return domain.FinalizeResult{}, fmt.Errorf("memory: extraction candidate is not owned by this batch")
		}
		if candidate.SourceSenderID != source.SenderID {
			return domain.FinalizeResult{}, fmt.Errorf("memory: extraction candidate source sender does not match")
		}
		if candidate.SubjectType == "" || candidate.SubjectType == domain.SubjectUnknown {
			candidate.SubjectType = domain.SubjectUnknown
			candidate.SubjectID = domain.ExtractionUnknownSubjectID(candidate.SourceMessageID, candidate.SourceSenderID)
		}
		if err := candidate.Validate(); err != nil {
			return domain.FinalizeResult{}, err
		}
		key := domain.ComputeDedupeKey(candidate.BotID, candidate.Visibility, candidate.OwnerKey, candidate.SubjectType, candidate.SubjectID, candidate.Content)
		var existingStatus string
		err := tx.QueryRow(ctx, `SELECT status FROM memories WHERE dedupe_key=$1 AND status IN ('candidate','active','deleted','superseded') ORDER BY created_at LIMIT 1`, key).Scan(&existingStatus)
		if err == nil {
			if existingStatus == string(domain.StatusDeleted) || existingStatus == string(domain.StatusSuperseded) {
				result.Suppressed++
			} else {
				result.Duplicates++
			}
			continue
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return domain.FinalizeResult{}, err
		}
		affected, err := insertExtractionMemory(ctx, tx, candidate, key)
		if err != nil {
			return domain.FinalizeResult{}, err
		}
		if affected == 0 {
			result.Duplicates++
		} else {
			result.Created++
		}
	}
	for _, job := range batch.Jobs {
		affected, err := tx.Exec(ctx, `
			UPDATE memory_extraction_jobs
			SET status='done', attempts=attempts+1, round_attempts=0,
				claim_token=NULL, lease_expires_at=NULL, last_error_code=NULL, updated_at=clock_timestamp()
			WHERE id=$1::uuid AND status='processing' AND claim_token=$2::uuid AND lease_expires_at>clock_timestamp()`,
			job.ID, batch.ClaimToken)
		if err != nil {
			return domain.FinalizeResult{}, err
		}
		if affected.RowsAffected() != 1 {
			return domain.FinalizeResult{}, memoryapp.ErrExtractionLeaseLost
		}
	}
	return result, tx.Commit(ctx)
}

// FailExtractionBatch records a retryable failure for every still-owned job.
// It rechecks authorization under the same lock, cancelling the batch when the
// generation has changed instead of retrying stale work.
// FailExtractionBatch 记录批次失败；在相同授权锁下重新核对代际，已变化时取消批次
// 而不重试过期任务。
func (r *Repo) FailExtractionBatch(ctx context.Context, batch domain.ExtractionBatch, code string, retryDelays []time.Duration, maxAttempts int) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if err := scopeinfra.LockMemoryAuthorization(ctx, tx, batch.Scope.BotID, batch.Scope.ScopeType, batch.Scope.ScopeID); err != nil {
		return err
	}
	currentEventID, authorized, err := scopeinfra.ResolveMemoryAuthorizationInTx(ctx, tx, batch.Scope.BotID, batch.Scope.ScopeType, batch.Scope.ScopeID)
	if err != nil {
		return err
	}
	for _, job := range batch.Jobs {
		var attempts, roundAttempts int
		var claimToken sql.NullString
		var leaseExpiresAt sql.NullTime
		err := tx.QueryRow(ctx, `SELECT attempts, round_attempts, claim_token::text, lease_expires_at FROM memory_extraction_jobs WHERE id=$1::uuid FOR UPDATE`, job.ID).Scan(&attempts, &roundAttempts, &claimToken, &leaseExpiresAt)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return err
		}
		if !claimToken.Valid || claimToken.String != batch.ClaimToken || !leaseExpiresAt.Valid || !leaseExpiresAt.Time.After(time.Now()) {
			continue
		}
		if !authorized || currentEventID != batch.Scope.AuthorizationEventID {
			if _, err := tx.Exec(ctx, `UPDATE memory_extraction_jobs SET status='cancelled', claim_token=NULL, lease_expires_at=NULL, last_error_code='authorization_changed', updated_at=clock_timestamp() WHERE id=$1::uuid`, job.ID); err != nil {
				return err
			}
			continue
		}
		nextAttempts := attempts + 1
		status := "pending"
		if nextAttempts >= maxAttempts {
			status = "failed"
		}
		nextRun := time.Now().Add(retryDelayForAttempt(retryDelays, nextAttempts))
		if _, err := tx.Exec(ctx, `
			UPDATE memory_extraction_jobs
			SET status=$1, attempts=$2, round_attempts=$3, next_run_at=$4,
				claim_token=NULL, lease_expires_at=NULL, last_error_code=$5, updated_at=clock_timestamp()
			WHERE id=$6::uuid`, status, nextAttempts, roundAttempts+1, nextRun, code, job.ID); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

// CancelExtractionBatch releases a claimed batch without candidates.
// CancelExtractionBatch 不产生候选地释放一个已认领批次。
func (r *Repo) CancelExtractionBatch(ctx context.Context, batch domain.ExtractionBatch, code string) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	for _, job := range batch.Jobs {
		if _, err := tx.Exec(ctx, `
			UPDATE memory_extraction_jobs
			SET status='cancelled', claim_token=NULL, lease_expires_at=NULL,
				last_error_code=$1, updated_at=clock_timestamp()
			WHERE id=$2::uuid AND status='processing' AND claim_token=$3::uuid`, code, job.ID, batch.ClaimToken); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

// RecoverStaleExtractionJobs returns expired processing jobs to pending or
// fails them once their cumulative attempts are exhausted.
// RecoverStaleExtractionJobs 让过期 processing 任务回到 pending；累计尝试耗尽时置失败。
func (r *Repo) RecoverStaleExtractionJobs(ctx context.Context, maxAttempts int, retryDelays []time.Duration) error {
	if maxAttempts <= 0 {
		return fmt.Errorf("memory: extraction recovery max attempts must be positive")
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `
		UPDATE memory_extraction_jobs
		SET status='failed', claim_token=NULL, lease_expires_at=NULL,
			last_error_code='source_missing', updated_at=clock_timestamp()
		WHERE message_id IS NULL AND status IN ('pending','processing')`); err != nil {
		return err
	}
	rows, err := tx.Query(ctx, `SELECT id::text, attempts FROM memory_extraction_jobs WHERE status='processing' AND lease_expires_at<=clock_timestamp() FOR UPDATE SKIP LOCKED`)
	if err != nil {
		return err
	}
	type stale struct {
		id       string
		attempts int
	}
	var items []stale
	for rows.Next() {
		var item stale
		if err := rows.Scan(&item.id, &item.attempts); err != nil {
			rows.Close()
			return err
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	for _, item := range items {
		nextAttempts := item.attempts + 1
		status := "pending"
		if nextAttempts >= maxAttempts {
			status = "failed"
		}
		if _, err := tx.Exec(ctx, `UPDATE memory_extraction_jobs SET status=$1, attempts=$2, round_attempts=round_attempts+1, next_run_at=clock_timestamp()+make_interval(secs=>$3), claim_token=NULL, lease_expires_at=NULL, last_error_code='lease_expired', updated_at=clock_timestamp() WHERE id=$4::uuid`, status, nextAttempts, retryDelayForAttempt(retryDelays, nextAttempts).Seconds(), item.id); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

func retryDelayForAttempt(delays []time.Duration, attempt int) time.Duration {
	if len(delays) == 0 {
		return 30 * time.Second
	}
	index := attempt - 1
	if index < 0 {
		index = 0
	}
	if index >= len(delays) {
		index = len(delays) - 1
	}
	return delays[index]
}

func (r *Repo) lockClaimedJobs(ctx context.Context, tx *db.Tx, batch domain.ExtractionBatch) error {
	sources := make(map[int64]domain.ExtractionSourceMessage, len(batch.Messages))
	for _, message := range batch.Messages {
		sources[message.ID] = message
	}
	for _, job := range batch.Jobs {
		var status string
		var claimToken sql.NullString
		var leaseExpiresAt sql.NullTime
		var messageID, messageBotID, messageSenderID int64
		var messageConversationID string
		var messageEventID sql.NullInt64
		err := tx.QueryRow(ctx, `
			SELECT j.status, j.claim_token::text, j.lease_expires_at,
				m.id, m.bot_id, m.conversation_id::text, m.sender_id,
				m.memory_authorization_event_id
			FROM memory_extraction_jobs j
			JOIN messages m ON m.id=j.message_id
			WHERE j.id=$1::uuid
			FOR UPDATE OF j, m`, job.ID).Scan(
			&status, &claimToken, &leaseExpiresAt, &messageID, &messageBotID,
			&messageConversationID, &messageSenderID, &messageEventID)
		if errors.Is(err, sql.ErrNoRows) {
			return memoryapp.ErrExtractionSourceMissing
		}
		if err != nil {
			return err
		}
		if status != "processing" || !claimToken.Valid || claimToken.String != batch.ClaimToken || !leaseExpiresAt.Valid || !leaseExpiresAt.Time.After(time.Now()) {
			return memoryapp.ErrExtractionLeaseLost
		}
		source, ok := sources[messageID]
		if !ok || messageBotID != batch.Scope.BotID || messageConversationID != batch.Scope.ConversationID ||
			messageSenderID != source.SenderID || !messageEventID.Valid || messageEventID.Int64 != batch.Scope.AuthorizationEventID ||
			source.AuthorizationEventID != batch.Scope.AuthorizationEventID {
			return memoryapp.ErrExtractionAuthorizationChanged
		}
	}
	return nil
}

func cancelClaimedBatchTx(ctx context.Context, tx *db.Tx, batch domain.ExtractionBatch, code string) error {
	for _, job := range batch.Jobs {
		if _, err := tx.Exec(ctx, `
			UPDATE memory_extraction_jobs
			SET status='cancelled', claim_token=NULL, lease_expires_at=NULL,
				last_error_code=$1, updated_at=clock_timestamp()
			WHERE id=$2::uuid AND status='processing' AND claim_token=$3::uuid`,
			code, job.ID, batch.ClaimToken); err != nil {
			return err
		}
	}
	return nil
}

func insertExtractionMemory(ctx context.Context, tx *db.Tx, memory domain.Memory, dedupeKey string) (int64, error) {
	subjectType := memory.SubjectType
	subjectID := strings.TrimSpace(memory.SubjectID)
	if subjectType == "" {
		subjectType = domain.SubjectUnknown
	}
	if subjectType == domain.SubjectUnknown && subjectID == "" {
		subjectID = domain.ExtractionUnknownSubjectID(memory.SourceMessageID, memory.SourceSenderID)
	}
	result, err := tx.Exec(ctx, `
		INSERT INTO memories
			(memory_id, visibility, owner_key, content, source, confidence, status, approved_by,
			 bot_id, source_message_id, source_sender_id, origin, subject_type, subject_id,
			 dedupe_key, review_reason)
		VALUES ($1::uuid,$2,$3,$4,$5,$6,'candidate','',$7,$8,$9,'extraction',$10,$11,$12,$13)
		ON CONFLICT DO NOTHING`,
		uuid.NewString(), memory.Visibility, memory.OwnerKey, memory.Content, memory.Source,
		memory.Confidence, memory.BotID, memory.SourceMessageID, memory.SourceSenderID,
		string(subjectType), subjectID, dedupeKey, memory.ReviewReason)
	if err != nil {
		return 0, err
	}
	return result.RowsAffected(), nil
}
