// Bounded worker pool with per-conversation serial processing.
// 有界工作器池：单会话顺序处理，跨会话受控并发。
package infrastructure

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"go.opentelemetry.io/otel/attribute"

	"github.com/HappyLadySauceM/Nagi/internal/conversation/application"
	"github.com/HappyLadySauceM/Nagi/internal/conversation/domain"
	"github.com/HappyLadySauceM/Nagi/internal/observability"
)

// JobProcessor executes the decision pipeline for one job.
// JobProcessor 为单个任务执行决策流水线。
type JobProcessor interface {
	Process(ctx context.Context, job domain.Job) error
}

// WorkerOptions bounds the worker pool.
// WorkerOptions 限定工作器池。
type WorkerOptions struct {
	RenewInterval time.Duration
	Concurrency   int
	MaxPending    int
	PollInterval  time.Duration
	MaxAttempts   int
	RetryBackoff  time.Duration
	// Identity may be nil, in which case traces carry no user dimension.
	// Identity 可为 nil，此时 trace 不携带用户维度。
	Identity *observability.Identities
}

// Worker polls pending jobs from the database and processes them with per-conversation
// ordering and a global concurrency bound.
// Worker 从数据库轮询待处理任务，按会话保持顺序并以全局并发上限处理。
type Worker struct {
	repo      application.Repository
	processor JobProcessor
	opts      WorkerOptions
	logger    *slog.Logger

	pending atomic.Int64
}

// NewWorker validates options and builds the worker.
// NewWorker 校验选项并构建工作器。
func NewWorker(repo application.Repository, processor JobProcessor, opts WorkerOptions, logger *slog.Logger) (*Worker, error) {
	if repo == nil || processor == nil || logger == nil {
		return nil, fmt.Errorf("conversation: worker dependencies required")
	}
	if opts.RenewInterval <= 0 {
		opts.RenewInterval = 30 * time.Second
	}
	if opts.Concurrency <= 0 {
		opts.Concurrency = 4
	}
	if opts.MaxPending <= 0 {
		opts.MaxPending = 1000
	}
	if opts.PollInterval <= 0 {
		opts.PollInterval = 200 * time.Millisecond
	}
	if opts.MaxAttempts <= 0 {
		opts.MaxAttempts = 5
	}
	if opts.RetryBackoff <= 0 {
		opts.RetryBackoff = 5 * time.Second
	}
	return &Worker{repo: repo, processor: processor, opts: opts, logger: logger}, nil
}

// Run recovers stale jobs and loops until ctx is cancelled; it drains in-flight
// jobs before returning so shutdown never cuts a decision pipeline mid-flight.
// Run 先恢复遗留任务，随后循环直至 ctx 取消；返回前排空在途任务，
// 保证退出不会中断正在执行的决策流水线。
func (w *Worker) Run(ctx context.Context) error {
	var wg sync.WaitGroup
	var mu sync.Mutex
	active := map[string]context.CancelFunc{}
	renewalCtx, stopRenewal := context.WithCancel(context.Background())
	defer stopRenewal()
	renewDone := make(chan struct{})
	defer func() { stopRenewal(); <-renewDone }()
	go func() {
		defer close(renewDone)
		ticker := time.NewTicker(w.opts.RenewInterval)
		defer ticker.Stop()
		for {
			select {
			case <-renewalCtx.Done():
				return
			case <-ticker.C:
				mu.Lock()
				tokens := make([]string, 0, len(active))
				for token := range active {
					tokens = append(tokens, token)
				}
				mu.Unlock()
				if renewer, ok := w.repo.(interface {
					RenewClaims(context.Context, []string) ([]string, error)
				}); ok {
					c, cancel := context.WithTimeout(renewalCtx, 5*time.Second)
					renewed, err := renewer.RenewClaims(c, tokens)
					cancel()
					good := map[string]bool{}
					if err == nil {
						for _, token := range renewed {
							good[token] = true
						}
					}
					mu.Lock()
					for _, token := range tokens {
						if !good[token] {
							if cancel := active[token]; cancel != nil {
								cancel()
							}
						}
					}
					mu.Unlock()
				}
			}
		}
	}()
	defer wg.Wait()
	nextRecovery := time.Time{}
	ticker := time.NewTicker(w.opts.PollInterval)
	defer ticker.Stop()
	for {
		if ctx.Err() != nil {
			return nil
		}
		if time.Now().After(nextRecovery) {
			nextRecovery = time.Now().Add(30 * time.Second)
			if err := w.repo.RecoverStaleJobs(ctx, domain.RecoveryPolicy{MaxAttempts: w.opts.MaxAttempts, RetryBackoff: w.opts.RetryBackoff}); err != nil {
				w.logger.Warn("recover expired claims failed", "error", err)
			}
			if repair, ok := w.repo.(interface{ ReconcileOutgoing(context.Context) error }); ok {
				if err := repair.ReconcileOutgoing(ctx); err != nil {
					w.logger.Warn("reconcile outgoing history failed", "error", err)
				}
			}
		}
		free := w.opts.Concurrency - int(w.pending.Load())
		if free > 0 {
			if free > w.opts.MaxPending {
				free = w.opts.MaxPending
			}
			jobs, err := w.repo.ClaimReadyJobs(ctx, free)
			if err != nil {
				w.logger.Warn("claim jobs failed", "error", err)
			} else {
				for _, job := range jobs {
					jobCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))
					mu.Lock()
					active[job.ClaimToken] = cancel
					mu.Unlock()
					w.pending.Add(1)
					wg.Add(1)
					go func(job domain.Job) {
						defer wg.Done()
						defer w.pending.Add(-1)
						defer cancel()
						defer func() { mu.Lock(); delete(active, job.ClaimToken); mu.Unlock() }()
						w.process(jobCtx, job)
					}(job)
				}
			}
		}
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
}

// process executes the pipeline and records the outcome; panics become failures.
// The job runs on a detached context so graceful shutdown drains in-flight work
// and their terminal state writes instead of severing them.
// process 执行流水线并记录结果；panic 转为任务失败。
// 任务在脱离取消的上下文上执行，优雅关停会等待在途任务及其终态回写完成。
func (w *Worker) process(ctx context.Context, job domain.Job) {
	// Trace context must be restored on the detached context so shutdown does
	// not sever the parent chain while the job is still finishing.
	// 追踪上下文必须在脱离取消的上下文上还原，避免关停期间切断父链。
	jobCtx := observability.TraceContext{TraceParent: job.TraceParent, TraceState: job.TraceState}.
		Restore(ctx)
	jobCtx, span := tracer().Start(jobCtx, "nagi.worker.process")
	defer span.End()
	// A job without a stored context predates trace propagation; marking it
	// keeps legacy traffic distinguishable from broken propagation.
	// 没有存储上下文的任务早于追踪传播；标注后可将其与传播故障区分开。
	span.SetAttributes(
		attribute.Int64("nagi.job_id", job.ID),
		attribute.Int64("nagi.message_id", job.MessageID),
		attribute.String("nagi.conversation_id", job.ConversationID),
		attribute.String("nagi.scope_type", string(job.Kind)),
		attribute.Bool("nagi.legacy_task", job.TraceParent == ""),
	)
	// The queued message is the worker's input; body export follows the
	// content gate, so a redacted deployment still shows ids and timing.
	// 队列中的消息是工作器的输入；正文导出遵循开关，
	// 因此脱敏部署仍能看到 ID 与耗时。
	observability.RecordObservation(span, "langfuse.observation.input", map[string]any{
		"job_id":          job.ID,
		"message_id":      job.MessageID,
		"conversation_id": job.ConversationID,
		"scope_type":      string(job.Kind),
		"legacy_task":     job.TraceParent == "",
		"text":            job.Message.Text,
	})
	identity := w.opts.Identity.For(job.BotID, job.Message.SenderID, job.TargetID)
	identity.SessionID = job.ConversationID
	observability.SetIdentity(span, identity)
	// Child spans (intent, agent, send) receive the same identity through
	// baggage so Langfuse can filter every observation by session and user.
	// 子 span（意图、生成、发送）通过 baggage 获得同样的身份属性，
	// 使 Langfuse 能按会话与用户过滤每个观测。
	jobCtx = observability.WithIdentity(jobCtx, identity)
	defer func() {
		if rec := recover(); rec != nil {
			w.logger.Error("job processor panicked", "job_id", job.ID, "panic", fmt.Sprint(rec))
			span.RecordError(fmt.Errorf("panic: %v", rec))
			w.finish(jobCtx, job.ID, job.ClaimToken, "failed", "panic")
		}
	}()
	if err := w.processor.Process(jobCtx, job); err != nil {
		w.logger.Warn("job processed with failure", "job_id", job.ID, "error", err)
		span.RecordError(err)
		observability.RecordObservation(span, "langfuse.observation.output", map[string]any{
			"status": "failed",
		})
		w.finish(jobCtx, job.ID, job.ClaimToken, "failed", "process_error")
		return
	}
	if reader, ok := w.repo.(interface {
		DeliveryError(context.Context, int64) error
	}); ok {
		if err := reader.DeliveryError(jobCtx, job.ID); err != nil {
			span.RecordError(err)
			w.finish(jobCtx, job.ID, job.ClaimToken, "failed", "delivery_incomplete")
			return
		}
	}
	observability.RecordObservation(span, "langfuse.observation.output", map[string]any{
		"status": "done",
	})
	w.finish(jobCtx, job.ID, job.ClaimToken, "done", "")
}

// finish records the terminal job state inside its own span so a completed job
// is never mistaken for a delivered message.
// finish 在独立 span 中记录任务终态，避免把任务完成误认为消息已送达。
func (w *Worker) finish(ctx context.Context, jobID int64, claim, status, code string) {
	ctx, span := tracer().Start(ctx, "nagi.message.finalize")
	defer span.End()
	span.SetAttributes(attribute.String("nagi.job.status", status))
	if code != "" {
		span.SetAttributes(attribute.String("nagi.error_code", code))
	}
	observability.RecordObservation(span, "langfuse.observation.output", map[string]any{
		"status":     status,
		"error_code": code,
	})
	var err error
	if status == "done" {
		err = w.repo.CompleteJob(ctx, jobID, claim)
	} else {
		err = w.repo.FailJob(ctx, jobID, code, w.opts.RetryBackoff, w.opts.MaxAttempts, claim)
	}
	if err != nil {
		span.RecordError(err)
		w.logger.Warn("record job outcome failed", "job_id", jobID, "status", status, "error", err)
	}
}
