// Per-scope sending budgets used by the outbound ledger.
// 出站台账使用的按作用域发送预算。
package infrastructure

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/HappyLadySauceM/Nagi/pkg/db/ent"
	sp "github.com/HappyLadySauceM/Nagi/pkg/db/ent/scopepolicy"

	scopeinfra "github.com/HappyLadySauceM/Nagi/internal/scope/infrastructure"
	"github.com/HappyLadySauceM/Nagi/pkg/db"

	"github.com/HappyLadySauceM/Nagi/internal/outbound/domain"
)

// DefaultMessageIntervalMS is the fallback when a scope has no sending policy.
// DefaultMessageIntervalMS 是会话没有发送策略时的缺省间隔。
const DefaultMessageIntervalMS = 700

// Policy is one scope's sending configuration. Whether autonomous sending is
// allowed is no longer stored here: it follows the scope's capability grant.
// Policy 是单个作用域的出站配置。是否允许自主发送不再存放在这里：
// 它跟随该作用域的能力授权。
type Policy struct {
	BotID               int64
	ScopeType           string
	ScopeID             int64
	MinIntervalMS       int
	MaxMessagesPerRun   int
	MaxCharsPerMessage  int
	MaxTotalCharsPerRun int
}

// Limits converts the policy into ledger limits.
// Limits 把策略转换为台账预算。
func (p Policy) Limits() domain.Limits {
	return domain.Limits{
		MaxMessages:        p.MaxMessagesPerRun,
		MinIntervalMS:      p.MinIntervalMS,
		MaxCharsPerMessage: p.MaxCharsPerMessage,
		MaxTotalChars:      p.MaxTotalCharsPerRun,
	}
}

// PoliciesRepo stores per-scope sending policies.
// PoliciesRepo 存储按作用域的出站策略。
type PoliciesRepo struct {
	pool *db.Pool
}

// NewPoliciesRepo builds the repository.
// NewPoliciesRepo 构建仓储。
func NewPoliciesRepo(pool *db.Pool) (*PoliciesRepo, error) {
	if pool == nil {
		return nil, fmt.Errorf("outbound: pool required")
	}
	return &PoliciesRepo{pool: pool}, nil
}

// Get loads one policy; the boolean reports whether it exists.
// Get 读取一条策略；布尔值表示是否存在。
func (r *PoliciesRepo) Get(ctx context.Context, botID int64, scopeType string, scopeID int64) (Policy, bool, error) {
	v, err := r.pool.Client.ScopePolicy.Query().Where(sp.BotID(botID), sp.ScopeType(scopeType), sp.ScopeIDIn(scopeID, 0)).Order(ent.Desc(sp.FieldScopeID)).First(ctx)
	if ent.IsNotFound(err) {
		return Policy{}, false, nil
	}
	if err != nil {
		return Policy{}, false, err
	}
	return Policy{BotID: v.BotID, ScopeType: v.ScopeType, ScopeID: v.ScopeID, MinIntervalMS: v.MessageIntervalMs, MaxMessagesPerRun: v.MaxMessagesPerRun, MaxCharsPerMessage: v.MaxCharsPerMessage, MaxTotalCharsPerRun: v.MaxTotalCharsPerRun}, true, nil
}

func lockCurrentLimits(ctx context.Context, tx *db.Tx, bot int64, kind string, target int64, limits domain.Limits) (domain.Limits, error) {
	if err := scopeinfra.LockMemoryAuthorization(ctx, tx, bot, kind, target); err != nil {
		return domain.Limits{}, err
	}
	err := tx.QueryRow(ctx, `SELECT max_messages_per_run,max_chars_per_message,max_total_chars_per_run,max_replies_per_hour,message_interval_ms
 FROM scope_policies WHERE bot_id=$1 AND scope_type=$2 AND scope_id IN ($3,0) ORDER BY scope_id DESC LIMIT 1 FOR SHARE`, bot, kind, target).
		Scan(&limits.MaxMessages, &limits.MaxCharsPerMessage, &limits.MaxTotalChars, &limits.MaxPerHour, &limits.MinIntervalMS)
	if !errors.Is(err, sql.ErrNoRows) {
		return limits, err
	}
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock_shared(hashtextextended('settings:bootstrap',0))`); err != nil {
		return domain.Limits{}, err
	}
	err = tx.QueryRow(ctx, `SELECT COALESCE((payload #>> '{Agent,Sending,MaxMessagesPerRun}')::integer,0),
 COALESCE((payload #>> '{Agent,Sending,MaxCharsPerMessage}')::integer,0),
 COALESCE((payload #>> '{Agent,Sending,MaxTotalCharsPerRun}')::integer,0)
 FROM system_settings WHERE id=1 FOR SHARE`).Scan(&limits.MaxMessages, &limits.MaxCharsPerMessage, &limits.MaxTotalChars)
	if errors.Is(err, sql.ErrNoRows) {
		err = nil
	}
	return limits, err
}
