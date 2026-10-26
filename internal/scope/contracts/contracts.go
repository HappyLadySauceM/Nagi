// Package contracts publishes the stable interfaces of the scope context.
// scope/contracts 发布 scope 上下文对外协作的稳定接口。
package contracts

import (
	"context"

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
