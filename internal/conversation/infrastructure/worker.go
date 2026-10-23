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

	"github.com/HappyLadySauceM/QQBot/internal/conversation/application"
	"github.com/HappyLadySauceM/QQBot/internal/conversation/domain"
)

// JobProcessor executes the decision pipeline for one job.
// JobProcessor 为单个任务执行决策流水线。
type JobProcessor interface {
	Process(ctx context.Context, job domain.Job) error
}

// WorkerOptions bounds the worker pool.
// WorkerOptions 限定工作器池。
type WorkerOptions struct {
	Concurrency  int
	MaxPending   int
	PollInterval time.Duration
	MaxAttempts  int
	RetryBackoff time.Duration
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
	if err := w.repo.RecoverStaleJobs(ctx); err != nil {
		w.logger.Warn("recover stale jobs failed", "error", err)
	}

	var wg sync.WaitGroup
	var qmu sync.Mutex
	queues := make(map[string]chan domain.Job)
	sem := make(chan struct{}, w.opts.Concurrency)

	defer func() {
		qmu.Lock()
		for _, ch := range queues {
			close(ch)
		}
		qmu.Unlock()
		wg.Wait()
	}()

	for {
		if ctx.Err() != nil {
			return nil
		}
		w.dispatchBatch(ctx, &qmu, queues, &wg, sem)
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(w.opts.PollInterval):
		}
	}
}

// dispatchBatch claims ready jobs into per-conversation queues without blocking.
// Jobs that do not fit are released back to the pending state.
// dispatchBatch 将就绪任务非阻塞地放入会话队列；放不下的任务退回待处理状态。
func (w *Worker) dispatchBatch(ctx context.Context, qmu *sync.Mutex, queues map[string]chan domain.Job, wg *sync.WaitGroup, sem chan struct{}) {
	if w.pending.Load() >= int64(w.opts.MaxPending) {
		return
	}
	jobs, err := w.repo.ClaimReadyJobs(ctx, int(w.opts.MaxPending)-int(w.pending.Load()))
	if err != nil {
		// The database being down must not crash the loop; retry on the next tick.
		// 数据库故障不能中断循环；下一个轮询周期重试。
		w.logger.Warn("claim ready jobs failed", "error", err)
		return
	}
	for _, job := range jobs {
		qmu.Lock()
		ch, ok := queues[job.ConversationID]
		if !ok {
			ch = make(chan domain.Job, 64)
			queues[job.ConversationID] = ch
			wg.Add(1)
			go w.consume(ctx, ch, sem, wg.Done)
		}
		qmu.Unlock()
		select {
		case ch <- job:
			w.pending.Add(1)
		default:
			// Conversation queue full: release the claim so the job is retried
			// on a later poll instead of staying stuck in processing.
			// 会话队列已满：释放认领，任务留待后续轮询重试。
			if err := w.repo.ReleaseJob(ctx, job.ID); err != nil {
				w.logger.Warn("release job failed", "job_id", job.ID, "error", err)
			}
		}
	}
}

// consume processes one conversation's jobs strictly in order.
// consume 严格按顺序处理单个会话的任务。
func (w *Worker) consume(ctx context.Context, ch <-chan domain.Job, sem chan struct{}, done func()) {
	defer done()
	for job := range ch {
		select {
		case <-ctx.Done():
			return
		case sem <- struct{}{}:
		}
		w.process(ctx, job)
		w.pending.Add(-1)
		<-sem
	}
}

// process executes the pipeline and records the outcome; panics become failures.
// The job runs on a detached context so graceful shutdown drains in-flight work
// and their terminal state writes instead of severing them.
// process 执行流水线并记录结果；panic 转为任务失败。
// 任务在脱离取消的上下文上执行，优雅关停会等待在途任务及其终态回写完成。
func (w *Worker) process(ctx context.Context, job domain.Job) {
	jobCtx := context.WithoutCancel(ctx)
	defer func() {
		if rec := recover(); rec != nil {
			w.logger.Error("job processor panicked", "job_id", job.ID, "panic", fmt.Sprint(rec))
			if err := w.repo.FailJob(jobCtx, job.ID, "panic", w.opts.RetryBackoff, w.opts.MaxAttempts); err != nil {
				w.logger.Warn("record job failure failed", "job_id", job.ID, "error", err)
			}
		}
	}()
	if err := w.processor.Process(jobCtx, job); err != nil {
		w.logger.Warn("job processed with failure", "job_id", job.ID, "error", err)
		if err := w.repo.FailJob(jobCtx, job.ID, "process_error", w.opts.RetryBackoff, w.opts.MaxAttempts); err != nil {
			w.logger.Warn("record job failure failed", "job_id", job.ID, "error", err)
		}
		return
	}
	if err := w.repo.CompleteJob(jobCtx, job.ID); err != nil {
		w.logger.Warn("complete job failed", "job_id", job.ID, "error", err)
	}
}
