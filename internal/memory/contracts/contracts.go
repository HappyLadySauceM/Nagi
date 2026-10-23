// Package contracts publishes the stable interfaces of the memory context.
// memory/contracts 发布 memory 上下文对外协作的稳定接口。
package contracts

import (
	"context"

	"github.com/HappyLadySauceM/QQBot/internal/memory/domain"
)

// Searcher retrieves memories already filtered by visibility; callers must never
// bypass this port to read raw memory rows.
// Searcher 检索已经过可见性过滤的记忆；调用方不得绕过本端口直接读取原始记忆行。
type Searcher interface {
	SearchVisible(ctx context.Context, in domain.SearchRequest) ([]domain.Memory, error)
}
