// PostgreSQL storage for versioned intent rulesets.
// 版本化意图规则的 PostgreSQL 存储。
package infrastructure

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/HappyLadySauceM/Nagi/internal/intent/domain"
)

// ErrRulesetNotFound reports a ruleset id that does not exist.
// ErrRulesetNotFound 表示规则集 id 不存在。
var ErrRulesetNotFound = errors.New("intent: ruleset not found")

// ErrRulesetImmutable reports an edit attempt on a non-draft ruleset.
// ErrRulesetImmutable 表示试图修改非草稿规则集。
var ErrRulesetImmutable = errors.New("intent: ruleset is not a draft")

// RulesetsRepo stores and activates rulesets.
// RulesetsRepo 存储并激活规则集。
type RulesetsRepo struct {
	pool *pgxpool.Pool
}

// NewRulesetsRepo builds the repository; the pool must not be nil.
// NewRulesetsRepo 构建仓储；pool 不能为 nil。
func NewRulesetsRepo(pool *pgxpool.Pool) (*RulesetsRepo, error) {
	if pool == nil {
		return nil, fmt.Errorf("intent: pool required")
	}
	return &RulesetsRepo{pool: pool}, nil
}

const rulesetColumns = `id, bot_id, scope_type, scope_id, version, status, note`

// Active returns the ruleset currently in force for one scope.
// Active 返回某作用域当前生效的规则集。
func (r *RulesetsRepo) Active(ctx context.Context, botID int64, scopeType string, scopeID int64) (domain.Ruleset, bool, error) {
	var set domain.Ruleset
	err := r.pool.QueryRow(ctx, `
		SELECT s.id, s.bot_id, s.scope_type, s.scope_id, s.version, s.status, s.note
		FROM intent_rulesets s
		JOIN intent_rule_activations a ON a.ruleset_id = s.id
		WHERE a.bot_id = $1 AND a.scope_type = $2 AND a.scope_id = $3
	`, botID, scopeType, scopeID).Scan(&set.ID, &set.BotID, &set.ScopeType, &set.ScopeID,
		&set.Version, &set.Status, &set.Note)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Ruleset{}, false, nil
	}
	if err != nil {
		return domain.Ruleset{}, false, fmt.Errorf("intent: load active ruleset: %w", err)
	}
	entries, err := r.entries(ctx, set.ID)
	if err != nil {
		return domain.Ruleset{}, false, err
	}
	set.Entries = entries
	return set, true, nil
}

// List returns every version of one scope, newest first.
// List 返回某作用域的所有版本，最新在前。
func (r *RulesetsRepo) List(ctx context.Context, botID int64, scopeType string, scopeID int64) ([]domain.Ruleset, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT `+rulesetColumns+`
		FROM intent_rulesets
		WHERE bot_id = $1 AND scope_type = $2 AND scope_id = $3
		ORDER BY version DESC
	`, botID, scopeType, scopeID)
	if err != nil {
		return nil, fmt.Errorf("intent: list rulesets: %w", err)
	}
	defer rows.Close()
	var out []domain.Ruleset
	for rows.Next() {
		var set domain.Ruleset
		if err := rows.Scan(&set.ID, &set.BotID, &set.ScopeType, &set.ScopeID,
			&set.Version, &set.Status, &set.Note); err != nil {
			return nil, fmt.Errorf("intent: scan ruleset: %w", err)
		}
		out = append(out, set)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("intent: scan rulesets: %w", err)
	}
	return out, nil
}

// Get loads one ruleset with entries.
// Get 读取一个规则集及其条目。
func (r *RulesetsRepo) Get(ctx context.Context, id int64) (domain.Ruleset, error) {
	var set domain.Ruleset
	err := r.pool.QueryRow(ctx, `SELECT `+rulesetColumns+` FROM intent_rulesets WHERE id = $1`, id).
		Scan(&set.ID, &set.BotID, &set.ScopeType, &set.ScopeID, &set.Version, &set.Status, &set.Note)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Ruleset{}, ErrRulesetNotFound
	}
	if err != nil {
		return domain.Ruleset{}, fmt.Errorf("intent: get ruleset: %w", err)
	}
	entries, err := r.entries(ctx, id)
	if err != nil {
		return domain.Ruleset{}, err
	}
	set.Entries = entries
	return set, nil
}

// CreateDraft creates the next version as a draft, optionally cloning the
// entries of the currently active version.
// CreateDraft 以草稿创建下一个版本，可选择复制当前生效版本的条目。
func (r *RulesetsRepo) CreateDraft(ctx context.Context, botID int64, scopeType string, scopeID int64, note string, cloneActive bool) (domain.Ruleset, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return domain.Ruleset{}, fmt.Errorf("intent: begin draft: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var version int
	if err := tx.QueryRow(ctx, `
		SELECT COALESCE(MAX(version), 0) + 1 FROM intent_rulesets
		WHERE bot_id = $1 AND scope_type = $2 AND scope_id = $3
	`, botID, scopeType, scopeID).Scan(&version); err != nil {
		return domain.Ruleset{}, fmt.Errorf("intent: next version: %w", err)
	}
	var id int64
	if err := tx.QueryRow(ctx, `
		INSERT INTO intent_rulesets (bot_id, scope_type, scope_id, version, status, note)
		VALUES ($1, $2, $3, $4, 'draft', $5) RETURNING id
	`, botID, scopeType, scopeID, version, note).Scan(&id); err != nil {
		return domain.Ruleset{}, fmt.Errorf("intent: create draft: %w", err)
	}
	if cloneActive {
		if _, err := tx.Exec(ctx, `
			INSERT INTO intent_rule_entries (ruleset_id, priority, match, action)
			SELECT $1, e.priority, e.match, e.action
			FROM intent_rule_entries e
			JOIN intent_rule_activations a ON a.ruleset_id = e.ruleset_id
			WHERE a.bot_id = $2 AND a.scope_type = $3 AND a.scope_id = $4
		`, id, botID, scopeType, scopeID); err != nil {
			return domain.Ruleset{}, fmt.Errorf("intent: clone entries: %w", err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.Ruleset{}, fmt.Errorf("intent: commit draft: %w", err)
	}
	return r.Get(ctx, id)
}

// ReplaceEntries rewrites the entries of a draft; published versions are
// immutable so history stays reproducible.
// ReplaceEntries 重写草稿条目；已发布版本不可变，历史因此可复现。
func (r *RulesetsRepo) ReplaceEntries(ctx context.Context, id int64, entries []domain.RuleEntry) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("intent: begin replace entries: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var status string
	if err := tx.QueryRow(ctx, `SELECT status FROM intent_rulesets WHERE id = $1 FOR UPDATE`, id).
		Scan(&status); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrRulesetNotFound
		}
		return fmt.Errorf("intent: lock ruleset: %w", err)
	}
	if status != "draft" {
		return ErrRulesetImmutable
	}
	if _, err := tx.Exec(ctx, `DELETE FROM intent_rule_entries WHERE ruleset_id = $1`, id); err != nil {
		return fmt.Errorf("intent: clear entries: %w", err)
	}
	for _, entry := range entries {
		matchJSON, err := json.Marshal(entry.Match)
		if err != nil {
			return fmt.Errorf("intent: encode match: %w", err)
		}
		actionJSON, err := json.Marshal(entry.Action)
		if err != nil {
			return fmt.Errorf("intent: encode action: %w", err)
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO intent_rule_entries (ruleset_id, priority, match, action)
			VALUES ($1, $2, $3, $4)
		`, id, entry.Priority, matchJSON, actionJSON); err != nil {
			return fmt.Errorf("intent: insert entry: %w", err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("intent: commit entries: %w", err)
	}
	return nil
}

// Publish activates one draft and archives the previously active version.
// Publish 激活一个草稿，并把此前生效的版本归档。
func (r *RulesetsRepo) Publish(ctx context.Context, id int64) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("intent: begin publish: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var set domain.Ruleset
	if err := tx.QueryRow(ctx, `
		SELECT `+rulesetColumns+` FROM intent_rulesets WHERE id = $1 FOR UPDATE
	`, id).Scan(&set.ID, &set.BotID, &set.ScopeType, &set.ScopeID, &set.Version, &set.Status, &set.Note); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrRulesetNotFound
		}
		return fmt.Errorf("intent: lock ruleset: %w", err)
	}
	if set.Status == "published" {
		return nil
	}
	if err := r.archiveActive(ctx, tx, set.BotID, set.ScopeType, set.ScopeID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `
		UPDATE intent_rulesets SET status = 'published', published_at = now() WHERE id = $1
	`, id); err != nil {
		return fmt.Errorf("intent: mark published: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO intent_rule_activations (bot_id, scope_type, scope_id, ruleset_id)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (bot_id, scope_type, scope_id)
		DO UPDATE SET ruleset_id = EXCLUDED.ruleset_id, updated_at = now()
	`, set.BotID, set.ScopeType, set.ScopeID, id); err != nil {
		return fmt.Errorf("intent: activate ruleset: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("intent: commit publish: %w", err)
	}
	return nil
}

// Rollback republishes a historical version.
// Rollback 重新发布一个历史版本。
func (r *RulesetsRepo) Rollback(ctx context.Context, botID int64, scopeType string, scopeID int64, version int) error {
	var id int64
	if err := r.pool.QueryRow(ctx, `
		SELECT id FROM intent_rulesets
		WHERE bot_id = $1 AND scope_type = $2 AND scope_id = $3 AND version = $4
	`, botID, scopeType, scopeID, version).Scan(&id); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrRulesetNotFound
		}
		return fmt.Errorf("intent: find version: %w", err)
	}
	return r.Publish(ctx, id)
}

// archiveActive marks the currently active version archived inside tx.
// archiveActive 在事务内把当前生效版本标记为归档。
func (r *RulesetsRepo) archiveActive(ctx context.Context, tx pgx.Tx, botID int64, scopeType string, scopeID int64) error {
	if _, err := tx.Exec(ctx, `
		UPDATE intent_rulesets SET status = 'archived'
		WHERE id IN (
			SELECT ruleset_id FROM intent_rule_activations
			WHERE bot_id = $1 AND scope_type = $2 AND scope_id = $3
		) AND status = 'published'
	`, botID, scopeType, scopeID); err != nil {
		return fmt.Errorf("intent: archive active: %w", err)
	}
	return nil
}

// entries loads one ruleset's entries ordered by priority.
// entries 按优先级读取规则集条目。
func (r *RulesetsRepo) entries(ctx context.Context, rulesetID int64) ([]domain.RuleEntry, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT priority, match, action FROM intent_rule_entries
		WHERE ruleset_id = $1 ORDER BY priority
	`, rulesetID)
	if err != nil {
		return nil, fmt.Errorf("intent: load entries: %w", err)
	}
	defer rows.Close()
	var out []domain.RuleEntry
	for rows.Next() {
		var (
			entry      domain.RuleEntry
			matchJSON  []byte
			actionJSON []byte
		)
		if err := rows.Scan(&entry.Priority, &matchJSON, &actionJSON); err != nil {
			return nil, fmt.Errorf("intent: scan entry: %w", err)
		}
		if err := json.Unmarshal(matchJSON, &entry.Match); err != nil {
			return nil, fmt.Errorf("intent: decode match: %w", err)
		}
		if err := json.Unmarshal(actionJSON, &entry.Action); err != nil {
			return nil, fmt.Errorf("intent: decode action: %w", err)
		}
		out = append(out, entry)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("intent: scan entries: %w", err)
	}
	return out, nil
}
