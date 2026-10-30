// Package infrastructure implements the scope repository on PostgreSQL.
// scope/infrastructure 在 PostgreSQL 上实现作用域仓库。
package infrastructure

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/HappyLadySauceM/Nagi/internal/scope/domain"
)

// Repo is the pgx-backed scope policy repository.
// Repo 是基于 pgx 的作用域策略仓库。
type Repo struct {
	pool *pgxpool.Pool
}

// NewRepo builds the repository; the pool must not be nil.
// NewRepo 构建仓库；pool 不能为 nil。
func NewRepo(pool *pgxpool.Pool) (*Repo, error) {
	if pool == nil {
		return nil, fmt.Errorf("scope: pool required")
	}
	return &Repo{pool: pool}, nil
}

const policyColumns = `enabled, reply_mode, store_messages, allow_memory_write, allow_history_read,
	min_reply_interval_ms, max_replies_per_hour, tool_permissions, version, max_messages_per_run, max_chars_per_message, max_total_chars_per_run, message_interval_ms`

// Get loads one stored policy; found is false when the scope has no row.
// Get 读取单条已存储策略；作用域无行时 found 为 false。
func (r *Repo) Get(ctx context.Context, botID int64, scopeType domain.ScopeType, scopeID int64) (domain.Policy, bool, error) {
	row := r.pool.QueryRow(ctx, `
		SELECT `+policyColumns+` FROM scope_policies
		WHERE bot_id = $1 AND scope_type = $2 AND scope_id = $3
	`, botID, string(scopeType), scopeID)
	p, err := scanPolicy(row.Scan)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Policy{}, false, nil
	}
	if err != nil {
		return domain.Policy{}, false, fmt.Errorf("scope: get policy: %w", err)
	}
	// scanPolicy only reads the value columns; the identity comes from the query
	// arguments so callers never receive a policy without its scope.
	// scanPolicy 只读取值列；身份来自查询参数，确保调用方拿到的策略始终带作用域身份。
	p.ScopeType = scopeType
	p.ScopeID = scopeID
	return p, true, nil
}

// List returns all stored policies, optionally filtered by bot.
// List 返回全部已存储策略，可按机器人过滤。
func (r *Repo) List(ctx context.Context, botID *int64) ([]domain.Policy, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT bot_id, scope_type, scope_id, `+policyColumns+`
		FROM scope_policies
		WHERE ($1::bigint IS NULL OR bot_id = $1)
		ORDER BY scope_type, scope_id
	`, botID)
	if err != nil {
		return nil, fmt.Errorf("scope: list policies: %w", err)
	}
	defer rows.Close()
	policies := []domain.Policy{}
	for rows.Next() {
		var scopeType string
		var scopeID int64
		// scanPolicy returns the scanned value columns; the identity columns are
		// prepended here and applied afterwards.
		// scanPolicy 返回扫描到的值列；身份列在此前置换入，随后补齐到策略上。
		p, scanErr := scanPolicy(func(dest ...any) error {
			return rows.Scan(append([]any{new(int64), &scopeType, &scopeID}, dest...)...)
		})
		if scanErr != nil {
			return nil, fmt.Errorf("scope: scan policy: %w", scanErr)
		}
		p.ScopeType = domain.ScopeType(scopeType)
		p.ScopeID = scopeID
		policies = append(policies, p)
	}
	return policies, rows.Err()
}

type rowScanner func(dest ...any) error

// scanPolicy scans the policy value columns shared by Get and List.
// scanPolicy 扫描 Get 与 List 共用的策略值列。
func scanPolicy(scan rowScanner) (domain.Policy, error) {
	var p domain.Policy
	var replyMode string
	var rawPermissions []byte
	dest := []any{&p.Enabled, &replyMode, &p.StoreMessages, &p.AllowMemoryWrite, &p.AllowHistoryRead,
		&p.MinReplyIntervalMS, &p.MaxRepliesPerHour, &rawPermissions, &p.Version, &p.MaxMessagesPerRun, &p.MaxCharsPerMessage, &p.MaxTotalCharsPerRun, &p.MessageIntervalMS}
	if err := scan(dest...); err != nil {
		return domain.Policy{}, err
	}
	permissions, err := decodeToolPermissions(rawPermissions)
	if err != nil {
		return domain.Policy{}, err
	}
	p.ReplyMode = domain.ReplyMode(replyMode)
	p.ToolPermissions = permissions
	return p, nil
}

// decodeToolPermissions converts the stored JSONB overrides; an empty object
// normalises to nil so callers can distinguish "no overrides" from explicit ones.
// decodeToolPermissions 把已存 JSONB 覆盖项转换为映射；空对象归一化为 nil，
// 使调用方能区分"无覆盖"与显式覆盖。
func decodeToolPermissions(raw []byte) (map[string]bool, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	var overrides map[string]bool
	if err := json.Unmarshal(raw, &overrides); err != nil {
		return nil, fmt.Errorf("scope: decode tool permissions: %w", err)
	}
	if len(overrides) == 0 {
		return nil, nil
	}
	return overrides, nil
}

// encodeToolPermissions renders the overrides as JSONB, always emitting an
// object so the column default and the write path stay interchangeable.
// encodeToolPermissions 把覆盖项渲染为 JSONB，始终输出对象，
// 使列默认值与写入路径可以互换。
func encodeToolPermissions(overrides map[string]bool) ([]byte, error) {
	if len(overrides) == 0 {
		return []byte("{}"), nil
	}
	encoded, err := json.Marshal(overrides)
	if err != nil {
		return nil, fmt.Errorf("scope: encode tool permissions: %w", err)
	}
	return encoded, nil
}

// Upsert stores one policy row.
// Upsert 存储一条策略行。
func (r *Repo) Upsert(ctx context.Context, botID int64, policy domain.Policy) error {
	policy = policy.WithDefaults()
	permissions, err := encodeToolPermissions(policy.ToolPermissions)
	if err != nil {
		return err
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, "SELECT pg_advisory_xact_lock(hashtextextended($1, 0))", fmt.Sprintf("scope:%d:%s", botID, policy.ScopeType)); err != nil {
		return err
	}
	if policy.ExpectedTemplateVersion != nil {
		var version int64
		err = tx.QueryRow(ctx, "SELECT version FROM scope_policies WHERE bot_id=$1 AND scope_type=$2 AND scope_id=0", botID, string(policy.ScopeType)).Scan(&version)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		if version != *policy.ExpectedTemplateVersion {
			return domain.ErrTemplateConflict
		}
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO scope_policies
			(bot_id, scope_type, scope_id, enabled, reply_mode, store_messages, allow_memory_write,
			 allow_history_read, min_reply_interval_ms, max_replies_per_hour,
			 tool_permissions, max_messages_per_run, max_chars_per_message, max_total_chars_per_run, message_interval_ms, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, now())
		ON CONFLICT (bot_id, scope_type, scope_id) DO UPDATE SET
			enabled = EXCLUDED.enabled, reply_mode = EXCLUDED.reply_mode,
			store_messages = EXCLUDED.store_messages, allow_memory_write = EXCLUDED.allow_memory_write,
			allow_history_read = EXCLUDED.allow_history_read,
			min_reply_interval_ms = EXCLUDED.min_reply_interval_ms,
			max_replies_per_hour = EXCLUDED.max_replies_per_hour,
			max_messages_per_run = EXCLUDED.max_messages_per_run, max_chars_per_message = EXCLUDED.max_chars_per_message, max_total_chars_per_run = EXCLUDED.max_total_chars_per_run, message_interval_ms = EXCLUDED.message_interval_ms, tool_permissions = EXCLUDED.tool_permissions, version = scope_policies.version + 1, updated_at = now()
	`, botID, string(policy.ScopeType), policy.ScopeID, policy.Enabled, string(policy.ReplyMode),
		policy.StoreMessages, policy.AllowMemoryWrite, policy.AllowHistoryRead,
		policy.MinReplyIntervalMS, policy.MaxRepliesPerHour, permissions, policy.MaxMessagesPerRun, policy.MaxCharsPerMessage, policy.MaxTotalCharsPerRun, policy.MessageIntervalMS)
	if err != nil {
		return fmt.Errorf("scope: upsert policy: %w", err)
	}
	return tx.Commit(ctx)
}

// Delete removes one stored policy and reports whether a row was deleted.
// Delete 删除一条已存策略并报告是否删除了行。
func (r *Repo) Delete(ctx context.Context, botID int64, scopeType domain.ScopeType, scopeID int64) (bool, error) {
	tag, err := r.pool.Exec(ctx, `
		DELETE FROM scope_policies
		WHERE bot_id = $1 AND scope_type = $2 AND scope_id = $3
	`, botID, string(scopeType), scopeID)
	if err != nil {
		return false, fmt.Errorf("scope: delete policy: %w", err)
	}
	return tag.RowsAffected() > 0, nil
}
