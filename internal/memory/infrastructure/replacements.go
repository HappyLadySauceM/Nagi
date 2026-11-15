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
	"github.com/HappyLadySauceM/Nagi/pkg/db"
)

const memoryRowColumns = `memory_id::text, bot_id, visibility, owner_key, content,
	source_message_id, source, source_sender_id, origin, subject_type, subject_id,
	dedupe_key, review_reason, confidence, status, approved_by, expires_at,
	created_at, updated_at`

const replacementColumns = `replacement_id::text, bot_id, new_memory_id::text,
	old_memory_id::text, actor, reason, idempotency_key, created_at`

type rowScanner interface {
	Scan(dest ...any) error
}

type rowQuerier interface {
	QueryRow(ctx context.Context, query string, args ...any) *sql.Row
}

// ApproveMemory changes candidate -> active under the shared memory mutation
// lock and a row lock.
// ApproveMemory 在共享记忆变更锁与行锁下将 candidate 改为 active。
func (r *Repo) ApproveMemory(ctx context.Context, memoryID, actor string) error {
	botID, err := r.memoryBotID(ctx, memoryID)
	if err != nil {
		return err
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if err := LockMemoryMutation(ctx, tx, botID); err != nil {
		return err
	}
	var status string
	err = tx.QueryRow(ctx, `SELECT status FROM memories WHERE memory_id=$1::uuid FOR UPDATE`, memoryID).Scan(&status)
	if errors.Is(err, sql.ErrNoRows) {
		return memoryapp.ErrMemoryNotFound
	}
	if err != nil {
		return err
	}
	if status != string(domain.StatusCandidate) {
		return memoryapp.ErrMemoryStatusConflict
	}
	result, err := tx.Exec(ctx, `
		UPDATE memories
		SET status='active', approved_by=$1, updated_at=clock_timestamp()
		WHERE memory_id=$2::uuid AND status='candidate'`, actor, memoryID)
	if err != nil {
		return err
	}
	if result.RowsAffected() != 1 {
		return memoryapp.ErrMemoryStatusConflict
	}
	return tx.Commit(ctx)
}

// ApproveMemoryWithEdit applies an edit and candidate -> active in one
// transaction under the shared mutation lock and a row lock.
// ApproveMemoryWithEdit 在共享变更锁与行锁下，于同一事务应用编辑与
// candidate -> active。
func (r *Repo) ApproveMemoryWithEdit(ctx context.Context, memoryID, actor, content string, visibility domain.Visibility, ownerKey *string) error {
	if strings.TrimSpace(actor) == "" {
		return fmt.Errorf("memory: approver is required")
	}
	botID, err := r.memoryBotID(ctx, memoryID)
	if err != nil {
		return err
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if err := LockMemoryMutation(ctx, tx, botID); err != nil {
		return err
	}
	memory, err := scanMemoryRow(tx.QueryRow(ctx, `SELECT `+memoryRowColumns+` FROM memories
		WHERE memory_id=$1::uuid FOR UPDATE`, memoryID))
	if errors.Is(err, sql.ErrNoRows) {
		return memoryapp.ErrMemoryNotFound
	}
	if err != nil {
		return err
	}
	if memory.Status != domain.StatusCandidate {
		return memoryapp.ErrMemoryStatusConflict
	}
	if trimmed := strings.TrimSpace(content); trimmed != "" {
		memory.Content = trimmed
	}
	if visibility != "" {
		memory.Visibility = visibility
	}
	if ownerKey != nil {
		memory.OwnerKey = *ownerKey
	}
	if memory.SubjectType != "" && memory.SubjectID != "" {
		memory.DedupeKey = domain.ComputeDedupeKey(memory.BotID, memory.Visibility, memory.OwnerKey, memory.SubjectType, memory.SubjectID, memory.Content)
	}
	if err := memory.Validate(); err != nil {
		return fmt.Errorf("%w: %v", memoryapp.ErrMemoryInvalid, err)
	}
	result, err := tx.Exec(ctx, `
		UPDATE memories SET
			content=$1, visibility=$2, owner_key=$3, status='active', approved_by=$4,
			origin=$5, review_reason=$6, source_sender_id=$7,
			subject_type=$8, subject_id=$9, dedupe_key=$10,
			expires_at=$11, updated_at=clock_timestamp()
		WHERE memory_id=$12::uuid AND status='candidate'`,
		memory.Content, string(memory.Visibility), memory.OwnerKey, actor,
		string(memory.Origin), memory.ReviewReason, memoryNullableInt64(memory.SourceSenderID),
		memoryNullableString(string(memory.SubjectType)), memoryNullableString(memory.SubjectID), memoryNullableString(memory.DedupeKey),
		memoryNullableTime(memory.ExpiresAt), memoryID)
	if err != nil {
		return err
	}
	if result.RowsAffected() != 1 {
		return memoryapp.ErrMemoryStatusConflict
	}
	return tx.Commit(ctx)
}

// UpdateMemoryLocked applies an edited memory row under the shared mutation
// lock and rejects edits after supersede/delete.
// UpdateMemoryLocked 在共享变更锁下应用编辑后的记忆行，并拒绝替代/删除后的编辑。
func (r *Repo) UpdateMemoryLocked(ctx context.Context, memory domain.Memory) error {
	if memory.BotID <= 0 {
		return fmt.Errorf("memory: mutation bot id must be positive")
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if err := LockMemoryMutation(ctx, tx, memory.BotID); err != nil {
		return err
	}
	var status string
	err = tx.QueryRow(ctx, `SELECT status FROM memories WHERE memory_id=$1::uuid FOR UPDATE`, memory.ID).Scan(&status)
	if errors.Is(err, sql.ErrNoRows) {
		return memoryapp.ErrMemoryNotFound
	}
	if err != nil {
		return err
	}
	if status == string(domain.StatusSuperseded) || status == string(domain.StatusDeleted) {
		return memoryapp.ErrMemoryStatusConflict
	}
	result, err := tx.Exec(ctx, `
		UPDATE memories SET
			content=$1, visibility=$2, owner_key=$3,
			origin=$4, review_reason=$5, source_sender_id=$6,
			subject_type=$7, subject_id=$8, dedupe_key=$9,
			expires_at=$10, updated_at=clock_timestamp()
		WHERE memory_id=$11::uuid`,
		memory.Content, string(memory.Visibility), memory.OwnerKey,
		string(memory.Origin), memory.ReviewReason, memoryNullableInt64(memory.SourceSenderID),
		memoryNullableString(string(memory.SubjectType)), memoryNullableString(memory.SubjectID), memoryNullableString(memory.DedupeKey),
		memoryNullableTime(memory.ExpiresAt), memory.ID)
	if err != nil {
		return err
	}
	if result.RowsAffected() != 1 {
		return memoryapp.ErrMemoryNotFound
	}
	return tx.Commit(ctx)
}

// DeleteMemory physically removes one memory under the shared mutation lock.
// DeleteMemory 在共享变更锁下物理删除一条记忆。
func (r *Repo) DeleteMemory(ctx context.Context, memoryID string) (bool, error) {
	botID, err := r.memoryBotID(ctx, memoryID)
	if errors.Is(err, memoryapp.ErrMemoryNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer tx.Rollback(ctx)
	if err := LockMemoryMutation(ctx, tx, botID); err != nil {
		return false, err
	}
	var status string
	err = tx.QueryRow(ctx, `SELECT status FROM memories WHERE memory_id=$1::uuid FOR UPDATE`, memoryID).Scan(&status)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	result, err := tx.Exec(ctx, `DELETE FROM memories WHERE memory_id=$1::uuid`, memoryID)
	if err != nil {
		return false, err
	}
	if result.RowsAffected() != 1 {
		return false, nil
	}
	if err := tx.Commit(ctx); err != nil {
		return false, err
	}
	return true, nil
}

// InsertCandidateLocked inserts one candidate after a duplicate lookup under
// the shared memory mutation lock.
// InsertCandidateLocked 在共享记忆变更锁下完成去重查询并插入一条候选。
func (r *Repo) InsertCandidateLocked(ctx context.Context, memory domain.Memory) (string, bool, error) {
	if memory.BotID <= 0 {
		return "", false, fmt.Errorf("memory: mutation bot id must be positive")
	}
	if err := memory.Validate(); err != nil {
		return "", false, err
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return "", false, err
	}
	defer tx.Rollback(ctx)
	if err := LockMemoryMutation(ctx, tx, memory.BotID); err != nil {
		return "", false, err
	}
	var existingID string
	if memory.DedupeKey != "" {
		err = tx.QueryRow(ctx, `SELECT memory_id::text FROM memories
			WHERE dedupe_key=$1 AND status IN ('candidate','active')
			ORDER BY created_at LIMIT 1`, memory.DedupeKey).Scan(&existingID)
	} else {
		err = tx.QueryRow(ctx, `SELECT memory_id::text FROM memories
			WHERE bot_id=$1 AND visibility=$2 AND owner_key=$3 AND content=$4
				AND status IN ('candidate','active')
			ORDER BY created_at DESC LIMIT 1`,
			memory.BotID, string(memory.Visibility), memory.OwnerKey, memory.Content).Scan(&existingID)
	}
	if err == nil {
		return existingID, true, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return "", false, err
	}
	id := memory.ID
	if id == "" {
		id = uuid.NewString()
	} else if _, err := uuid.Parse(id); err != nil {
		return "", false, err
	}
	origin := memory.Origin
	if origin == "" {
		origin = domain.OriginManual
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO memories
			(memory_id, bot_id, visibility, owner_key, content, source_message_id, source,
			 source_sender_id, origin, subject_type, subject_id, dedupe_key, review_reason,
			 confidence, status, approved_by, expires_at)
		VALUES ($1::uuid,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,'candidate','',$15)`,
		id, memory.BotID, string(memory.Visibility), memory.OwnerKey, memory.Content,
		memoryNullableInt64(memory.SourceMessageID), memory.Source,
		memoryNullableInt64(memory.SourceSenderID), string(origin),
		memoryNullableString(string(memory.SubjectType)), memoryNullableString(memory.SubjectID),
		memoryNullableString(memory.DedupeKey), memory.ReviewReason, memory.Confidence,
		memoryNullableTime(memory.ExpiresAt)); err != nil {
		return "", false, err
	}
	if err := tx.Commit(ctx); err != nil {
		return "", false, err
	}
	return id, false, nil
}

// Supersede performs the explicit candidate -> active / old active ->
// superseded transaction with idempotency handling.
// Supersede 执行显式 candidate -> active / 旧 active -> superseded 事务，并处理幂等。
func (r *Repo) Supersede(ctx context.Context, in domain.SupersedeInput) (domain.SupersedeResult, error) {
	var botID int64
	err := r.pool.QueryRow(ctx, `SELECT bot_id FROM memories WHERE memory_id=$1::uuid`, in.NewMemoryID).Scan(&botID)
	if errors.Is(err, sql.ErrNoRows) {
		return r.supersedeWithoutCandidate(ctx, in)
	}
	if err != nil {
		return domain.SupersedeResult{}, err
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return domain.SupersedeResult{}, err
	}
	defer tx.Rollback(ctx)
	if err := LockMemoryMutation(ctx, tx, botID); err != nil {
		return domain.SupersedeResult{}, err
	}
	existing, found, err := findReplacementByPair(ctx, tx, in.NewMemoryID, in.OldMemoryID)
	if err != nil {
		return domain.SupersedeResult{}, err
	}
	if found {
		if existing.IdempotencyKey != in.IdempotencyKey {
			return domain.SupersedeResult{}, memoryapp.ErrIdempotencyConflict
		}
		return replacementResult(ctx, tx, existing, true)
	}
	if _, found, err := findReplacementByKey(ctx, tx, botID, in.IdempotencyKey); err != nil {
		return domain.SupersedeResult{}, err
	} else if found {
		return domain.SupersedeResult{}, memoryapp.ErrIdempotencyConflict
	}

	memories, err := lockMemoryRows(ctx, tx, in.NewMemoryID, in.OldMemoryID)
	if err != nil {
		return domain.SupersedeResult{}, err
	}
	if len(memories) != 2 {
		return domain.SupersedeResult{}, memoryapp.ErrMemoryNotFound
	}
	newMemory := memories[in.NewMemoryID]
	oldMemory := memories[in.OldMemoryID]
	if newMemory.Status != domain.StatusCandidate || oldMemory.Status != domain.StatusActive {
		return domain.SupersedeResult{}, memoryapp.ErrMemoryStatusConflict
	}
	if newMemory.BotID != oldMemory.BotID || newMemory.Visibility != oldMemory.Visibility || newMemory.OwnerKey != oldMemory.OwnerKey {
		return domain.SupersedeResult{}, memoryapp.ErrSubjectMismatch
	}
	if !knownSubject(newMemory) || !knownSubject(oldMemory) {
		return domain.SupersedeResult{}, memoryapp.ErrSubjectAmbiguous
	}
	if newMemory.SubjectType != oldMemory.SubjectType || newMemory.SubjectID != oldMemory.SubjectID {
		return domain.SupersedeResult{}, memoryapp.ErrSubjectMismatch
	}

	var relation domain.Replacement
	relation.ReplacementID = uuid.NewString()
	relation.BotID = botID
	relation.NewMemoryID = in.NewMemoryID
	relation.OldMemoryID = in.OldMemoryID
	relation.Actor = in.Actor
	relation.Reason = in.Reason
	relation.IdempotencyKey = in.IdempotencyKey
	if err := tx.QueryRow(ctx, `
		INSERT INTO memory_replacements
			(replacement_id, bot_id, new_memory_id, old_memory_id, actor, reason, idempotency_key, created_at)
		VALUES ($1::uuid,$2,$3::uuid,$4::uuid,$5,$6,$7,clock_timestamp())
		RETURNING created_at`, relation.ReplacementID, relation.BotID, relation.NewMemoryID,
		relation.OldMemoryID, relation.Actor, relation.Reason, relation.IdempotencyKey).Scan(&relation.CreatedAt); err != nil {
		return domain.SupersedeResult{}, err
	}
	if err := updateMemoryStatus(ctx, tx, in.NewMemoryID, string(domain.StatusCandidate), string(domain.StatusActive), in.Actor); err != nil {
		return domain.SupersedeResult{}, err
	}
	if err := updateMemoryStatus(ctx, tx, in.OldMemoryID, string(domain.StatusActive), string(domain.StatusSuperseded), ""); err != nil {
		return domain.SupersedeResult{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.SupersedeResult{}, err
	}
	return domain.SupersedeResult{
		NewMemoryID: relation.NewMemoryID,
		OldMemoryID: relation.OldMemoryID,
		NewStatus:   domain.StatusActive,
		OldStatus:   domain.StatusSuperseded,
		ReplacedAt:  relation.CreatedAt,
		ReplacedBy:  relation.Actor,
		Idempotent:  false,
	}, nil
}

func (r *Repo) supersedeWithoutCandidate(ctx context.Context, in domain.SupersedeInput) (domain.SupersedeResult, error) {
	existing, found, err := findReplacementByPair(ctx, r.pool, in.NewMemoryID, in.OldMemoryID)
	if err != nil {
		return domain.SupersedeResult{}, err
	}
	if found {
		if existing.IdempotencyKey != in.IdempotencyKey {
			return domain.SupersedeResult{}, memoryapp.ErrIdempotencyConflict
		}
		return replacementResult(ctx, r.pool, existing, true)
	}
	// Without the candidate row there is no bot scope for an idempotency-key
	// lookup; the contract only allows returning the existing pair or not-found.
	// 候选行不存在时没有可用的 bot 作用域做幂等键查询；契约只允许返回已有关系或未找到。
	return domain.SupersedeResult{}, memoryapp.ErrMemoryNotFound
}

// Management returns one memory and all replacement relations touching it.
// Management 返回一条记忆及所有与其相关的替代关系。
func (r *Repo) Management(ctx context.Context, memoryID string, botID int64) (domain.Memory, []domain.ReplacementView, bool, error) {
	memory, found, err := r.Get(ctx, memoryID)
	if err != nil || !found || memory.BotID != botID {
		return domain.Memory{}, nil, false, err
	}
	rows, err := r.pool.Query(ctx, `
		SELECT r.replacement_id::text, r.new_memory_id::text, r.old_memory_id::text,
			r.actor, r.reason, r.created_at, n.status, o.status
		FROM memory_replacements r
		LEFT JOIN memories n ON n.memory_id=r.new_memory_id
		LEFT JOIN memories o ON o.memory_id=r.old_memory_id
		WHERE r.bot_id=$1 AND (r.new_memory_id=$2::uuid OR r.old_memory_id=$2::uuid)
		ORDER BY r.created_at, r.replacement_id`, botID, memoryID)
	if err != nil {
		return domain.Memory{}, nil, false, err
	}
	defer rows.Close()
	out := make([]domain.ReplacementView, 0)
	for rows.Next() {
		var view domain.ReplacementView
		var newStatus, oldStatus sql.NullString
		if err := rows.Scan(&view.ReplacementID, &view.NewMemoryID, &view.OldMemoryID,
			&view.Actor, &view.Reason, &view.CreatedAt, &newStatus, &oldStatus); err != nil {
			return domain.Memory{}, nil, false, err
		}
		view.NewStatus = statusOrDeleted(newStatus)
		view.OldStatus = statusOrDeleted(oldStatus)
		if view.NewMemoryID == memoryID {
			view.Direction = "as_new"
			view.Counterpart = domain.ReplacementCounterpart{MemoryID: view.OldMemoryID, Status: statusOrEmpty(oldStatus), Missing: !oldStatus.Valid}
		} else {
			view.Direction = "as_old"
			view.Counterpart = domain.ReplacementCounterpart{MemoryID: view.NewMemoryID, Status: statusOrEmpty(newStatus), Missing: !newStatus.Valid}
		}
		out = append(out, view)
	}
	if err := rows.Err(); err != nil {
		return domain.Memory{}, nil, false, err
	}
	return memory, out, true, nil
}

func (r *Repo) memoryBotID(ctx context.Context, memoryID string) (int64, error) {
	var botID int64
	err := r.pool.QueryRow(ctx, `SELECT bot_id FROM memories WHERE memory_id=$1::uuid`, memoryID).Scan(&botID)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, memoryapp.ErrMemoryNotFound
	}
	if err != nil {
		return 0, err
	}
	return botID, nil
}

func lockMemoryRows(ctx context.Context, tx *db.Tx, ids ...string) (map[string]domain.Memory, error) {
	rows, err := tx.Query(ctx, `SELECT `+memoryRowColumns+` FROM memories
		WHERE memory_id IN ($1::uuid,$2::uuid) ORDER BY memory_id FOR UPDATE`, ids[0], ids[1])
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make(map[string]domain.Memory, len(ids))
	for rows.Next() {
		memory, err := scanMemoryRow(rows)
		if err != nil {
			return nil, err
		}
		out[memory.ID] = memory
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

func updateMemoryStatus(ctx context.Context, tx *db.Tx, id, from, to, actor string) error {
	result, err := tx.Exec(ctx, `
		UPDATE memories SET status=$1, approved_by=CASE WHEN $2 <> '' THEN $2 ELSE approved_by END,
			updated_at=clock_timestamp()
		WHERE memory_id=$3::uuid AND status=$4`, to, actor, id, from)
	if err != nil {
		return err
	}
	if result.RowsAffected() != 1 {
		return memoryapp.ErrMemoryStatusConflict
	}
	return nil
}

func findReplacementByPair(ctx context.Context, q rowQuerier, newID, oldID string) (domain.Replacement, bool, error) {
	return scanReplacement(q.QueryRow(ctx, `SELECT `+replacementColumns+` FROM memory_replacements
		WHERE new_memory_id=$1::uuid AND old_memory_id=$2::uuid`, newID, oldID))
}

func findReplacementByKey(ctx context.Context, q rowQuerier, botID int64, key string) (domain.Replacement, bool, error) {
	query := `SELECT ` + replacementColumns + ` FROM memory_replacements WHERE bot_id=$1 AND idempotency_key=$2`
	args := []any{botID, key}
	if botID <= 0 {
		query = `SELECT ` + replacementColumns + ` FROM memory_replacements WHERE idempotency_key=$1`
		args = []any{key}
	}
	return scanReplacement(q.QueryRow(ctx, query, args...))
}

func scanReplacement(row rowScanner) (domain.Replacement, bool, error) {
	var out domain.Replacement
	err := row.Scan(&out.ReplacementID, &out.BotID, &out.NewMemoryID, &out.OldMemoryID,
		&out.Actor, &out.Reason, &out.IdempotencyKey, &out.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.Replacement{}, false, nil
	}
	if err != nil {
		return domain.Replacement{}, false, err
	}
	return out, true, nil
}

func replacementResult(ctx context.Context, q rowQuerier, relation domain.Replacement, idempotent bool) (domain.SupersedeResult, error) {
	newStatus, err := memoryStatus(ctx, q, relation.NewMemoryID)
	if err != nil {
		return domain.SupersedeResult{}, err
	}
	oldStatus, err := memoryStatus(ctx, q, relation.OldMemoryID)
	if err != nil {
		return domain.SupersedeResult{}, err
	}
	return domain.SupersedeResult{
		NewMemoryID: relation.NewMemoryID,
		OldMemoryID: relation.OldMemoryID,
		NewStatus:   newStatus,
		OldStatus:   oldStatus,
		ReplacedAt:  relation.CreatedAt,
		ReplacedBy:  relation.Actor,
		Idempotent:  idempotent,
	}, nil
}

func memoryStatus(ctx context.Context, q rowQuerier, id string) (domain.Status, error) {
	var status string
	err := q.QueryRow(ctx, `SELECT status FROM memories WHERE memory_id=$1::uuid`, id).Scan(&status)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.StatusDeleted, nil
	}
	if err != nil {
		return "", err
	}
	return domain.Status(status), nil
}

func statusOrDeleted(v sql.NullString) domain.Status {
	if !v.Valid {
		return domain.StatusDeleted
	}
	return domain.Status(v.String)
}

func statusOrEmpty(v sql.NullString) domain.Status {
	if !v.Valid {
		return ""
	}
	return domain.Status(v.String)
}

func knownSubject(memory domain.Memory) bool {
	return (memory.SubjectType == domain.SubjectUser || memory.SubjectType == domain.SubjectGroup) && strings.TrimSpace(memory.SubjectID) != ""
}

func scanMemoryRow(row rowScanner) (domain.Memory, error) {
	var memory domain.Memory
	var visibility, origin, status string
	var sourceMessageID, sourceSenderID sql.NullInt64
	var subjectType, subjectID, dedupeKey sql.NullString
	var expiresAt sql.NullTime
	var confidence float64
	err := row.Scan(&memory.ID, &memory.BotID, &visibility, &memory.OwnerKey, &memory.Content,
		&sourceMessageID, &memory.Source, &sourceSenderID, &origin, &subjectType, &subjectID,
		&dedupeKey, &memory.ReviewReason, &confidence, &status, &memory.ApprovedBy, &expiresAt,
		&memory.CreatedAt, &memory.UpdatedAt)
	if err != nil {
		return domain.Memory{}, err
	}
	memory.Visibility = domain.Visibility(visibility)
	memory.Origin = domain.Origin(origin)
	memory.Status = domain.Status(status)
	memory.Confidence = confidence
	if sourceMessageID.Valid {
		memory.SourceMessageID = sourceMessageID.Int64
	}
	if sourceSenderID.Valid {
		memory.SourceSenderID = sourceSenderID.Int64
	}
	if subjectType.Valid {
		memory.SubjectType = domain.SubjectType(subjectType.String)
	}
	if subjectID.Valid {
		memory.SubjectID = subjectID.String
	}
	if dedupeKey.Valid {
		memory.DedupeKey = dedupeKey.String
	}
	if expiresAt.Valid {
		memory.ExpiresAt = expiresAt.Time
	}
	return memory, nil
}

func memoryNullableString(value string) any {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	return value
}

func memoryNullableInt64(value int64) any {
	if value == 0 {
		return nil
	}
	return value
}

func memoryNullableTime(value time.Time) any {
	if value.IsZero() {
		return nil
	}
	return value
}
