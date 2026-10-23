// Package contracts publishes the stable interfaces of the audit context.
// audit/contracts 发布 audit 上下文对外协作的稳定接口。
package contracts

import (
	"context"

	"github.com/HappyLadySauceM/QQBot/internal/audit/domain"
)

// Recorder persists admin audit entries; implementations must never panic and
// should treat audit failures as non-fatal for the request being served.
// Recorder 持久化管理审计条目；实现不得 panic，且应把审计失败视为
// 非致命错误，不影响正在处理的请求。
type Recorder interface {
	Record(ctx context.Context, entry domain.Entry) error
}
