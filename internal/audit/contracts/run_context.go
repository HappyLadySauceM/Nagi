package contracts

import (
	"context"
	"encoding/json"
	"fmt"
	"sync/atomic"

	"github.com/HappyLadySauceM/Nagi/internal/audit/domain"
	"github.com/HappyLadySauceM/Nagi/internal/observability"
)

// RunContext carries the mandatory, local audit writer for one worker attempt.
// It is deliberately separate from tracing: local facts remain required when
// Langfuse is unavailable.
type RunContext struct {
	recorder AIRecorder
	runID    string
	next     atomic.Int64
}

type runContextKey struct{}
type sideEffectKey struct{}

// sideEffectState records whether an execution reached an external write. It
// is kept in the worker context so an audit failure cannot trigger an unsafe
// replay after the write boundary has been crossed.
// sideEffectState 记录一次执行是否到达外部写入。它保存在工作器上下文中，确保
// 跨过写入边界后，审计失败不会触发不安全的重放。
type sideEffectState struct{ occurred atomic.Bool }

// WithRun attaches a newly started audit run to its worker context. Sequence
// zero belongs to the worker root event, so child operations start at one.
func WithRun(ctx context.Context, recorder AIRecorder, runID string) context.Context {
	run := &RunContext{recorder: recorder, runID: runID}
	run.next.Store(1)
	return context.WithValue(ctx, runContextKey{}, run)
}

// WithSideEffectState enables no-retry tracking for one worker execution.
// WithSideEffectState 为一次工作器执行启用不可重试的副作用跟踪。
func WithSideEffectState(ctx context.Context) context.Context {
	if _, ok := ctx.Value(sideEffectKey{}).(*sideEffectState); ok {
		return ctx
	}
	return context.WithValue(ctx, sideEffectKey{}, &sideEffectState{})
}

// MarkSideEffect records that a call may have caused an external write.
// MarkSideEffect 记录一次调用可能已经产生外部写入。
func MarkSideEffect(ctx context.Context) {
	if state, ok := ctx.Value(sideEffectKey{}).(*sideEffectState); ok {
		state.occurred.Store(true)
	}
}

// SideEffectOccurred reports whether retrying this worker attempt is unsafe.
// SideEffectOccurred 报告重试本次工作器尝试是否不安全。
func SideEffectOccurred(ctx context.Context) bool {
	state, ok := ctx.Value(sideEffectKey{}).(*sideEffectState)
	return ok && state.occurred.Load()
}

// StartEvent persists a required operation-start fact. It returns nil when the
// current execution has no audit run, which keeps database-less wiring usable.
func StartEvent(ctx context.Context, stage string, input any) (*domain.AIEvent, error) {
	run, _ := ctx.Value(runContextKey{}).(*RunContext)
	if run == nil || run.recorder == nil || run.runID == "" {
		return nil, nil
	}
	payload, err := json.Marshal(observability.Redact(input))
	if err != nil {
		return nil, fmt.Errorf("encode %s audit input: %w", stage, err)
	}
	sequence := int(run.next.Add(1) - 1)
	event, err := run.recorder.RecordEvent(ctx, domain.AIEvent{
		RunID: run.runID, OperationID: fmt.Sprintf("%s:%06d", stage, sequence),
		Sequence: sequence, SchemaVersion: 1, Stage: stage, Input: payload,
	})
	if err != nil {
		return nil, fmt.Errorf("start %s audit event: %w", stage, err)
	}
	return &event, nil
}

// FinishEvent persists the terminal operation fact. Callers must return the
// error because a later side effect is not auditable without it.
func FinishEvent(ctx context.Context, event *domain.AIEvent, status, errorCode string, output any) error {
	if event == nil {
		return nil
	}
	payload, err := json.Marshal(observability.Redact(output))
	if err != nil {
		return fmt.Errorf("encode %s audit output: %w", event.Stage, err)
	}
	recorder := auditRecorder(ctx)
	if recorder == nil {
		return fmt.Errorf("finish %s audit event: run context is missing", event.Stage)
	}
	if err := recorder.FinishEvent(ctx, event.ID, status, errorCode, payload); err != nil {
		return fmt.Errorf("finish %s audit event: %w", event.Stage, err)
	}
	return nil
}

func auditRecorder(ctx context.Context) AIRecorder {
	run, _ := ctx.Value(runContextKey{}).(*RunContext)
	if run == nil {
		return nil
	}
	return run.recorder
}
