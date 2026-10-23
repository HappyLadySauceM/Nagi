// PostgreSQL repository for long-term memories.
// 长期记忆的 PostgreSQL 仓库。
package infrastructure

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/HappyLadySauceM/QQBot/internal/memory/domain"
)

// uuidNewString generates a memory identifier.
// uuidNewString 生成记忆标识。
func uuidNewString() string { return uuid.NewString() }

// Repo is the pgx-backed memory repository.
// Repo 是基于 pgx 的记忆仓库。
type Repo struct {
	pool *pgxpool.Pool
}

// NewRepo builds the repository; the pool must not be nil.
// NewRepo 构建仓库；pool 不能为 nil。
func NewRepo(pool *pgxpool.Pool) (*Repo, error) {
	if pool == nil {
		return nil, fmt.Errorf("memory: pool required")
	}
	return &Repo{pool: pool}, nil
}

const memoryColumns = `memory_id, bot_id, visibility, owner_key, content, COALESCE(source_message_id, 0),
	source, confidence, status, approved_by, COALESCE(expires_at, to_timestamp(0)), created_at, updated_at`

// Insert stores one memory row.
// Insert 存储一条记忆行。
func (r *Repo) Insert(ctx context.Context, m domain.Memory) (string, error) {
	id := m.ID
	if id == "" {
		id = uuidNewString()
	}
	var expires any
	if !m.ExpiresAt.IsZero() {
		expires = m.ExpiresAt
	}
	_, err := r.pool.Exec(ctx, `
		INSERT INTO memories (memory_id, bot_id, visibility, owner_key, content, source_message_id,
			source, confidence, status, approved_by, expires_at)
		VALUES ($1, $2, $3, $4, $5, NULLIF($6, 0), $7, $8, $9, $10, $11)
	`, id, m.BotID, string(m.Visibility), m.OwnerKey, m.Content, m.SourceMessageID,
		m.Source, m.Confidence, string(m.Status), m.ApprovedBy, expires)
	if err != nil {
		return "", fmt.Errorf("memory: insert: %w", err)
	}
	return id, nil
}

// Get loads one memory; found is false when it does not exist.
// Get 读取一条记忆；不存在时 found 为 false。
func (r *Repo) Get(ctx context.Context, memoryID string) (domain.Memory, bool, error) {
	row := r.pool.QueryRow(ctx, `
		SELECT `+memoryColumns+` FROM memories WHERE memory_id = $1
	`, memoryID)
	m, err := scanMemory(row.Scan)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Memory{}, false, nil
	}
	if err != nil {
		return domain.Memory{}, false, fmt.Errorf("memory: get: %w", err)
	}
	return m, true, nil
}

// Update writes mutable fields of one memory.
// Update 写入一条记忆的可变字段。
func (r *Repo) Update(ctx context.Context, m domain.Memory) error {
	var expires any
	if !m.ExpiresAt.IsZero() {
		expires = m.ExpiresAt
	}
	_, err := r.pool.Exec(ctx, `
		UPDATE memories SET content = $2, visibility = $3, owner_key = $4, status = $5,
			approved_by = $6, expires_at = $7, updated_at = now()
		WHERE memory_id = $1
	`, m.ID, m.Content, string(m.Visibility), m.OwnerKey, string(m.Status), m.ApprovedBy, expires)
	if err != nil {
		return fmt.Errorf("memory: update: %w", err)
	}
	return nil
}

// SoftDelete marks one memory deleted so it never shows up in retrieval again.
// SoftDelete 将记忆标记为删除，使其不再出现在检索结果中。
func (r *Repo) SoftDelete(ctx context.Context, memoryID string) error {
	_, err := r.pool.Exec(ctx, `
		UPDATE memories SET status = 'deleted', updated_at = now() WHERE memory_id = $1
	`, memoryID)
	if err != nil {
		return fmt.Errorf("memory: soft delete: %w", err)
	}
	return nil
}

// List returns one page of memories matching the filter plus the total count.
// List 返回匹配过滤条件的一页记忆及总数。
func (r *Repo) List(ctx context.Context, f domain.ListFilter) ([]domain.Memory, int64, error) {
	var total int64
	if err := r.pool.QueryRow(ctx, `
		SELECT count(*) FROM memories
		WHERE ($1::bigint IS NULL OR bot_id = $1)
		  AND ($2 = '' OR visibility = $2)
		  AND ($3 = '' OR owner_key = $3)
		  AND ($4 = '' OR status = $4)
		  AND ($5 = '' OR content ILIKE '%' || $5 || '%')
	`, f.BotID, string(f.Visibility), f.OwnerKey, string(f.Status), f.Search,
	).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("memory: count: %w", err)
	}
	rows, err := r.pool.Query(ctx, `
		SELECT `+memoryColumns+` FROM memories
		WHERE ($1::bigint IS NULL OR bot_id = $1)
		  AND ($2 = '' OR visibility = $2)
		  AND ($3 = '' OR owner_key = $3)
		  AND ($4 = '' OR status = $4)
		  AND ($5 = '' OR content ILIKE '%' || $5 || '%')
		ORDER BY updated_at DESC
		LIMIT $6 OFFSET $7
	`, f.BotID, string(f.Visibility), f.OwnerKey, string(f.Status), f.Search, f.Limit, f.Offset())
	if err != nil {
		return nil, 0, fmt.Errorf("memory: list: %w", err)
	}
	defer rows.Close()
	items := []domain.Memory{}
	for rows.Next() {
		m, scanErr := scanMemory(rows.Scan)
		if scanErr != nil {
			return nil, 0, scanErr
		}
		items = append(items, m)
	}
	return items, total, rows.Err()
}

// FindSame looks up an exact content duplicate within the same visibility scope.
// FindSame 在同一可见性范围内查找内容完全一致的重复项。
func (r *Repo) FindSame(ctx context.Context, botID int64, visibility domain.Visibility, ownerKey, content string) (domain.Memory, bool, error) {
	row := r.pool.QueryRow(ctx, `
		SELECT `+memoryColumns+` FROM memories
		WHERE bot_id = $1 AND visibility = $2 AND owner_key = $3 AND content = $4
		  AND status IN ('candidate','active')
		ORDER BY created_at DESC LIMIT 1
	`, botID, string(visibility), ownerKey, content)
	m, err := scanMemory(row.Scan)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Memory{}, false, nil
	}
	if err != nil {
		return domain.Memory{}, false, fmt.Errorf("memory: find same: %w", err)
	}
	return m, true, nil
}

// SearchVisible returns active unexpired memories the conversation may read.
// SearchVisible 返回会话可读取的激活且未过期的记忆。
func (r *Repo) SearchVisible(ctx context.Context, in domain.SearchRequest) ([]domain.Memory, error) {
	scopeVisibility := "group"
	if in.Kind == "private" {
		scopeVisibility = "private_user"
	}
	ownerKey := ""
	if in.TargetID > 0 {
		ownerKey = fmt.Sprintf("%d", in.TargetID)
	}
	rows, err := r.pool.Query(ctx, `
		SELECT `+memoryColumns+` FROM memories
		WHERE bot_id = $1 AND status = 'active'
		  AND (expires_at IS NULL OR expires_at > now())
		  AND (
		    visibility = 'bot_shared'
		    OR ($2 <> '' AND visibility = $3 AND owner_key = $2)
		    OR ($4 <> '' AND visibility = 'conversation' AND owner_key = $4)
		  )
		  AND ($5 = '' OR content ILIKE '%' || $5 || '%')
		ORDER BY confidence DESC, updated_at DESC
		LIMIT $6
	`, in.BotID, ownerKey, scopeVisibility, in.ConversationID, in.Query, in.Limit)
	if err != nil {
		return nil, fmt.Errorf("memory: search visible: %w", err)
	}
	defer rows.Close()
	items := []domain.Memory{}
	for rows.Next() {
		m, scanErr := scanMemory(rows.Scan)
		if scanErr != nil {
			return nil, scanErr
		}
		items = append(items, m)
	}
	return items, rows.Err()
}

type rowScanner func(dest ...any) error

// scanMemory scans the shared memory column order.
// scanMemory 扫描共享的记忆列顺序。
func scanMemory(scan rowScanner) (domain.Memory, error) {
	var m domain.Memory
	var visibility, status string
	if err := scan(&m.ID, &m.BotID, &visibility, &m.OwnerKey, &m.Content, &m.SourceMessageID,
		&m.Source, &m.Confidence, &status, &m.ApprovedBy, &m.ExpiresAt, &m.CreatedAt, &m.UpdatedAt); err != nil {
		return domain.Memory{}, fmt.Errorf("memory: scan: %w", err)
	}
	m.Visibility = domain.Visibility(visibility)
	m.Status = domain.Status(status)
	return m, nil
}
