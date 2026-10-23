// Package contracts publishes the stable interfaces of the intent context.
// intent/contracts 发布 intent 上下文对外协作的稳定接口。
package contracts

import (
	"context"

	"github.com/HappyLadySauceM/QQBot/internal/intent/domain"
)

// ReplyExecutor acts on reply decisions, e.g. generating and sending a response.
// ReplyExecutor 对回复决策执行动作，例如生成并发送回复。
type ReplyExecutor interface {
	ExecuteReply(ctx context.Context, task domain.Task, history []domain.Message) error
}
