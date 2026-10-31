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
var ErrTargetConflict = errors.New("intent: target already bound to another ruleset")

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

const rulesetColumns = `id, bot_id, scope_type, scope_id, version, status, note, builtin_rules, targets`

// Active returns the ruleset currently in force for one scope.
// Active 返回某作用域当前生效的规则集。
func (r *RulesetsRepo) Active(ctx context.Context, botID int64, scopeType string, scopeID int64) (domain.Ruleset, bool, error) {
	var set domain.Ruleset
	err := r.pool.QueryRow(ctx, `
		SELECT s.id, s.bot_id, s.scope_type, s.scope_id, s.version, s.status, s.note, s.builtin_rules, s.targets
		FROM intent_rulesets s
		JOIN intent_rule_activations a ON a.ruleset_id = s.id
		WHERE s.deleted_at IS NULL AND a.bot_id = $1 AND a.scope_type = $2 AND a.scope_id = $3
	`, botID, scopeType, scopeID).Scan(&set.ID, &set.BotID, &set.ScopeType, &set.ScopeID,
		&set.Version, &set.Status, &set.Note, &set.BuiltinRules, &set.Targets)
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
		WHERE deleted_at IS NULL AND bot_id = $1 AND ($2 = '' OR (scope_type = $2 AND scope_id = $3))
		ORDER BY id DESC
	`, botID, scopeType, scopeID)
	if err != nil {
		return nil, fmt.Errorf("intent: list rulesets: %w", err)
	}
	defer rows.Close()
	var out []domain.Ruleset
	for rows.Next() {
		var set domain.Ruleset
		if err := rows.Scan(&set.ID, &set.BotID, &set.ScopeType, &set.ScopeID,
			&set.Version, &set.Status, &set.Note, &set.BuiltinRules, &set.Targets); err != nil {
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
	err := r.pool.QueryRow(ctx, `SELECT `+rulesetColumns+` FROM intent_rulesets WHERE deleted_at IS NULL AND id = $1`, id).
		Scan(&set.ID, &set.BotID, &set.ScopeType, &set.ScopeID, &set.Version, &set.Status, &set.Note, &set.BuiltinRules, &set.Targets)
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
func (r *RulesetsRepo) CreateDraft(ctx context.Context, botID int64, scopeType string, scopeID int64, note string, cloneActive bool, cloneFrom ...int64) (domain.Ruleset, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return domain.Ruleset{}, fmt.Errorf("intent: begin draft: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := tx.Exec(ctx, "SELECT pg_advisory_xact_lock(hashtextextended($1,0))", fmt.Sprintf("intent:%d", botID)); err != nil {
		return domain.Ruleset{}, err
	}
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
	if scopeID > 0 {
		if _, err := tx.Exec(ctx, "UPDATE intent_rulesets SET targets=jsonb_build_array(jsonb_build_object('kind',$2::text,'id',$3::bigint)) WHERE id=$1", id, scopeType, scopeID); err != nil {
			return domain.Ruleset{}, err
		}
	}
	if cloneActive || (len(cloneFrom) > 0 && cloneFrom[0] > 0) {
		var sourceID int64
		if len(cloneFrom) > 0 && cloneFrom[0] > 0 {
			err = tx.QueryRow(ctx, "SELECT id FROM intent_rulesets WHERE deleted_at IS NULL AND id=$1 AND bot_id=$2 AND scope_type=$3 AND scope_id=$4 AND status IN ('draft','published','archived') FOR SHARE", cloneFrom[0], botID, scopeType, scopeID).Scan(&sourceID)
		} else {
			err = tx.QueryRow(ctx, "SELECT id FROM intent_rulesets WHERE bot_id=$1 AND scope_type=$2 AND scope_id=$3 AND deleted_at IS NULL AND status='published' ORDER BY version DESC LIMIT 1", botID, scopeType, scopeID).Scan(&sourceID)
		}
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return domain.Ruleset{}, err
		}
		if len(cloneFrom) > 0 && cloneFrom[0] > 0 && errors.Is(err, pgx.ErrNoRows) {
			return domain.Ruleset{}, ErrRulesetNotFound
		}
		if sourceID == id {
			return domain.Ruleset{}, ErrRulesetNotFound
		}
		if sourceID > 0 {
			if _, err = tx.Exec(ctx, "UPDATE intent_rulesets target SET targets=source.targets,builtin_rules=source.builtin_rules FROM intent_rulesets source WHERE target.id=$1 AND source.id=$2", id, sourceID); err != nil {
				return domain.Ruleset{}, err
			}
			if _, err = tx.Exec(ctx, "INSERT INTO intent_rule_entries(ruleset_id,priority,match,action,enabled) SELECT $1,priority,match,action,enabled FROM intent_rule_entries WHERE ruleset_id=$2", id, sourceID); err != nil {
				return domain.Ruleset{}, err
			}
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.Ruleset{}, fmt.Errorf("intent: commit draft: %w", err)
	}
	return r.Get(ctx, id)
}

type RuleOptions struct {
	BuiltinRules map[string]bool
	Targets      *[]domain.RuleTarget
}

// ReplaceEntries rewrites the entries of a draft; published versions are
// immutable so history stays reproducible.
// ReplaceEntries 重写草稿条目；已发布版本不可变，历史因此可复现。
func (r *RulesetsRepo) ReplaceEntries(ctx context.Context, id int64, entries []domain.RuleEntry, options ...RuleOptions) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("intent: begin replace entries: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var status string
	if err := tx.QueryRow(ctx, `SELECT status FROM intent_rulesets WHERE deleted_at IS NULL AND id = $1 FOR UPDATE`, id).
		Scan(&status); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrRulesetNotFound
		}
		return fmt.Errorf("intent: lock ruleset: %w", err)
	}
	if status != "draft" {
		return ErrRulesetImmutable
	}
	if len(options) > 0 {
		raw, err := json.Marshal(options[0].BuiltinRules)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, "UPDATE intent_rulesets SET builtin_rules=$2 WHERE id=$1", id, raw); err != nil {
			return err
		}
	}
	if len(options) > 0 && options[0].Targets != nil {
		raw, err := json.Marshal(*options[0].Targets)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, "UPDATE intent_rulesets SET targets=$2 WHERE id=$1", id, raw); err != nil {
			return err
		}
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
			INSERT INTO intent_rule_entries (ruleset_id, priority, match, action, enabled)
			VALUES ($1, $2, $3, $4, $5)
		`, id, entry.Priority, matchJSON, actionJSON, entry.Enabled == nil || *entry.Enabled); err != nil {
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

	var accountID int64
	if err := tx.QueryRow(ctx, "SELECT bot_id FROM intent_rulesets WHERE deleted_at IS NULL AND id=$1", id).Scan(&accountID); err != nil {
		return ErrRulesetNotFound
	}
	if _, err := tx.Exec(ctx, "SELECT pg_advisory_xact_lock(hashtextextended($1,0))", fmt.Sprintf("intent:%d", accountID)); err != nil {
		return err
	}
	var set domain.Ruleset
	if err := tx.QueryRow(ctx, `
		SELECT `+rulesetColumns+` FROM intent_rulesets WHERE deleted_at IS NULL AND id = $1 FOR UPDATE
	`, id).Scan(&set.ID, &set.BotID, &set.ScopeType, &set.ScopeID, &set.Version, &set.Status, &set.Note, &set.BuiltinRules, &set.Targets); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrRulesetNotFound
		}
		return fmt.Errorf("intent: lock ruleset: %w", err)
	}
	if set.Status == "published" {
		return nil
	}
	targets := set.Targets
	if len(targets) == 0 {
		targets = []domain.RuleTarget{{Kind: "group", ID: 0}}
	}
	for _, target := range targets {
		var otherType string
		var otherID int64
		err := tx.QueryRow(ctx, "SELECT s.scope_type,s.scope_id FROM intent_rule_activations a JOIN intent_rulesets s ON s.id=a.ruleset_id WHERE a.bot_id=$1 AND a.scope_type=$2 AND a.scope_id=$3", set.BotID, target.Kind, target.ID).Scan(&otherType, &otherID)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		if err == nil && (otherType != set.ScopeType || otherID != set.ScopeID) {
			return ErrTargetConflict
		}
	}
	if _, err := tx.Exec(ctx, "DELETE FROM intent_rule_activations WHERE ruleset_id IN (SELECT id FROM intent_rulesets WHERE bot_id=$1 AND scope_type=$2 AND scope_id=$3)", set.BotID, set.ScopeType, set.ScopeID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, "UPDATE intent_rulesets SET status='archived' WHERE bot_id=$1 AND scope_type=$2 AND scope_id=$3 AND status='published'", set.BotID, set.ScopeType, set.ScopeID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, "UPDATE intent_rulesets SET status='published',published_at=now() WHERE id=$1", id); err != nil {
		return err
	}
	for _, target := range targets {
		if _, err := tx.Exec(ctx, "INSERT INTO intent_rule_activations(bot_id,scope_type,scope_id,ruleset_id) VALUES($1,$2,$3,$4)", set.BotID, target.Kind, target.ID, id); err != nil {
			return err
		}
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
		WHERE bot_id = $1 AND scope_type = $2 AND scope_id = $3 AND version = $4 AND deleted_at IS NULL
	`, botID, scopeType, scopeID, version).Scan(&id); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrRulesetNotFound
		}
		return fmt.Errorf("intent: find version: %w", err)
	}
	var status string
	if err := r.pool.QueryRow(ctx, `SELECT status FROM intent_rulesets WHERE deleted_at IS NULL AND id=$1`, id).Scan(&status); err != nil {
		return err
	}
	if status != "archived" {
		return ErrRulesetImmutable
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
		SELECT priority, match, action, enabled FROM intent_rule_entries
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
		if err := rows.Scan(&entry.Priority, &matchJSON, &actionJSON, &entry.Enabled); err != nil {
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

// Delete removes activations and hides a version without reusing its number.
// Delete 移除生效绑定并隐藏版本，保留其编号不被复用。
func (r *RulesetsRepo) Delete(ctx context.Context, botID, id int64) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err = tx.Exec(ctx, "SELECT pg_advisory_xact_lock(hashtextextended($1,0))", fmt.Sprintf("intent:%d", botID)); err != nil {
		return err
	}
	var found int64
	err = tx.QueryRow(ctx, "SELECT id FROM intent_rulesets WHERE id=$1 AND bot_id=$2 FOR UPDATE", id, botID).Scan(&found)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrRulesetNotFound
	}
	if err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, "DELETE FROM intent_rule_activations WHERE ruleset_id=$1", id); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, "UPDATE intent_rulesets SET deleted_at=COALESCE(deleted_at,now()) WHERE id=$1", id); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
