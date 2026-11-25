// Package contracts publishes the stable interfaces of the scope context.
// scope/contracts 发布 scope 上下文对外协作的稳定接口。
package contracts

import (
	"context"
	"database/sql"
	"fmt"
	"sort"

	"github.com/HappyLadySauceM/Nagi/internal/scope/domain"
)

// Authorizer resolves the effective policy of one conversation scope.
// Authorizer 解析单个会话作用域的生效策略。
//
// Implementations must apply default-deny: unknown scopes resolve to the
// safe default (everything disabled), never to an enabled policy.
// 实现必须默认拒绝：未知作用域解析为安全默认（全部关闭），绝不会开启。
type Authorizer interface {
	Resolve(ctx context.Context, botID int64, scopeType domain.ScopeType, scopeID int64) (domain.Policy, error)
}

type PolicyReadFence interface {
	WithPolicyRead(context.Context, int64, string, func() error) error
}

func ResolveConversation(ctx context.Context, reader Authorizer, botID int64, kind string, targetID int64) (domain.Policy, error) {
	return reader.Resolve(ctx, botID, domain.ScopeType(kind), targetID)
}

// PolicyLockKey identifies the policy family shared by a template and its overrides.
// PolicyLockKey 标识模板及其独立策略共用的策略锁。
type PolicyLockKey struct {
	BotID int64
	Kind  string
}

// LockPolicies takes transaction-scoped policy locks in account and kind order.
// LockPolicies 按账号和作用域类型顺序取得事务级策略锁。
func LockPolicies(ctx context.Context, tx interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}, exclusive bool, keys ...PolicyLockKey) error {
	ordered := append([]PolicyLockKey(nil), keys...)
	sort.Slice(ordered, func(i, j int) bool {
		if ordered[i].BotID != ordered[j].BotID {
			return ordered[i].BotID < ordered[j].BotID
		}
		return ordered[i].Kind < ordered[j].Kind
	})
	statement := "SELECT pg_advisory_xact_lock_shared(hashtextextended($1,0))"
	if exclusive {
		statement = "SELECT pg_advisory_xact_lock(hashtextextended($1,0))"
	}
	for i, key := range ordered {
		if i > 0 && key == ordered[i-1] {
			continue
		}
		if _, err := tx.ExecContext(ctx, statement, fmt.Sprintf("scope:%d:%s", key.BotID, key.Kind)); err != nil {
			return fmt.Errorf("scope: lock policy family: %w", err)
		}
	}
	return nil
}
