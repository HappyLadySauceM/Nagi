package infrastructure

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"time"

	"github.com/HappyLadySauceM/Nagi/internal/audit/domain"
	"github.com/HappyLadySauceM/Nagi/pkg/db"
	"github.com/google/uuid"
)

var (
	// ErrNotFound keeps missing audit facts distinguishable from infrastructure failures.
	// ErrNotFound 让缺失的审计事实与基础设施故障可区分。
	ErrNotFound = domain.ErrNotFound
	// ErrConflict rejects an idempotency key reused with different facts.
	// ErrConflict 拒绝使用同一幂等键写入不同事实。
	ErrConflict = domain.ErrConflict
)

// AIRecorder stores local AI audit facts. It deliberately does not replace the
// administrative Recorder, whose failures are intentionally non-fatal.
// AIRecorder 保存本地 AI 审计事实，刻意不替代失败非致命的管理审计 Recorder。
type AIRecorder struct{ pool *db.Pool }

func NewAIRecorder(pool *db.Pool) (*AIRecorder, error) {
	if pool == nil {
		return nil, fmt.Errorf("ai audit: pool required")
	}
	return &AIRecorder{pool: pool}, nil
}

func (r *AIRecorder) StartRun(ctx context.Context, run domain.AIRun) (domain.AIRun, error) {
	if run.OperationID == "" || run.JobID <= 0 || run.BotID <= 0 {
		return domain.AIRun{}, fmt.Errorf("ai audit: invalid run identity")
	}
	conversationID, err := uuid.Parse(run.ConversationID)
	if err != nil {
		return domain.AIRun{}, fmt.Errorf("ai audit: parse conversation id: %w", err)
	}
	if existing, err := r.loadRunByOperation(ctx, run.OperationID); err == nil {
		if existing.JobID != run.JobID || existing.BotID != run.BotID || existing.ConversationID != run.ConversationID || existing.TraceID != run.TraceID || existing.ConfigVersion != run.ConfigVersion {
			return domain.AIRun{}, ErrConflict
		}
		return existing, nil
	} else if !errors.Is(err, ErrNotFound) {
		return domain.AIRun{}, err
	}
	id := uuid.New()
	if run.ID != "" {
		id, err = uuid.Parse(run.ID)
		if err != nil {
			return domain.AIRun{}, fmt.Errorf("ai audit: parse run id: %w", err)
		}
	}
	startedAt := run.StartedAt
	if startedAt.IsZero() {
		startedAt = time.Now().UTC()
	}
	_, err = r.pool.Exec(ctx, `INSERT INTO ai_audit_runs (id, job_id, operation_id, bot_id, conversation_id, trace_id, config_version, status, error_code, started_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,'running','',$8)`, id, run.JobID, run.OperationID, run.BotID, conversationID, run.TraceID, run.ConfigVersion, startedAt)
	if err != nil {
		// A racing writer may have committed the same operation first. Re-read it
		// so duplicate delivery observes the established fact instead of failing.
		// 并发写入者可能刚提交同一操作；重新读取即可让重复投递得到既有事实。
		if existing, loadErr := r.loadRunByOperation(ctx, run.OperationID); loadErr == nil {
			if existing.JobID != run.JobID || existing.BotID != run.BotID || existing.ConversationID != run.ConversationID || existing.TraceID != run.TraceID || existing.ConfigVersion != run.ConfigVersion {
				return domain.AIRun{}, ErrConflict
			}
			return existing, nil
		}
		return domain.AIRun{}, fmt.Errorf("ai audit: insert run: %w", err)
	}
	return r.GetRun(ctx, id.String())
}

func (r *AIRecorder) FinishRun(ctx context.Context, runID, status, errorCode string) error {
	if runID == "" || status == "" {
		return fmt.Errorf("ai audit: invalid run completion")
	}
	id, err := uuid.Parse(runID)
	if err != nil {
		return fmt.Errorf("ai audit: parse run id: %w", err)
	}
	result, err := r.pool.Exec(ctx, `UPDATE ai_audit_runs SET status=$2,error_code=$3,finished_at=now() WHERE id=$1 AND status='running'`, id, status, errorCode)
	if err != nil {
		return fmt.Errorf("ai audit: finish run: %w", err)
	}
	if result.RowsAffected() == 1 {
		return nil
	}
	run, err := r.GetRun(ctx, runID)
	if errors.Is(err, ErrNotFound) {
		return err
	}
	if err != nil {
		return err
	}
	if run.Status == status && run.ErrorCode == errorCode {
		return nil
	}
	return ErrConflict
}

func (r *AIRecorder) RecordEvent(ctx context.Context, event domain.AIEvent) (domain.AIEvent, error) {
	if event.RunID == "" || event.OperationID == "" || event.Stage == "" || event.Sequence < 0 {
		return domain.AIEvent{}, fmt.Errorf("ai audit: invalid event identity")
	}
	if len(event.Input) > 0 && !json.Valid(event.Input) {
		return domain.AIEvent{}, fmt.Errorf("ai audit: event input is not json")
	}
	runID, err := uuid.Parse(event.RunID)
	if err != nil {
		return domain.AIEvent{}, fmt.Errorf("ai audit: parse run id: %w", err)
	}
	startedAt := event.StartedAt
	if startedAt.IsZero() {
		startedAt = time.Now().UTC()
	}
	schemaVersion := event.SchemaVersion
	if schemaVersion <= 0 {
		schemaVersion = 1
	}
	eventID := uuid.New()
	if event.ID != "" {
		eventID, err = uuid.Parse(event.ID)
		if err != nil {
			return domain.AIEvent{}, fmt.Errorf("ai audit: parse event id: %w", err)
		}
	}
	result, err := r.pool.Exec(ctx, `INSERT INTO ai_audit_events (id,run_id,operation_id,sequence,schema_version,stage,status,error_code,input,started_at)
		VALUES ($1,$2,$3,$4,$5,$6,'started','',$7,$8) ON CONFLICT (run_id,operation_id) DO NOTHING`, eventID, runID, event.OperationID, event.Sequence, schemaVersion, event.Stage, nullableJSON(event.Input), startedAt)
	if err != nil {
		return domain.AIEvent{}, fmt.Errorf("ai audit: insert event: %w", err)
	}
	if result.RowsAffected() == 1 {
		return r.loadEvent(ctx, eventID.String())
	}
	existing, err := r.loadEventByOperation(ctx, runID, event.OperationID)
	if err != nil {
		return domain.AIEvent{}, err
	}
	if existing.Sequence != event.Sequence || existing.Stage != event.Stage || existing.SchemaVersion != schemaVersion || !jsonEqual(existing.Input, event.Input) {
		return domain.AIEvent{}, ErrConflict
	}
	return existing, nil
}

func (r *AIRecorder) FinishEvent(ctx context.Context, eventID, status, errorCode string, output []byte) error {
	if eventID == "" || status == "" || (len(output) > 0 && !json.Valid(output)) {
		return fmt.Errorf("ai audit: invalid event completion")
	}
	id, err := uuid.Parse(eventID)
	if err != nil {
		return fmt.Errorf("ai audit: parse event id: %w", err)
	}
	result, err := r.pool.Exec(ctx, `UPDATE ai_audit_events SET status=$2,error_code=$3,output=$4,finished_at=now() WHERE id=$1 AND status='started'`, id, status, errorCode, nullableJSON(output))
	if err != nil {
		return fmt.Errorf("ai audit: finish event: %w", err)
	}
	if result.RowsAffected() == 1 {
		return nil
	}
	event, err := r.loadEvent(ctx, eventID)
	if err != nil {
		return err
	}
	if event.Status == status && event.ErrorCode == errorCode && jsonEqual(event.Output, output) {
		return nil
	}
	return ErrConflict
}

func (r *AIRecorder) RecordArtifact(ctx context.Context, artifact domain.AIArtifact) (domain.AIArtifact, error) {
	if artifact.RunID == "" || artifact.Kind == "" {
		return domain.AIArtifact{}, fmt.Errorf("ai audit: invalid artifact identity")
	}
	runID, err := uuid.Parse(artifact.RunID)
	if err != nil {
		return domain.AIArtifact{}, fmt.Errorf("ai audit: parse run id: %w", err)
	}
	id := uuid.New()
	if artifact.ID != "" {
		id, err = uuid.Parse(artifact.ID)
		if err != nil {
			return domain.AIArtifact{}, fmt.Errorf("ai audit: parse artifact id: %w", err)
		}
	}
	var eventID any
	if artifact.EventID != "" {
		parsed, parseErr := uuid.Parse(artifact.EventID)
		if parseErr != nil {
			return domain.AIArtifact{}, fmt.Errorf("ai audit: parse event id: %w", parseErr)
		}
		eventID = parsed
	}
	status := artifact.Status
	if status == "" {
		status = "pending"
	}
	_, err = r.pool.Exec(ctx, `INSERT INTO ai_audit_artifacts (id,run_id,event_id,kind,status,sha256,content_type,size_bytes,object_key,bucket,failure_code,expires_at,cleanup_attempts,next_attempt_at,lease_token,lease_expires_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16)`,
		id, runID, eventID, artifact.Kind, status, artifact.SHA256, artifact.ContentType, artifact.SizeBytes,
		artifact.ObjectKey, artifact.Bucket, artifact.FailureCode, artifact.ExpiresAt, artifact.CleanupAttempts,
		artifact.NextAttemptAt, artifact.LeaseToken, artifact.LeaseExpiresAt)
	if err != nil {
		return domain.AIArtifact{}, fmt.Errorf("ai audit: insert artifact: %w", err)
	}
	return r.GetArtifact(ctx, id.String())
}

func (r *AIRecorder) ListRuns(ctx context.Context, query domain.AIRunQuery) ([]domain.AIRun, error) {
	if query.BotID <= 0 {
		return nil, fmt.Errorf("ai audit: bot id required")
	}
	limit := query.Limit
	if limit <= 0 {
		limit = 20
	}
	if limit > 100 {
		return nil, fmt.Errorf("ai audit: limit exceeds 100")
	}
	args := []any{query.BotID}
	where := "bot_id=$1"
	if query.ConversationID != "" {
		conversationID, err := uuid.Parse(query.ConversationID)
		if err != nil {
			return nil, fmt.Errorf("ai audit: parse conversation id: %w", err)
		}
		args = append(args, conversationID)
		where += fmt.Sprintf(" AND conversation_id=$%d", len(args))
	}
	if query.JobID > 0 {
		args = append(args, query.JobID)
		where += fmt.Sprintf(" AND job_id=$%d", len(args))
	}
	args = append(args, limit)
	rows, err := r.pool.Query(ctx, `SELECT id,operation_id,conversation_id,trace_id,status,error_code,job_id,bot_id,config_version,started_at,finished_at FROM ai_audit_runs WHERE `+where+` ORDER BY started_at DESC LIMIT $`+fmt.Sprint(len(args)), args...)
	if err != nil {
		return nil, fmt.Errorf("ai audit: list runs: %w", err)
	}
	defer rows.Close()
	runs := make([]domain.AIRun, 0)
	for rows.Next() {
		run, err := scanRun(rows)
		if err != nil {
			return nil, err
		}
		runs = append(runs, run)
	}
	return runs, rows.Err()
}

func (r *AIRecorder) GetRun(ctx context.Context, id string) (domain.AIRun, error) {
	run, err := r.loadRun(ctx, id)
	if err != nil {
		return domain.AIRun{}, err
	}
	run.Events, err = r.eventsFor(ctx, id)
	if err != nil {
		return domain.AIRun{}, err
	}
	run.Artifacts, err = r.artifactsFor(ctx, id)
	if err != nil {
		return domain.AIRun{}, err
	}
	return run, nil
}

func (r *AIRecorder) GetArtifact(ctx context.Context, id string) (domain.AIArtifact, error) {
	parsed, err := uuid.Parse(id)
	if err != nil {
		return domain.AIArtifact{}, fmt.Errorf("ai audit: parse artifact id: %w", err)
	}
	row := r.pool.QueryRow(ctx, `SELECT id,run_id,event_id,kind,status,sha256,content_type,object_key,bucket,failure_code,size_bytes,expires_at,created_at,updated_at,cleanup_attempts,next_attempt_at,lease_token,lease_expires_at FROM ai_audit_artifacts WHERE id=$1`, parsed)
	artifact, err := scanArtifact(row)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.AIArtifact{}, ErrNotFound
	}
	return artifact, err
}

func (r *AIRecorder) loadRunByOperation(ctx context.Context, operationID string) (domain.AIRun, error) {
	row := r.pool.QueryRow(ctx, `SELECT id,operation_id,conversation_id,trace_id,status,error_code,job_id,bot_id,config_version,started_at,finished_at FROM ai_audit_runs WHERE operation_id=$1`, operationID)
	run, err := scanRun(row)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.AIRun{}, ErrNotFound
	}
	return run, err
}

func (r *AIRecorder) loadRun(ctx context.Context, id string) (domain.AIRun, error) {
	parsed, err := uuid.Parse(id)
	if err != nil {
		return domain.AIRun{}, fmt.Errorf("ai audit: parse run id: %w", err)
	}
	row := r.pool.QueryRow(ctx, `SELECT id,operation_id,conversation_id,trace_id,status,error_code,job_id,bot_id,config_version,started_at,finished_at FROM ai_audit_runs WHERE id=$1`, parsed)
	run, err := scanRun(row)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.AIRun{}, ErrNotFound
	}
	return run, err
}

func (r *AIRecorder) loadEventByOperation(ctx context.Context, runID uuid.UUID, operationID string) (domain.AIEvent, error) {
	row := r.pool.QueryRow(ctx, `SELECT id,run_id,operation_id,sequence,schema_version,stage,status,error_code,input,output,started_at,finished_at FROM ai_audit_events WHERE run_id=$1 AND operation_id=$2`, runID, operationID)
	return scanEvent(row)
}

func (r *AIRecorder) loadEvent(ctx context.Context, id string) (domain.AIEvent, error) {
	parsed, err := uuid.Parse(id)
	if err != nil {
		return domain.AIEvent{}, fmt.Errorf("ai audit: parse event id: %w", err)
	}
	row := r.pool.QueryRow(ctx, `SELECT id,run_id,operation_id,sequence,schema_version,stage,status,error_code,input,output,started_at,finished_at FROM ai_audit_events WHERE id=$1`, parsed)
	return scanEvent(row)
}

func (r *AIRecorder) eventsFor(ctx context.Context, runID string) ([]domain.AIEvent, error) {
	parsed, err := uuid.Parse(runID)
	if err != nil {
		return nil, fmt.Errorf("ai audit: parse run id: %w", err)
	}
	rows, err := r.pool.Query(ctx, `SELECT id,run_id,operation_id,sequence,schema_version,stage,status,error_code,input,output,started_at,finished_at FROM ai_audit_events WHERE run_id=$1 ORDER BY sequence`, parsed)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	events := make([]domain.AIEvent, 0)
	for rows.Next() {
		event, err := scanEvent(rows)
		if err != nil {
			return nil, err
		}
		events = append(events, event)
	}
	return events, rows.Err()
}

func (r *AIRecorder) artifactsFor(ctx context.Context, runID string) ([]domain.AIArtifact, error) {
	parsed, err := uuid.Parse(runID)
	if err != nil {
		return nil, fmt.Errorf("ai audit: parse run id: %w", err)
	}
	rows, err := r.pool.Query(ctx, `SELECT id,run_id,event_id,kind,status,sha256,content_type,object_key,bucket,failure_code,size_bytes,expires_at,created_at,updated_at,cleanup_attempts,next_attempt_at,lease_token,lease_expires_at FROM ai_audit_artifacts WHERE run_id=$1 ORDER BY created_at`, parsed)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	artifacts := make([]domain.AIArtifact, 0)
	for rows.Next() {
		artifact, err := scanArtifact(rows)
		if err != nil {
			return nil, err
		}
		artifacts = append(artifacts, artifact)
	}
	return artifacts, rows.Err()
}

type rowScanner interface{ Scan(...any) error }

func scanRun(row rowScanner) (domain.AIRun, error) {
	var id, conversationID uuid.UUID
	var run domain.AIRun
	if err := row.Scan(&id, &run.OperationID, &conversationID, &run.TraceID, &run.Status, &run.ErrorCode, &run.JobID, &run.BotID, &run.ConfigVersion, &run.StartedAt, &run.FinishedAt); err != nil {
		return domain.AIRun{}, err
	}
	run.ID, run.ConversationID = id.String(), conversationID.String()
	return run, nil
}

func scanEvent(row rowScanner) (domain.AIEvent, error) {
	var id, runID uuid.UUID
	var event domain.AIEvent
	if err := row.Scan(&id, &runID, &event.OperationID, &event.Sequence, &event.SchemaVersion, &event.Stage, &event.Status, &event.ErrorCode, &event.Input, &event.Output, &event.StartedAt, &event.FinishedAt); err != nil {
		return domain.AIEvent{}, err
	}
	event.ID, event.RunID = id.String(), runID.String()
	return event, nil
}

func scanArtifact(row rowScanner) (domain.AIArtifact, error) {
	var id, runID uuid.UUID
	var eventID *uuid.UUID
	var artifact domain.AIArtifact
	if err := row.Scan(&id, &runID, &eventID, &artifact.Kind, &artifact.Status, &artifact.SHA256, &artifact.ContentType, &artifact.ObjectKey, &artifact.Bucket, &artifact.FailureCode, &artifact.SizeBytes, &artifact.ExpiresAt, &artifact.CreatedAt, &artifact.UpdatedAt, &artifact.CleanupAttempts, &artifact.NextAttemptAt, &artifact.LeaseToken, &artifact.LeaseExpiresAt); err != nil {
		return domain.AIArtifact{}, err
	}
	artifact.ID, artifact.RunID = id.String(), runID.String()
	if eventID != nil {
		artifact.EventID = eventID.String()
	}
	return artifact, nil
}

// FindUploadedArtifactBySHA256 returns the newest retained uploaded artifact
// with the same content hash, letting identical media reuse one stored object.
// FindUploadedArtifactBySHA256 返回同一内容哈希的最新可保留已上传工件，使相同媒体复用同一存储对象。
func (r *AIRecorder) FindUploadedArtifactBySHA256(ctx context.Context, contentHash string) (domain.AIArtifact, error) {
	if contentHash == "" {
		return domain.AIArtifact{}, fmt.Errorf("ai audit: sha256 required")
	}
	row := r.pool.QueryRow(ctx, `SELECT id,run_id,event_id,kind,status,sha256,content_type,object_key,bucket,failure_code,size_bytes,expires_at,created_at,updated_at,cleanup_attempts,next_attempt_at,lease_token,lease_expires_at FROM ai_audit_artifacts
		WHERE sha256=$1 AND status='uploaded' AND object_key <> '' AND (expires_at IS NULL OR expires_at > now()) ORDER BY created_at DESC LIMIT 1`, contentHash)
	artifact, err := scanArtifact(row)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.AIArtifact{}, ErrNotFound
	}
	return artifact, err
}

// ClaimExpiredArtifacts leases uploaded objects whose retention deadline passed.
// ClaimExpiredArtifacts 租约领取已过保留期的上传对象。
func (r *AIRecorder) ClaimExpiredArtifacts(ctx context.Context, limit int, lease time.Duration) ([]domain.AIArtifact, error) {
	if limit <= 0 || lease <= 0 {
		return nil, fmt.Errorf("ai audit: invalid expiry claim")
	}
	token := uuid.NewString()
	rows, err := r.pool.Query(ctx, `WITH candidates AS (
		SELECT candidate.id FROM ai_audit_artifacts candidate
		WHERE candidate.expires_at <= now() AND candidate.object_key <> ''
			AND (candidate.next_attempt_at IS NULL OR candidate.next_attempt_at <= now())
			AND (candidate.status IN ('uploaded','pending','failed') OR (candidate.status IN ('uploading','expiring') AND candidate.lease_expires_at <= now()))
			AND NOT EXISTS (
				SELECT 1 FROM ai_audit_artifacts keep
				WHERE keep.object_key = candidate.object_key
					AND (keep.bucket = candidate.bucket OR keep.bucket = '' OR candidate.bucket = '')
					AND keep.id <> candidate.id
					AND keep.status IN ('uploaded','uploading','expiring')
					AND (keep.expires_at IS NULL OR keep.expires_at > now())
			)
		ORDER BY candidate.expires_at FOR UPDATE SKIP LOCKED LIMIT $1
	)
	UPDATE ai_audit_artifacts AS artifact SET status='expiring',lease_token=$2,lease_expires_at=now()+$3::interval,updated_at=now()
	FROM candidates WHERE artifact.id=candidates.id
	RETURNING artifact.id,artifact.run_id,artifact.event_id,artifact.kind,artifact.status,artifact.sha256,artifact.content_type,artifact.object_key,artifact.bucket,artifact.failure_code,artifact.size_bytes,artifact.expires_at,artifact.created_at,artifact.updated_at,artifact.cleanup_attempts,artifact.next_attempt_at,artifact.lease_token,artifact.lease_expires_at`, limit, token, lease.String())
	if err != nil {
		return nil, fmt.Errorf("ai audit: claim expired artifacts: %w", err)
	}
	defer rows.Close()
	artifacts := make([]domain.AIArtifact, 0, limit)
	for rows.Next() {
		artifact, scanErr := scanArtifact(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		artifacts = append(artifacts, artifact)
	}
	return artifacts, rows.Err()
}

// MarkArtifactExpired commits successful remote deletion while retaining only
// the immutable audit metadata and hash.
// MarkArtifactExpired 提交远端删除结果，仅保留不可变审计元数据与哈希。
func (r *AIRecorder) MarkArtifactExpired(ctx context.Context, id, token string) error {
	result, err := r.pool.Exec(ctx, `UPDATE ai_audit_artifacts SET status='expired',lease_token='',lease_expires_at=NULL,updated_at=now()
		WHERE id=$1 AND status='expiring' AND lease_token=$2`, id, token)
	if err != nil {
		return fmt.Errorf("ai audit: mark artifact expired: %w", err)
	}
	if result.RowsAffected() != 1 {
		return ErrConflict
	}
	return nil
}

// RetryArtifactExpiry returns a failed deletion to uploaded state for a later
// retry, preserving the object reference and failure reason for operators.
// RetryArtifactExpiry 将删除失败的工件恢复为 uploaded 状态，供后续重试并保留原因。
func (r *AIRecorder) RetryArtifactExpiry(ctx context.Context, id, token string, next time.Time, code string) error {
	result, err := r.pool.Exec(ctx, `UPDATE ai_audit_artifacts SET status='uploaded',cleanup_attempts=cleanup_attempts+1,next_attempt_at=$3,failure_code=$4,lease_token='',lease_expires_at=NULL,updated_at=now()
		WHERE id=$1 AND status='expiring' AND lease_token=$2`, id, token, next, code)
	if err != nil {
		return fmt.Errorf("ai audit: retry artifact expiry: %w", err)
	}
	if result.RowsAffected() != 1 {
		return ErrConflict
	}
	return nil
}

func nullableJSON(value []byte) any {
	if len(value) == 0 {
		return nil
	}
	return value
}

func jsonEqual(left, right []byte) bool {
	if len(left) == 0 && len(right) == 0 {
		return true
	}
	var a, b any
	return json.Unmarshal(left, &a) == nil && json.Unmarshal(right, &b) == nil && reflect.DeepEqual(a, b)
}
