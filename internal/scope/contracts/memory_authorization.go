package contracts

import (
	"context"

	"github.com/HappyLadySauceM/Nagi/internal/scope/domain"
)

// MemoryAuthorization identifies the current authorization generation of a
// conversation scope and whether extraction is still authorized.
// MemoryAuthorization 标识会话作用域当前的授权代际以及是否仍允许提取。
type MemoryAuthorization struct {
	EventID    int64
	Authorized bool
}

// MemoryAuthorizer resolves the current memory authorization generation.
// MemoryAuthorizer 解析当前记忆授权代际。
type MemoryAuthorizer interface {
	ResolveMemoryAuthorization(ctx context.Context, botID int64, scopeType domain.ScopeType, scopeID int64) (MemoryAuthorization, error)
}
