// Tests for the bounded worker: per-conversation ordering, failure handling.
// 有界工作器测试：单会话顺序与失败处理。
package infrastructure

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"

	auditcontracts "github.com/HappyLadySauceM/Nagi/internal/audit/contracts"
	auditdomain "github.com/HappyLadySauceM/Nagi/internal/audit/domain"
	"github.com/HappyLadySauceM/Nagi/internal/conversation/domain"
	"github.com/HappyLadySauceM/Nagi/internal/observability"
)

// fakeJobRepo serves a fixed job list and records outcomes.
// fakeJobRepo 提供固定任务列表并记录结果。
type fakeJobRepo struct {
	mu        sync.Mutex
	jobs      []domain.Job
	completed []int64
	failed    []int64
	failure   []struct {
		code     string
		attempts int
	}
	recovered bool
	claimed   map[int64]domain.Job
}

func (f *fakeJobRepo) ClaimReadyJobs(_ context.Context, limit int) ([]domain.Job, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.claimed == nil {
		f.claimed = map[int64]domain.Job{}
	}
	busy := map[string]bool{}
	for _, j := range f.claimed {
		busy[j.ConversationID] = true
	}
	var out, remaining []domain.Job
	for _, j := range f.jobs {
		if len(out) < limit && !busy[j.ConversationID] {
			out = append(out, j)
			busy[j.ConversationID] = true
			f.claimed[j.ID] = j
		} else {
			remaining = append(remaining, j)
		}
	}
	f.jobs = remaining
	return out, nil
}

func (f *fakeJobRepo) ReleaseJob(_ context.Context, _ int64, claim ...string) error {
	return nil
}

func (f *fakeJobRepo) CompleteJob(_ context.Context, jobID int64, claim ...string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.claimed, jobID)
	f.completed = append(f.completed, jobID)
	return nil
}

func (f *fakeJobRepo) FailJob(_ context.Context, jobID int64, code string, _ time.Duration, attempts int, claim ...string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.claimed, jobID)
	f.failed = append(f.failed, jobID)
	f.failure = append(f.failure, struct {
		code     string
		attempts int
	}{code: code, attempts: attempts})
	return nil
}

func (f *fakeJobRepo) RecoverStaleJobs(_ context.Context, policies ...domain.RecoveryPolicy) error {
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
func (f *fakeJobRepo) RecordOutgoing(context.Context, string, int64, string, int64, string, string, ...string) error {
	return nil
}
func (f *fakeJobRepo) PurgeBefore(context.Context, time.Time, int) (int, error) { return 0, nil }

// trackingProcessor records execution and detects same-conversation overlap.
// trackingProcessor 记录执行并检测同会话并发重叠。
type trackingProcessor struct {
	mu        sync.Mutex
	active    map[string]int
	violation bool
	processed []int64
	failIDs   map[int64]bool
	err       error
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
	if p.err != nil {
		err := p.err
		p.active[job.ConversationID]--
		p.mu.Unlock()
		return err
	}
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

type fakeAuditRecorder struct {
	mu            sync.Mutex
	startErr      error
	eventErr      error
	finishErr     error
	started       []auditdomain.AIRun
	events        []auditdomain.AIEvent
	finishedRuns  []string
	finishedEvent []string
}

func (f *fakeAuditRecorder) StartRun(_ context.Context, run auditdomain.AIRun) (auditdomain.AIRun, error) {
	if f.startErr != nil {
		return auditdomain.AIRun{}, f.startErr
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	run.ID = "run-1"
	f.started = append(f.started, run)
	return run, nil
}

func (f *fakeAuditRecorder) FinishRun(_ context.Context, id, status, code string) error {
	if f.finishErr != nil {
		return f.finishErr
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.finishedRuns = append(f.finishedRuns, id+":"+status+":"+code)
	return nil
}

func (f *fakeAuditRecorder) RecordEvent(_ context.Context, event auditdomain.AIEvent) (auditdomain.AIEvent, error) {
	if f.eventErr != nil {
		return auditdomain.AIEvent{}, f.eventErr
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	event.ID = "event-1"
	f.events = append(f.events, event)
	return event, nil
}

func (f *fakeAuditRecorder) FinishEvent(_ context.Context, id, status, code string, _ []byte) error {
	if f.finishErr != nil {
		return f.finishErr
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.finishedEvent = append(f.finishedEvent, id+":"+status+":"+code)
	return nil
}

func (f *fakeAuditRecorder) RecordArtifact(context.Context, auditdomain.AIArtifact) (auditdomain.AIArtifact, error) {
	return auditdomain.AIArtifact{}, nil
}

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

// TestWorkerLinksReceiveTrace verifies that the worker starts an independent
// processing trace linked to the persisted receive trace, marks legacy jobs,
// and attaches session/user attributes.
// TestWorkerLinksReceiveTrace 验证工作器开启独立处理 trace 并链接到持久化的接收 trace，
// 标注无上下文的历史任务，并附加会话与用户属性。
func TestWorkerLinksReceiveTrace(t *testing.T) {
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
		if span.Name() == "nagi.message.process" {
			attrs := map[string]string{}
			for _, kv := range span.Attributes() {
				attrs[string(kv.Key)] = kv.Value.Emit()
			}
			seen[attrs["nagi.conversation_id"]] = span
		}
	}
	traced, ok := seen["conv-traced"]
	if !ok {
		t.Fatalf("worker span for the traced job missing: %v", seen)
	}
	if got := traced.SpanContext().TraceID(); got == wantTraceID {
		t.Fatalf("worker trace id must be independent from receive trace: %s", got)
	}
	if len(traced.Links()) != 1 || traced.Links()[0].SpanContext.TraceID() != wantTraceID {
		t.Fatalf("worker trace links = %+v, want receive trace %s", traced.Links(), wantTraceID)
	}
	tracedAttrs := map[string]string{}
	for _, kv := range traced.Attributes() {
		tracedAttrs[string(kv.Key)] = kv.Value.Emit()
	}
	if tracedAttrs[observability.SessionIDKey] != "conv-traced" {
		t.Fatalf("session attribute = %q", tracedAttrs[observability.SessionIDKey])
	}
	if tracedAttrs[observability.TraceNameKey] != "nagi.message.process" {
		t.Fatalf("trace name = %q", tracedAttrs[observability.TraceNameKey])
	}
	if tracedAttrs[observability.UserIDKey] == "" {
		t.Fatal("user attribute must be set when a secret is configured")
	}
	if tracedAttrs["nagi.legacy_task"] != "false" {
		t.Fatalf("nagi.legacy_task = %q, want false", tracedAttrs["nagi.legacy_task"])
	}
	legacySpan, ok := seen["conv-legacy"]
	if !ok {
		t.Fatal("worker span for the legacy job missing")
	}
	legacyAttrs := map[string]string{}
	for _, kv := range legacySpan.Attributes() {
		legacyAttrs[string(kv.Key)] = kv.Value.Emit()
	}
	if legacyAttrs["nagi.legacy_task"] != "true" {
		t.Fatalf("nagi.legacy_task = %q, want true", legacyAttrs["nagi.legacy_task"])
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
		if span.Name() != "nagi.message.process" {
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

func TestWorkerDoesNotRetryUncertainSideEffect(t *testing.T) {
	repo := &fakeJobRepo{jobs: []domain.Job{makeJob(81, "audit-unknown")}}
	proc := newTrackingProcessor(nil)
	proc.err = auditcontracts.UnsafeToRetry("tool.execute", errors.New("audit unavailable"))
	worker, err := NewWorker(repo, proc, WorkerOptions{Concurrency: 1, PollInterval: 2 * time.Millisecond, MaxAttempts: 5}, discardLogger())
	if err != nil {
		t.Fatalf("new worker: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		waitFor(t, func() bool {
			repo.mu.Lock()
			defer repo.mu.Unlock()
			return len(repo.failure) == 1
		})
		cancel()
	}()
	if err := worker.Run(ctx); err != nil {
		t.Fatalf("run: %v", err)
	}
	repo.mu.Lock()
	defer repo.mu.Unlock()
	if got := repo.failure[0]; got.code != "side_effect_audit_unknown" || got.attempts != 1 {
		t.Fatalf("failure = %+v, want side_effect_audit_unknown/1", got)
	}
}

type sideEffectProcessor struct{}

func (sideEffectProcessor) Process(ctx context.Context, _ domain.Job) error {
	auditcontracts.MarkSideEffect(ctx)
	return nil
}

func TestWorkerDoesNotRetryWhenAuditFinishFailsAfterSideEffect(t *testing.T) {
	repo := &fakeJobRepo{jobs: []domain.Job{makeJob(82, "audit-finish-unknown")}}
	worker, err := NewWorker(repo, sideEffectProcessor{}, WorkerOptions{
		Concurrency: 1, PollInterval: 2 * time.Millisecond, MaxAttempts: 5,
		Audit: &fakeAuditRecorder{finishErr: errors.New("audit unavailable")},
	}, discardLogger())
	if err != nil {
		t.Fatalf("new worker: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		waitFor(t, func() bool {
			repo.mu.Lock()
			defer repo.mu.Unlock()
			return len(repo.failure) == 1
		})
		cancel()
	}()
	if err := worker.Run(ctx); err != nil {
		t.Fatalf("run: %v", err)
	}
	repo.mu.Lock()
	defer repo.mu.Unlock()
	if got := repo.failure[0]; got.code != "side_effect_audit_unknown" || got.attempts != 1 {
		t.Fatalf("failure = %+v, want side_effect_audit_unknown/1", got)
	}
}

func TestWorkerStopsBeforeProcessorWhenAuditStartFails(t *testing.T) {
	repo := &fakeJobRepo{jobs: []domain.Job{makeJob(71, "audit-failed")}}
	proc := newTrackingProcessor(nil)
	worker, err := NewWorker(repo, proc, WorkerOptions{
		Concurrency: 1, PollInterval: time.Millisecond,
		Audit: &fakeAuditRecorder{startErr: errors.New("audit unavailable")},
	}, discardLogger())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		waitFor(t, func() bool {
			repo.mu.Lock()
			defer repo.mu.Unlock()
			return len(repo.failed) == 1
		})
		cancel()
	}()
	if err := worker.Run(ctx); err != nil {
		t.Fatal(err)
	}
	proc.mu.Lock()
	defer proc.mu.Unlock()
	if len(proc.processed) != 0 {
		t.Fatalf("processor ran without audit record: %v", proc.processed)
	}
	repo.mu.Lock()
	defer repo.mu.Unlock()
	if len(repo.failed) != 1 || repo.failed[0] != 71 || len(repo.completed) != 0 {
		t.Fatalf("completed=%v failed=%v", repo.completed, repo.failed)
	}
}

func TestWorkerRecordsAuditRunAroundSuccessfulProcess(t *testing.T) {
	repo := &fakeJobRepo{jobs: []domain.Job{makeJob(72, "audit-success")}}
	proc := newTrackingProcessor(nil)
	audit := &fakeAuditRecorder{}
	worker, err := NewWorker(repo, proc, WorkerOptions{
		Concurrency: 1, PollInterval: time.Millisecond, Audit: audit,
		ConfigVersion: func() int64 { return 9 },
	}, discardLogger())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		waitFor(t, func() bool {
			repo.mu.Lock()
			defer repo.mu.Unlock()
			return len(repo.completed) == 1
		})
		cancel()
	}()
	if err := worker.Run(ctx); err != nil {
		t.Fatal(err)
	}
	audit.mu.Lock()
	defer audit.mu.Unlock()
	if len(audit.started) != 1 || audit.started[0].ConfigVersion != 9 || len(audit.events) != 1 {
		t.Fatalf("started=%+v events=%+v", audit.started, audit.events)
	}
	if len(audit.finishedEvent) != 1 || len(audit.finishedRuns) != 1 || audit.finishedRuns[0] != "run-1:done:" {
		t.Fatalf("event finish=%v run finish=%v", audit.finishedEvent, audit.finishedRuns)
	}
}

type failedRenewalRepo struct{ *fakeJobRepo }

func (r *failedRenewalRepo) RenewClaims(context.Context, []string) ([]string, error) {
	return nil, fmt.Errorf("database unavailable")
}

type leaseBlockingProcessor struct {
	started   chan struct{}
	cancelled chan struct{}
}

func (p *leaseBlockingProcessor) Process(ctx context.Context, _ domain.Job) error {
	close(p.started)
	<-ctx.Done()
	close(p.cancelled)
	return ctx.Err()
}

func TestWorkerCancelsExecutionWhenRenewalFails(t *testing.T) {
	job := makeJob(9123, "lease-worker")
	job.ClaimToken = "test-worker-claim"
	repo := &failedRenewalRepo{&fakeJobRepo{jobs: []domain.Job{job}}}
	processor := &leaseBlockingProcessor{started: make(chan struct{}), cancelled: make(chan struct{})}
	worker, err := NewWorker(repo, processor, WorkerOptions{Concurrency: 1, PollInterval: time.Millisecond, RenewInterval: 5 * time.Millisecond}, discardLogger())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- worker.Run(ctx) }()
	select {
	case <-processor.started:
	case <-time.After(time.Second):
		t.Fatal("processor did not start")
	}
	select {
	case <-processor.cancelled:
	case <-time.After(time.Second):
		t.Fatal("renewal failure did not cancel execution")
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("worker did not stop")
	}
}
