// Tests for the decision pipeline orchestration.
// 决策流水线编排的测试。
package application

import (
	"context"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	obdomain "github.com/HappyLadySauceM/QQBot/internal/onebot/domain"
	scopedomain "github.com/HappyLadySauceM/QQBot/internal/scope/domain"

	"github.com/HappyLadySauceM/QQBot/internal/intent/domain"
)

// obdomainSegment is a test alias for the OneBot segment type.
// obdomainSegment 是 OneBot 消息段类型的测试别名。
type obdomainSegment = obdomain.Segment

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// fakeScopes resolves policies from a map.
// fakeScopes 从映射解析策略。
type fakeScopes struct{ policies map[string]scopedomain.Policy }

func (f *fakeScopes) Resolve(_ context.Context, botID int64, t scopedomain.ScopeType, id int64) (scopedomain.Policy, error) {
	return f.policies[keyOf(botID, string(t), id)], nil
}

func keyOf(botID int64, typ string, id int64) string {
	return strings.Join([]string{itoaInt(botID), typ, itoaInt(id)}, ":")
}

func itoaInt(v int64) string {
	if v == 0 {
		return "0"
	}
	var b []byte
	neg := v < 0
	if neg {
		v = -v
	}
	for v > 0 {
		b = append([]byte{byte('0' + v%10)}, b...)
		v /= 10
	}
	if neg {
		return "-" + string(b)
	}
	return string(b)
}

// fakeHistory records the limit and returns fixed messages.
// fakeHistory 记录 limit 并返回固定消息。
type fakeHistory struct {
	limit    int
	messages []domain.Message
}

func (f *fakeHistory) RecentIntentMessages(_ context.Context, _ string, limit int) ([]domain.Message, error) {
	f.limit = limit
	return f.messages, nil
}

// fakeActivity returns canned bot reply metadata and records the conversation id.
// fakeActivity 返回预置的机器人发言元数据并记录会话 ID。
type fakeActivity struct {
	conversationID  string
	lastReplyAt     time.Time
	repliesLastHour int
	err             error
	calls           int
}

func (f *fakeActivity) BotReplyMeta(_ context.Context, conversationID string) (time.Time, int, error) {
	f.calls++
	f.conversationID = conversationID
	if f.err != nil {
		return time.Time{}, 0, f.err
	}
	return f.lastReplyAt, f.repliesLastHour, nil
}

// fakeSemantic returns a canned answer and records the input.
// fakeSemantic 返回预置回答并记录输入。
type fakeSemantic struct {
	input  domain.Input
	answer domain.SemanticAnswer
	err    error
	calls  int
}

func (f *fakeSemantic) Decide(_ context.Context, in domain.Input) (domain.SemanticAnswer, error) {
	f.calls++
	f.input = in
	return f.answer, f.err
}

// fakeRecorder records decisions.
// fakeRecorder 记录决策。
type fakeRecorder struct{ decisions []domain.FinalDecision }

func (f *fakeRecorder) Record(_ context.Context, _ int64, d domain.FinalDecision) error {
	f.decisions = append(f.decisions, d)
	return nil
}

// fakeExecutor records dispatched replies.
// fakeExecutor 记录分发的回复。
type fakeExecutor struct{ tasks []domain.Task }

func (f *fakeExecutor) ExecuteReply(_ context.Context, task domain.Task, _ []domain.Message) error {
	f.tasks = append(f.tasks, task)
	return nil
}

func groupTask() domain.Task {
	return domain.Task{
		JobID: 11, BotID: 100, Kind: "group", TargetID: 700, ConversationID: "conv-1",
		Message: domain.Message{MessageID: 5001, SenderID: 200, Text: "那你觉得呢？"},
	}
}

func newDecider(t *testing.T, opts Options) *Decider {
	t.Helper()
	if opts.Logger == nil {
		opts.Logger = testLogger()
	}
	d, err := NewDecider(opts)
	if err != nil {
		t.Fatalf("new decider: %v", err)
	}
	return d
}

// TestProcessTaskSkipsSemanticForExplicitMention verifies the fast path.
// TestProcessTaskSkipsSemanticForExplicitMention 验证明确指向的快速路径。
func TestProcessTaskSkipsSemanticForExplicitMention(t *testing.T) {
	scopes := &fakeScopes{policies: map[string]scopedomain.Policy{
		keyOf(100, "group", 700): {ScopeType: scopedomain.ScopeGroup, ScopeID: 700, Enabled: true, ReplyMode: scopedomain.ModeIntentBased, MinReplyIntervalMS: 0, MaxRepliesPerHour: 0},
	}}
	semantic := &fakeSemantic{}
	recorder := &fakeRecorder{}
	executor := &fakeExecutor{}
	d := newDecider(t, Options{Scopes: scopes, Semantic: semantic, Recorder: recorder, Executor: executor, Thresholds: thresholds, Provider: "jev", Model: "jev-latest"})

	task := groupTask()
	task.Segments = []obdomain.Segment{domainSegmentForTest("100")}
	if err := d.ProcessTask(context.Background(), task); err != nil {
		t.Fatalf("process: %v", err)
	}
	if semantic.calls != 0 {
		t.Fatal("explicit mention must not call the semantic decider")
	}
	if len(recorder.decisions) != 1 || recorder.decisions[0].Decision != domain.DecisionReply || recorder.decisions[0].ReasonCode != "explicit_mention" {
		t.Fatalf("decisions = %+v", recorder.decisions)
	}
	if len(executor.tasks) != 1 || executor.tasks[0].JobID != 11 {
		t.Fatalf("executor tasks = %+v", executor.tasks)
	}
	if recorder.decisions[0].Provider != "jev" || recorder.decisions[0].Model != "jev-latest" {
		t.Fatalf("provider/model not stamped: %+v", recorder.decisions[0])
	}
}

// TestProcessTaskGatesHistoryOnPolicy verifies that history is only read when the
// policy allows it.
// TestProcessTaskGatesHistoryOnPolicy 验证仅在策略允许时读取历史。
func TestProcessTaskGatesHistoryOnPolicy(t *testing.T) {
	policy := scopedomain.Policy{ScopeType: scopedomain.ScopeGroup, ScopeID: 700, Enabled: true,
		ReplyMode: scopedomain.ModeIntentBased, AllowHistoryRead: true, MinReplyIntervalMS: 0, MaxRepliesPerHour: 0}
	history := &fakeHistory{messages: []domain.Message{}}
	semantic := &fakeSemantic{answer: domain.SemanticAnswer{Available: true, ContinuesBotThread: 0.9, AddressedToOther: 0.1, IntentCategory: "chat"}}
	d := newDecider(t, Options{Scopes: &fakeScopes{policies: map[string]scopedomain.Policy{keyOf(100, "group", 700): policy}},
		History: history, Semantic: semantic, Recorder: &fakeRecorder{}, Thresholds: thresholds})

	if err := d.ProcessTask(context.Background(), groupTask()); err != nil {
		t.Fatalf("process: %v", err)
	}
	if history.limit == 0 {
		t.Fatal("history should be read when the policy allows it")
	}
	if semantic.calls != 1 {
		t.Fatalf("semantic calls = %d", semantic.calls)
	}

	// Without AllowHistoryRead the history reader must not be called.
	// 关闭 AllowHistoryRead 后不得读取历史。
	policy.AllowHistoryRead = false
	history2 := &fakeHistory{}
	d2 := newDecider(t, Options{Scopes: &fakeScopes{policies: map[string]scopedomain.Policy{keyOf(100, "group", 700): policy}},
		History: history2, Semantic: semantic, Recorder: &fakeRecorder{}, Thresholds: thresholds})
	if err := d2.ProcessTask(context.Background(), groupTask()); err != nil {
		t.Fatalf("process 2: %v", err)
	}
	if history2.limit != 0 {
		t.Fatal("history must not be read without policy consent")
	}
}

// TestProcessTaskDegradesWithoutSemantic verifies the silent fallback.
// TestProcessTaskDegradesWithoutSemantic 验证语义不可用时静默降级。
func TestProcessTaskDegradesWithoutSemantic(t *testing.T) {
	policy := scopedomain.Policy{ScopeType: scopedomain.ScopeGroup, ScopeID: 700, Enabled: true,
		ReplyMode: scopedomain.ModeIntentBased, MinReplyIntervalMS: 0, MaxRepliesPerHour: 0}
	recorder := &fakeRecorder{}
	executor := &fakeExecutor{}
	d := newDecider(t, Options{Scopes: &fakeScopes{policies: map[string]scopedomain.Policy{keyOf(100, "group", 700): policy}},
		Semantic: &fakeSemantic{err: context.DeadlineExceeded}, Recorder: recorder, Executor: executor, Thresholds: thresholds})

	if err := d.ProcessTask(context.Background(), groupTask()); err != nil {
		t.Fatalf("process: %v", err)
	}
	if len(recorder.decisions) != 1 || recorder.decisions[0].Decision != domain.DecisionSilent || recorder.decisions[0].ReasonCode != "jev_unavailable" {
		t.Fatalf("decisions = %+v", recorder.decisions)
	}
	if len(executor.tasks) != 0 {
		t.Fatal("no reply may be dispatched on semantic failure")
	}
}

func domainSegmentForTest(qq string) obdomainSegment {
	return obdomainSegment{Type: "at", Data: map[string]string{"qq": qq}}
}

// TestProcessTaskRateLimitsWithoutHistoryAuthorization verifies that cooldown
// enforcement survives AllowHistoryRead=false because activity metadata is
// fetched independently of the history reader.
// TestProcessTaskRateLimitsWithoutHistoryAuthorization 验证关闭 AllowHistoryRead
// 后冷却仍生效，因为活动元数据独立于历史读取获取。
func TestProcessTaskRateLimitsWithoutHistoryAuthorization(t *testing.T) {
	policy := scopedomain.Policy{ScopeType: scopedomain.ScopeGroup, ScopeID: 700, Enabled: true,
		ReplyMode: scopedomain.ModeMentionOnly, AllowHistoryRead: false,
		MinReplyIntervalMS: 10000, MaxRepliesPerHour: 20}
	activity := &fakeActivity{lastReplyAt: time.Now().Add(-2 * time.Second), repliesLastHour: 1}
	semantic := &fakeSemantic{}
	recorder := &fakeRecorder{}
	executor := &fakeExecutor{}
	d := newDecider(t, Options{
		Scopes:   &fakeScopes{policies: map[string]scopedomain.Policy{keyOf(100, "group", 700): policy}},
		Activity: activity, Semantic: semantic, Recorder: recorder, Executor: executor,
		Thresholds: thresholds,
	})

	task := groupTask()
	task.Segments = []obdomain.Segment{domainSegmentForTest("100")}
	if err := d.ProcessTask(context.Background(), task); err != nil {
		t.Fatalf("process: %v", err)
	}
	if activity.calls != 1 || activity.conversationID != "conv-1" {
		t.Fatalf("activity = %+v", activity)
	}
	if len(recorder.decisions) != 1 || recorder.decisions[0].Decision != domain.DecisionSilent ||
		recorder.decisions[0].ReasonCode != "cooldown" {
		t.Fatalf("decisions = %+v", recorder.decisions)
	}
	if len(executor.tasks) != 0 || semantic.calls != 0 {
		t.Fatal("cooled-down explicit mention must neither call semantic nor reply")
	}
}

// TestProcessTaskFailsClosedWhenActivityUnavailable verifies that activity-store
// failures fail the job (retryable) instead of silently bypassing the rate gate.
// TestProcessTaskFailsClosedWhenActivityUnavailable 验证活动存储故障使任务失败
// （可重试），而不是静默绕过频率闸门。
func TestProcessTaskFailsClosedWhenActivityUnavailable(t *testing.T) {
	policy := scopedomain.Policy{ScopeType: scopedomain.ScopeGroup, ScopeID: 700, Enabled: true,
		ReplyMode: scopedomain.ModeMentionOnly, AllowHistoryRead: false,
		MinReplyIntervalMS: 10000, MaxRepliesPerHour: 20}
	activity := &fakeActivity{err: context.DeadlineExceeded}
	d := newDecider(t, Options{
		Scopes:   &fakeScopes{policies: map[string]scopedomain.Policy{keyOf(100, "group", 700): policy}},
		Activity: activity, Recorder: &fakeRecorder{}, Thresholds: thresholds,
	})
	if err := d.ProcessTask(context.Background(), groupTask()); err == nil {
		t.Fatal("expected activity failure to propagate as a retryable error")
	}
}
