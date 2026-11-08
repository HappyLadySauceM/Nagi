// Package contracts publishes the stable interfaces of the audit context.
// audit/contracts 发布 audit 上下文对外协作的稳定接口。
package contracts

import (
	"context"
	"fmt"
	"time"

	"github.com/HappyLadySauceM/Nagi/internal/audit/domain"
)

// SideEffectUncertainError marks a completed write whose local audit terminal
// fact could not be saved. Retrying it may repeat an external side effect.
// SideEffectUncertainError 标记已完成、但本地审计终态未能保存的写操作；重试可能
// 重复外部副作用。
type SideEffectUncertainError struct {
	Stage string
	Cause error
}

func (e *SideEffectUncertainError) Error() string {
	if e == nil {
		return "side effect outcome unknown"
	}
	return fmt.Sprintf("%s side effect outcome unknown: %v", e.Stage, e.Cause)
}

func (e *SideEffectUncertainError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Cause
}

// UnsafeToRetry wraps an audit-completion failure after an external write.
// UnsafeToRetry 在外部写操作之后包装审计完成失败，调用方必须停止自动重试。
func UnsafeToRetry(stage string, cause error) error {
	return &SideEffectUncertainError{Stage: stage, Cause: cause}
}

// Recorder persists admin audit entries; implementations must never panic and
// should treat audit failures as non-fatal for the request being served.
// Recorder 持久化管理审计条目；实现不得 panic，且应把审计失败视为
// 非致命错误，不影响正在处理的请求。
type Recorder interface {
	Record(ctx context.Context, entry domain.Entry) error
}

// AIRecorder persists worker-run audit facts. Unlike the administrative audit
// recorder, callers treat a write failure as a processing failure.
// AIRecorder 持久化 worker 运行审计事实。与管理审计不同，调用方必须将写入失败
// 视为处理失败。
type AIRecorder interface {
	StartRun(context.Context, domain.AIRun) (domain.AIRun, error)
	FinishRun(context.Context, string, string, string) error
	RecordEvent(context.Context, domain.AIEvent) (domain.AIEvent, error)
	FinishEvent(context.Context, string, string, string, []byte) error
	RecordArtifact(context.Context, domain.AIArtifact) (domain.AIArtifact, error)
}

// AIReader is the administrator-only read model for local audit facts.
// AIReader 是供管理员使用的本地审计事实只读模型。
type AIReader interface {
	ListRuns(context.Context, domain.AIRunQuery) ([]domain.AIRun, error)
	GetRun(context.Context, string) (domain.AIRun, error)
	GetArtifact(context.Context, string) (domain.AIArtifact, error)
}

// AIStore is the complete local-audit port used by the composition root.
// AIStore 是组合根使用的完整本地审计端口。
type AIStore interface {
	AIRecorder
	AIReader
}

// ArtifactQueue persists media object references and retention cleanup state.
// ArtifactQueue 持久化媒体对象引用与保留期清理状态。
type ArtifactQueue interface {
	AIStore
	FindUploadedArtifactBySHA256(context.Context, string) (domain.AIArtifact, error)
	ClaimExpiredArtifacts(context.Context, int, time.Duration) ([]domain.AIArtifact, error)
	MarkArtifactExpired(context.Context, string, string) error
	RetryArtifactExpiry(context.Context, string, string, time.Time, string) error
}
