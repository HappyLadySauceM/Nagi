// Per-scope sending policies that gate autonomous replies.
// 门控自主回复的按作用域出站策略。
package infrastructure

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/HappyLadySauceM/Nagi/internal/outbound/domain"
)

// Policy is one scope's sending configuration.
// Policy 是单个作用域的出站配置。
type Policy struct {
	BotID               int64
	ScopeType           string
	ScopeID             int64
	AllowSendCurrent    bool
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
type SendingAuthorizer func(context.Context, int64, string, int64) (bool, error)

type PoliciesRepo struct {
	authorize SendingAuthorizer
	pool      *pgxpool.Pool
}

// NewPoliciesRepo builds the repository.
// NewPoliciesRepo 构建仓储。
func NewPoliciesRepo(pool *pgxpool.Pool, authorize ...SendingAuthorizer) (*PoliciesRepo, error) {
	if pool == nil {
		return nil, fmt.Errorf("outbound: pool required")
	}
	repo := &PoliciesRepo{pool: pool}
	if len(authorize) > 0 {
		repo.authorize = authorize[0]
	}
	return repo, nil
}

// Get loads one policy; the boolean reports whether it exists.
// Get 读取一条策略；布尔值表示是否存在。
func (r *PoliciesRepo) Get(ctx context.Context, botID int64, scopeType string, scopeID int64) (Policy, bool, error) {
	var p Policy
	err := r.pool.QueryRow(ctx, `
		SELECT bot_id, scope_type, scope_id,
		       max_messages_per_run, max_chars_per_message, max_total_chars_per_run, message_interval_ms
		FROM scope_policies
		WHERE bot_id = $1 AND scope_type = $2 AND scope_id IN ($3, 0) ORDER BY scope_id DESC LIMIT 1
	`, botID, scopeType, scopeID).Scan(&p.BotID, &p.ScopeType, &p.ScopeID,
		&p.MaxMessagesPerRun, &p.MaxCharsPerMessage, &p.MaxTotalCharsPerRun, &p.MinIntervalMS)
	if errors.Is(err, pgx.ErrNoRows) {
		return Policy{}, false, nil
	}
	if err != nil {
		return Policy{}, false, fmt.Errorf("outbound: get policy: %w", err)
	}
	if r.authorize != nil {
		p.AllowSendCurrent, err = r.authorize(ctx, botID, scopeType, scopeID)
		if err != nil {
			return Policy{}, false, err
		}
	}
	return p, true, nil
}
