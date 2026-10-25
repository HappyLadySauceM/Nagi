// Tests for the bounded worker: per-conversation ordering, failure handling.
// 有界工作器测试：单会话顺序与失败处理。
package infrastructure

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"

	"github.com/HappyLadySauceM/QQBot/internal/conversation/domain"
	"github.com/HappyLadySauceM/QQBot/internal/observability"
)

// fakeJobRepo serves a fixed job list and records outcomes.
// fakeJobRepo 提供固定任务列表并记录结果。
type fakeJobRepo struct {
	mu        sync.Mutex
	jobs      []domain.Job
	completed []int64
	failed    []int64
	recovered bool
}

func (f *fakeJobRepo) ClaimReadyJobs(_ context.Context, limit int) ([]domain.Job, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := limit
	if n > len(f.jobs) {
		n = len(f.jobs)
	}
	out := append([]domain.Job(nil), f.jobs[:n]...)
	f.jobs = f.jobs[n:]
	return out, nil
}

func (f *fakeJobRepo) ReleaseJob(_ context.Context, _ int64) error {
	return nil
}

func (f *fakeJobRepo) CompleteJob(_ context.Context, jobID int64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.completed = append(f.completed, jobID)
	return nil
}

func (f *fakeJobRepo) FailJob(_ context.Context, jobID int64, _ string, _ time.Duration, _ int) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.failed = append(f.failed, jobID)
	return nil
}

func (f *fakeJobRepo) RecoverStaleJobs(_ context.Context) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.recovered = true
	return nil
}

// Unused repository methods for the interface.
// 接口要求的未使用方法。
func (f *fakeJobRepo) Ingest(context.Context, domain.IngestInput) (domain.IngestResult, error) {
	return domain.IngestResult{}, nil
}
func (f *fakeJobRepo) RecentMessages(context.Context, string, int) ([]domain.Message, error) {
	return nil, nil
}
func (f *fakeJobRepo) GetConversation(context.Context, int64, domain.Kind, int64) (domain.Conversation, bool, error) {
	return domain.Conversation{}, false, nil
}

func (f *fakeJobRepo) BotReplyMeta(context.Context, string) (time.Time, int, error) {
	return time.Time{}, 0, nil
}
func (f *fakeJobRepo) RecordOutgoing(context.Context, string, int64, string, int64, string) error {
	return nil
}

// trackingProcessor records execution and detects same-conversation overlap.
// trackingProcessor 记录执行并检测同会话并发重叠。
type trackingProcessor struct {
	mu        sync.Mutex
	active    map[string]int
	violation bool
	processed []int64
	failIDs   map[int64]bool
	cur       int64
	maxConc   int64
}

func newTrackingProcessor(failIDs map[int64]bool) *trackingProcessor {
	return &trackingProcessor{
		active:  make(map[string]int),
		failIDs: failIDs,
	}
}

func (p *trackingProcessor) Process(_ context.Context, job domain.Job) error {
	p.mu.Lock()
	p.active[job.ConversationID]++
	if p.active[job.ConversationID] > 1 {
		p.violation = true
	}
	p.processed = append(p.processed, job.ID)
	p.mu.Unlock()

	cur := atomic.AddInt64(&p.cur, 1)
	for {
		max := atomic.LoadInt64(&p.maxConc)
		if cur <= max || atomic.CompareAndSwapInt64(&p.maxConc, max, cur) {
			break
		}
	}
	defer atomic.AddInt64(&p.cur, -1)

	time.Sleep(10 * time.Millisecond)

	p.mu.Lock()
	p.active[job.ConversationID]--
	p.mu.Unlock()

	if p.failIDs[job.ID] {
		return errTestProcess
	}
	return nil
}

var errTestProcess = errorTest{}

type errorTest struct{}

func (errorTest) Error() string { return "test process error" }

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("condition not met within timeout")
}

func makeJob(id int64, conv string) domain.Job {
	return domain.Job{
		ID: id, MessageID: id, ConversationID: conv, BotID: 1,
		Kind: domain.KindGroup, TargetID: 700,
		Message: domain.Message{ID: id, ConversationID: conv},
	}
}

// TestWorkerProcessesJobsWithConversationOrdering verifies per-conversation
// serialization, cross-conversation concurrency and completion recording.
// TestWorkerProcessesJobsWithConversationOrdering 验证单会话串行、跨会话并发
// 与完成记录。
func TestWorkerProcessesJobsWithConversationOrdering(t *testing.T) {
	repo := &fakeJobRepo{jobs: []domain.Job{
		makeJob(1, "A"), makeJob(2, "A"), makeJob(3, "B"),
	}}
	proc := newTrackingProcessor(nil)
	worker, err := NewWorker(repo, proc, WorkerOptions{Concurrency: 4, PollInterval: 2 * time.Millisecond}, discardLogger())
	if err != nil {
		t.Fatalf("new worker: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		waitFor(t, func() bool {
			proc.mu.Lock()
			defer proc.mu.Unlock()
			return len(proc.processed) == 3
		})
		cancel()
	}()
	if err := worker.Run(ctx); err != nil {
		t.Fatalf("run: %v", err)
	}
	proc.mu.Lock()
	defer proc.mu.Unlock()
	if proc.violation {
		t.Fatal("same conversation jobs must not overlap")
	}
	if proc.maxConc > 4 {
		t.Fatalf("concurrency exceeded: %d", proc.maxConc)
	}
	repo.mu.Lock()
	defer repo.mu.Unlock()
	if len(repo.completed) != 3 || len(repo.failed) != 0 || !repo.recovered {
		t.Fatalf("completed=%v failed=%v recovered=%v", repo.completed, repo.failed, repo.recovered)
	}
}

// TestWorkerRestoresJobTraceContext verifies that the worker continues the
// trace stored with the job, marks jobs without one as legacy, and attaches
// session/user attributes.
// TestWorkerRestoresJobTraceContext 验证工作器延续任务里保存的 trace、
// 标注无上下文的历史任务，并附加会话与用户属性。
func TestWorkerRestoresJobTraceContext(t *testing.T) {
	recorder := recordingProvider(t)
	withTrace := makeJob(21, "conv-traced")
	withTrace.Message.SenderID = 200
	withTrace.TraceParent = "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01"
	legacy := makeJob(22, "conv-legacy")
	legacy.Message.SenderID = 201

	repo := &fakeJobRepo{jobs: []domain.Job{withTrace, legacy}}
	proc := newTrackingProcessor(nil)
	worker, err := NewWorker(repo, proc, WorkerOptions{
		Concurrency:  1,
		PollInterval: 2 * time.Millisecond,
		Identity:     observability.NewIdentities("0123456789abcdef0123456789abcdef", false),
	}, discardLogger())
	if err != nil {
		t.Fatalf("new worker: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		waitFor(t, func() bool {
			proc.mu.Lock()
			defer proc.mu.Unlock()
			return len(proc.processed) == 2
		})
		cancel()
	}()
	if err := worker.Run(ctx); err != nil {
		t.Fatalf("run: %v", err)
	}

	wantTraceID := trace.SpanContextFromContext(
		observability.TraceContext{TraceParent: withTrace.TraceParent}.Restore(context.Background()),
	).TraceID()
	seen := map[string]sdktrace.ReadOnlySpan{}
	for _, span := range recorder.Ended() {
		if span.Name() == "qqbot.worker.process" {
			attrs := map[string]string{}
			for _, kv := range span.Attributes() {
				attrs[string(kv.Key)] = kv.Value.Emit()
			}
			seen[attrs["qqbot.conversation_id"]] = span
		}
	}
	traced, ok := seen["conv-traced"]
	if !ok {
		t.Fatalf("worker span for the traced job missing: %v", seen)
	}
	if got := traced.SpanContext().TraceID(); got != wantTraceID {
		t.Fatalf("worker trace id = %s, want %s", got, wantTraceID)
	}
	tracedAttrs := map[string]string{}
	for _, kv := range traced.Attributes() {
		tracedAttrs[string(kv.Key)] = kv.Value.Emit()
	}
	if tracedAttrs[observability.SessionIDKey] != "conv-traced" {
		t.Fatalf("session attribute = %q", tracedAttrs[observability.SessionIDKey])
	}
	if tracedAttrs[observability.UserIDKey] == "" {
		t.Fatal("user attribute must be set when a secret is configured")
	}
	if tracedAttrs["qqbot.legacy_task"] != "false" {
		t.Fatalf("qqbot.legacy_task = %q, want false", tracedAttrs["qqbot.legacy_task"])
	}
	legacySpan, ok := seen["conv-legacy"]
	if !ok {
		t.Fatal("worker span for the legacy job missing")
	}
	legacyAttrs := map[string]string{}
	for _, kv := range legacySpan.Attributes() {
		legacyAttrs[string(kv.Key)] = kv.Value.Emit()
	}
	if legacyAttrs["qqbot.legacy_task"] != "true" {
		t.Fatalf("qqbot.legacy_task = %q, want true", legacyAttrs["qqbot.legacy_task"])
	}
}

// TestWorkerExportsRawQQIdentifiers verifies raw mode records the real
// account, target and sender numbers on the worker span.
// TestWorkerExportsRawQQIdentifiers 验证原始模式在工作器 span 上记录真实账号、
// 目标与发送者号码。
func TestWorkerExportsRawQQIdentifiers(t *testing.T) {
	recorder := recordingProvider(t)
	job := makeJob(31, "conv-raw")
	job.BotID = 1341939164
	job.TargetID = 987654321
	job.Message.SenderID = 1552089234
	repo := &fakeJobRepo{jobs: []domain.Job{job}}
	proc := newTrackingProcessor(nil)
	worker, err := NewWorker(repo, proc, WorkerOptions{
		Concurrency:  1,
		PollInterval: 2 * time.Millisecond,
		Identity:     observability.NewIdentities("", true),
	}, discardLogger())
	if err != nil {
		t.Fatalf("new worker: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		waitFor(t, func() bool {
			proc.mu.Lock()
			defer proc.mu.Unlock()
			return len(proc.processed) == 1
		})
		cancel()
	}()
	if err := worker.Run(ctx); err != nil {
		t.Fatalf("run: %v", err)
	}

	attrs := map[string]string{}
	for _, span := range recorder.Ended() {
		if span.Name() != "qqbot.worker.process" {
			continue
		}
		for _, kv := range span.Attributes() {
			attrs[string(kv.Key)] = kv.Value.AsString()
		}
	}
	for key, want := range map[string]string{
		observability.SessionIDKey: "conv-raw",
		observability.UserIDKey:    "1552089234",
		observability.BotIDKey:     "1341939164",
		observability.TargetIDKey:  "987654321",
		observability.SenderIDKey:  "1552089234",
	} {
		if attrs[key] != want {
			t.Fatalf("%s = %q, want %q (attrs=%v)", key, attrs[key], want, attrs)
		}
	}
	if _, ok := attrs[observability.BotIDHashKey]; ok {
		t.Fatal("raw mode must not also write the hash attribute")
	}
}

// TestWorkerRecordsFailures verifies that processor errors route to FailJob.
// TestWorkerRecordsFailures 验证处理器错误进入 FailJob。
func TestWorkerRecordsFailures(t *testing.T) {
	repo := &fakeJobRepo{jobs: []domain.Job{makeJob(9, "A")}}
	proc := newTrackingProcessor(map[int64]bool{9: true})
	worker, err := NewWorker(repo, proc, WorkerOptions{Concurrency: 1, PollInterval: 2 * time.Millisecond}, discardLogger())
	if err != nil {
		t.Fatalf("new worker: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		waitFor(t, func() bool {
			repo.mu.Lock()
			defer repo.mu.Unlock()
			return len(repo.failed) == 1
		})
		cancel()
	}()
	if err := worker.Run(ctx); err != nil {
		t.Fatalf("run: %v", err)
	}
	repo.mu.Lock()
	defer repo.mu.Unlock()
	if len(repo.failed) != 1 || repo.failed[0] != 9 || len(repo.completed) != 0 {
		t.Fatalf("completed=%v failed=%v", repo.completed, repo.failed)
	}
}
