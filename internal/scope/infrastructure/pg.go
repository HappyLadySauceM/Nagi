// Package infrastructure implements the scope repository on PostgreSQL.
// scope/infrastructure 在 PostgreSQL 上实现作用域仓库。
package infrastructure

import (
	"context"
	"encoding/json"
	entsql "entgo.io/ent/dialect/sql"
	"github.com/HappyLadySauceM/Nagi/pkg/db/ent"
	sp "github.com/HappyLadySauceM/Nagi/pkg/db/ent/scopepolicy"
	"time"

	"fmt"

	"github.com/HappyLadySauceM/Nagi/pkg/db"

	"github.com/HappyLadySauceM/Nagi/internal/scope/domain"
)

// Repo is the pgx-backed scope policy repository.
// Repo 是基于 pgx 的作用域策略仓库。
type Repo struct {
	pool *db.Pool
}

// NewRepo builds the repository; the pool must not be nil.
// NewRepo 构建仓库；pool 不能为 nil。
func NewRepo(pool *db.Pool) (*Repo, error) {
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
	v, err := r.pool.Client.ScopePolicy.Query().Where(sp.BotID(botID), sp.ScopeType(string(scopeType)), sp.ScopeID(scopeID)).Only(ctx)
	if ent.IsNotFound(err) {
		return domain.Policy{}, false, nil
	}
	if err != nil {
		return domain.Policy{}, false, err
	}
	p, err := policyModel(v)
	return p, err == nil, err
}

// List returns all stored policies, optionally filtered by bot.
// List 返回全部已存储策略，可按机器人过滤。
func (r *Repo) List(ctx context.Context, botID *int64) ([]domain.Policy, error) {
	q := r.pool.Client.ScopePolicy.Query()
	if botID != nil {
		q.Where(sp.BotID(*botID))
	}
	rows, err := q.Order(ent.Asc(sp.FieldScopeType, sp.FieldScopeID)).All(ctx)
	if err != nil {
		return nil, err
	}
	out := []domain.Policy{}
	for _, v := range rows {
		p, err := policyModel(v)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, nil
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
	tx, err := r.pool.Client.Tx(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	client := tx.Client()
	if _, err := client.ExecContext(ctx, "SELECT pg_advisory_xact_lock(hashtextextended($1,0))", fmt.Sprintf("scope:%d:%s", botID, policy.ScopeType)); err != nil {
		return err
	}
	if policy.ExpectedTemplateVersion != nil {
		v, err := client.ScopePolicy.Query().Where(sp.BotID(botID), sp.ScopeType(string(policy.ScopeType)), sp.ScopeID(0)).Only(ctx)
		if err != nil && !ent.IsNotFound(err) {
			return err
		}
		var version int64
		if v != nil {
			version = v.Version
		}
		if version != *policy.ExpectedTemplateVersion {
			return domain.ErrTemplateConflict
		}
	}
	err = client.ScopePolicy.Create().SetBotID(botID).SetScopeType(string(policy.ScopeType)).SetScopeID(policy.ScopeID).SetEnabled(policy.Enabled).SetReplyMode(string(policy.ReplyMode)).SetStoreMessages(policy.StoreMessages).SetAllowMemoryWrite(policy.AllowMemoryWrite).SetAllowHistoryRead(policy.AllowHistoryRead).SetMinReplyIntervalMs(policy.MinReplyIntervalMS).SetMaxRepliesPerHour(policy.MaxRepliesPerHour).SetMaxMessagesPerRun(policy.MaxMessagesPerRun).SetMaxCharsPerMessage(policy.MaxCharsPerMessage).SetMaxTotalCharsPerRun(policy.MaxTotalCharsPerRun).SetMessageIntervalMs(policy.MessageIntervalMS).SetToolPermissions(permissions).SetUpdatedAt(time.Now()).OnConflict(entsql.ConflictColumns("bot_id", "scope_type", "scope_id")).Update(func(u *ent.ScopePolicyUpsert) {
		u.UpdateEnabled()
		u.UpdateReplyMode()
		u.UpdateStoreMessages()
		u.UpdateAllowMemoryWrite()
		u.UpdateAllowHistoryRead()
		u.UpdateMinReplyIntervalMs()
		u.UpdateMaxRepliesPerHour()
		u.UpdateMaxMessagesPerRun()
		u.UpdateMaxCharsPerMessage()
		u.UpdateMaxTotalCharsPerRun()
		u.UpdateMessageIntervalMs()
		u.UpdateToolPermissions()
		u.UpdateUpdatedAt()
		u.AddVersion(1)
	}).Exec(ctx)
	if err != nil {
		return err
	}
	return tx.Commit()
}

// Delete removes one stored policy and reports whether a row was deleted.
// Delete 删除一条已存策略并报告是否删除了行。
func (r *Repo) Delete(ctx context.Context, botID int64, scopeType domain.ScopeType, scopeID int64) (bool, error) {
	n, err := r.pool.Client.ScopePolicy.Delete().Where(sp.BotID(botID), sp.ScopeType(string(scopeType)), sp.ScopeID(scopeID)).Exec(ctx)
	return n > 0, err
}

func policyModel(v *ent.ScopePolicy) (domain.Policy, error) {
	p := domain.Policy{ScopeType: domain.ScopeType(v.ScopeType), ScopeID: v.ScopeID, Version: v.Version, Enabled: v.Enabled, ReplyMode: domain.ReplyMode(v.ReplyMode), StoreMessages: v.StoreMessages, AllowMemoryWrite: v.AllowMemoryWrite, AllowHistoryRead: v.AllowHistoryRead, MinReplyIntervalMS: v.MinReplyIntervalMs, MaxRepliesPerHour: v.MaxRepliesPerHour, MaxMessagesPerRun: v.MaxMessagesPerRun, MaxCharsPerMessage: v.MaxCharsPerMessage, MaxTotalCharsPerRun: v.MaxTotalCharsPerRun, MessageIntervalMS: v.MessageIntervalMs}
	permissions, err := decodeToolPermissions(v.ToolPermissions)
	p.ToolPermissions = permissions
	return p, err
}
