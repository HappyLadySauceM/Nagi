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
	MaxMessagesPerRun   int
	MaxCharsPerMessage  int
	MaxTotalCharsPerRun int
}

// Limits converts the policy into ledger limits.
// Limits 把策略转换为台账预算。
func (p Policy) Limits() domain.Limits {
	return domain.Limits{
		MaxMessages:        p.MaxMessagesPerRun,
		MaxCharsPerMessage: p.MaxCharsPerMessage,
		MaxTotalChars:      p.MaxTotalCharsPerRun,
	}
}

// PoliciesRepo stores per-scope sending policies.
// PoliciesRepo 存储按作用域的出站策略。
type PoliciesRepo struct {
	pool *pgxpool.Pool
}

// NewPoliciesRepo builds the repository.
// NewPoliciesRepo 构建仓储。
func NewPoliciesRepo(pool *pgxpool.Pool) (*PoliciesRepo, error) {
	if pool == nil {
		return nil, fmt.Errorf("outbound: pool required")
	}
	return &PoliciesRepo{pool: pool}, nil
}

// Get loads one policy; the boolean reports whether it exists.
// Get 读取一条策略；布尔值表示是否存在。
func (r *PoliciesRepo) Get(ctx context.Context, botID int64, scopeType string, scopeID int64) (Policy, bool, error) {
	var p Policy
	err := r.pool.QueryRow(ctx, `
		SELECT bot_id, scope_type, scope_id, allow_send_current,
		       max_messages_per_run, max_chars_per_message, max_total_chars_per_run
		FROM agent_sending_policies
		WHERE bot_id = $1 AND scope_type = $2 AND scope_id = $3
	`, botID, scopeType, scopeID).Scan(&p.BotID, &p.ScopeType, &p.ScopeID, &p.AllowSendCurrent,
		&p.MaxMessagesPerRun, &p.MaxCharsPerMessage, &p.MaxTotalCharsPerRun)
	if errors.Is(err, pgx.ErrNoRows) {
		return Policy{}, false, nil
	}
	if err != nil {
		return Policy{}, false, fmt.Errorf("outbound: get policy: %w", err)
	}
	return p, true, nil
}

// Upsert stores one policy.
// Upsert 写入一条策略。
func (r *PoliciesRepo) Upsert(ctx context.Context, p Policy) error {
	_, err := r.pool.Exec(ctx, `
		INSERT INTO agent_sending_policies
			(bot_id, scope_type, scope_id, allow_send_current,
			 max_messages_per_run, max_chars_per_message, max_total_chars_per_run)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		ON CONFLICT (bot_id, scope_type, scope_id) DO UPDATE SET
			allow_send_current = EXCLUDED.allow_send_current,
			max_messages_per_run = EXCLUDED.max_messages_per_run,
			max_chars_per_message = EXCLUDED.max_chars_per_message,
			max_total_chars_per_run = EXCLUDED.max_total_chars_per_run,
			updated_at = now()
	`, p.BotID, p.ScopeType, p.ScopeID, p.AllowSendCurrent,
		p.MaxMessagesPerRun, p.MaxCharsPerMessage, p.MaxTotalCharsPerRun)
	if err != nil {
		return fmt.Errorf("outbound: upsert policy: %w", err)
	}
	return nil
}
