// Tests for the decision pipeline orchestration.
// 决策流水线编排的测试。
package application

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	auditcontracts "github.com/HappyLadySauceM/Nagi/internal/audit/contracts"
	auditdomain "github.com/HappyLadySauceM/Nagi/internal/audit/domain"
	obdomain "github.com/HappyLadySauceM/Nagi/internal/onebot/domain"
	scopedomain "github.com/HappyLadySauceM/Nagi/internal/scope/domain"

	"github.com/HappyLadySauceM/Nagi/internal/intent/domain"
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
type fakeRecorder struct {
	decisions []domain.FinalDecision
	err       error
}

// fakeAIRunRecorder captures local run events without requiring a database.
// fakeAIRunRecorder 在无需数据库的情况下捕获本地运行事件。
type fakeAIRunRecorder struct {
	events   []auditdomain.AIEvent
	finished []auditdomain.AIEvent
}

func (*fakeAIRunRecorder) StartRun(context.Context, auditdomain.AIRun) (auditdomain.AIRun, error) {
	return auditdomain.AIRun{}, nil
}

func (*fakeAIRunRecorder) FinishRun(context.Context, string, string, string) error { return nil }

func (f *fakeAIRunRecorder) RecordEvent(_ context.Context, event auditdomain.AIEvent) (auditdomain.AIEvent, error) {
	event.ID = "event-1"
	f.events = append(f.events, event)
	return event, nil
}

func (f *fakeAIRunRecorder) FinishEvent(_ context.Context, id, status, code string, output []byte) error {
	f.finished = append(f.finished, auditdomain.AIEvent{ID: id, Status: status, ErrorCode: code, Output: output})
	return nil
}

func (*fakeAIRunRecorder) RecordArtifact(context.Context, auditdomain.AIArtifact) (auditdomain.AIArtifact, error) {
	return auditdomain.AIArtifact{}, nil
}

func (f *fakeRecorder) Record(_ context.Context, _ int64, d domain.FinalDecision, claim ...string) error {
	if f.err != nil {
		return f.err
	}
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
	steps := recorder.decisions[0].Steps
	if steps[0].Branch != "blocked" || steps[0].ReasonCode != "cooldown" {
		t.Fatalf("policy node = %+v", steps[0])
	}
	if steps[3].Status != domain.NodeStatusSkipped || steps[3].Detail["called"] != false {
		t.Fatalf("blocked semantic node = %+v", steps[3])
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

// TestProcessTaskRecordsExplainableSteps verifies that every decision carries
// policy, rules, semantic and final steps. A skipped semantic step remains
// explicit so an auditor does not have to infer why Jev was not called.
// TestProcessTaskRecordsExplainableSteps 验证每条决策都带策略、规则、语义与最终步骤；
// 即使跳过语义也明确记录原因，审计方无需反推为何没有调用 Jev。
func TestProcessTaskRecordsExplainableSteps(t *testing.T) {
	scopes := &fakeScopes{policies: map[string]scopedomain.Policy{
		keyOf(100, "group", 700): {ScopeType: scopedomain.ScopeGroup, ScopeID: 700, Enabled: true, ReplyMode: scopedomain.ModeAlwaysReply},
	}}
	recorder := &fakeRecorder{}
	d := newDecider(t, Options{Scopes: scopes, Recorder: recorder, Thresholds: thresholds, Provider: "jev", Model: "jev-latest"})
	if err := d.ProcessTask(context.Background(), groupTask()); err != nil {
		t.Fatalf("process: %v", err)
	}
	if len(recorder.decisions) != 1 {
		t.Fatalf("recorded %d decisions", len(recorder.decisions))
	}
	steps := recorder.decisions[0].Steps
	if len(steps) != 5 {
		t.Fatalf("steps = %d, want policy/context/rules/semantic/final", len(steps))
	}
	if steps[0].Phase != domain.StepPolicy || steps[1].Phase != domain.StepContext || steps[2].Phase != domain.StepRules || steps[3].Phase != domain.StepSemantic || steps[4].Phase != domain.StepFinal {
		t.Fatalf("phases = %+v", steps)
	}
	if steps[3].Detail["SkipReason"] != "always_reply" || steps[4].ReasonCode != "always_reply" {
		t.Fatalf("steps = %+v", steps)
	}

	// With intent_based the pipeline asks Jev, so a semantic step appears.
	scopes.policies[keyOf(100, "group", 700)] = scopedomain.Policy{
		ScopeType: scopedomain.ScopeGroup, ScopeID: 700, Enabled: true, ReplyMode: scopedomain.ModeIntentBased,
	}
	recorder.decisions = nil
	semantic := &fakeSemantic{}
	d = newDecider(t, Options{Scopes: scopes, Semantic: semantic, Recorder: recorder, Thresholds: thresholds, Provider: "jev", Model: "jev-latest"})
	if err := d.ProcessTask(context.Background(), groupTask()); err != nil {
		t.Fatalf("process with semantics: %v", err)
	}
	steps = recorder.decisions[0].Steps
	if len(steps) != 5 || steps[3].Phase != domain.StepSemantic || steps[3].Detail["Called"] != true {
		t.Fatalf("semantic steps = %+v", steps)
	}
	if steps[3].Detail["called"] != true || steps[3].Detail["available"] != false {
		t.Fatalf("legacy semantic detail aliases = %+v", steps[3].Detail)
	}
	if _, ok := steps[4].Detail["thresholds"].(map[string]float64); !ok {
		t.Fatalf("legacy final thresholds alias = %+v", steps[4].Detail)
	}
}

// TestProcessTaskWritesDecisionRunEvent verifies the timeline retains the
// exact explainable decision while keeping message bodies out of this event.
// TestProcessTaskWritesDecisionRunEvent 验证时间线保留完整可解释决策，且不将消息正文写入该事件。
func TestProcessTaskWritesDecisionRunEvent(t *testing.T) {
	scopes := &fakeScopes{policies: map[string]scopedomain.Policy{
		keyOf(100, "group", 700): {ScopeType: scopedomain.ScopeGroup, ScopeID: 700, Enabled: true, ReplyMode: scopedomain.ModeAlwaysReply},
	}}
	decisionRecorder := &fakeRecorder{}
	runRecorder := &fakeAIRunRecorder{}
	d := newDecider(t, Options{Scopes: scopes, Recorder: decisionRecorder, Thresholds: thresholds, Provider: "jev", Model: "jev-latest"})
	ctx := auditcontracts.WithRun(context.Background(), runRecorder, "run-1")
	if err := d.ProcessTask(ctx, groupTask()); err != nil {
		t.Fatalf("process: %v", err)
	}
	if len(runRecorder.events) != 1 || runRecorder.events[0].Stage != "intent.decision" || runRecorder.events[0].Sequence != 1 {
		t.Fatalf("events = %+v", runRecorder.events)
	}
	var input map[string]any
	if err := json.Unmarshal(runRecorder.events[0].Input, &input); err != nil {
		t.Fatalf("decode event input: %v", err)
	}
	if _, ok := input["text"]; ok {
		t.Fatalf("decision event must not include message text: %s", runRecorder.events[0].Input)
	}
	if len(runRecorder.finished) != 1 || runRecorder.finished[0].Status != "succeeded" {
		t.Fatalf("finished events = %+v", runRecorder.finished)
	}
	var output map[string]any
	if err := json.Unmarshal(runRecorder.finished[0].Output, &output); err != nil {
		t.Fatalf("decode event output: %v", err)
	}
	decision, ok := output["decision"].(map[string]any)
	if !ok || decision["ReasonCode"] != "always_reply" {
		t.Fatalf("decision output = %+v", output)
	}
	if strings.Contains(string(runRecorder.finished[0].Output), "那你觉得呢？") {
		t.Fatalf("decision output must not include message text: %s", runRecorder.finished[0].Output)
	}
	if _, exists := decision["Snapshot"]; exists {
		t.Fatalf("decision output must not include snapshot input: %+v", decision)
	}
}

func TestProcessTaskStopsWhenDecisionRecordingFails(t *testing.T) {
	recorder := &fakeRecorder{err: context.DeadlineExceeded}
	executor := &fakeExecutor{}
	d := newDecider(t, Options{
		Scopes: &fakeScopes{policies: map[string]scopedomain.Policy{
			keyOf(100, "group", 700): {ScopeType: scopedomain.ScopeGroup, ScopeID: 700, Enabled: true, ReplyMode: scopedomain.ModeAlwaysReply},
		}},
		Recorder: recorder, Executor: executor, Thresholds: thresholds,
	})
	if err := d.ProcessTask(context.Background(), groupTask()); err == nil {
		t.Fatal("decision recording failure must stop the run")
	}
	if len(executor.tasks) != 0 {
		t.Fatal("reply executed after the required decision audit failed")
	}
}

func TestSemanticSimulationNeverRecordsOrExecutesReply(t *testing.T) {
	semantic := &fakeSemantic{answer: domain.SemanticAnswer{Available: true, DirectedToBot: 0.9}}
	recorder := &fakeRecorder{}
	executor := &fakeExecutor{}
	d := newDecider(t, Options{Scopes: &fakeScopes{}, Semantic: semantic, Recorder: recorder, Executor: executor, Thresholds: thresholds, Provider: "test-local", Model: "decision-model"})
	in := baseInput(scopedomain.ModeIntentBased)
	rule, _ := EvaluateRuleChain(in, nil, nil)
	final, steps := d.EvaluateSimulation(context.Background(), in, rule, true)
	if final.Decision != domain.DecisionReply || semantic.calls != 1 || len(steps) != 5 {
		t.Fatalf("semantic simulation: %+v calls=%d steps=%d", final, semantic.calls, len(steps))
	}
	if len(recorder.decisions) != 0 || len(executor.tasks) != 0 {
		t.Fatal("simulation caused persistence or delivery")
	}
	rule = domain.RuleResult{Blocked: true, ReasonCode: "policy_disabled"}
	final, _ = d.EvaluateSimulation(context.Background(), in, rule, true)
	if final.ReasonCode != "policy_disabled" || semantic.calls != 1 {
		t.Fatalf("blocked simulation called model: %+v calls=%d", final, semantic.calls)
	}
}

type simulationActivity struct{ fakeActivity }

func (*simulationActivity) ConversationIDForScope(context.Context, int64, string, int64) (string, error) {
	return "simulation-conversation", nil
}

func TestFullSimulationUsesActualHourlyActivity(t *testing.T) {
	semantic := &fakeSemantic{answer: domain.SemanticAnswer{Available: true, DirectedToBot: 0.99}}
	activity := &simulationActivity{fakeActivity: fakeActivity{repliesLastHour: 1, lastReplyAt: time.Now().Add(-time.Hour)}}
	d := newDecider(t, Options{Scopes: &fakeScopes{}, Semantic: semantic, Activity: activity, Recorder: &fakeRecorder{}, Thresholds: thresholds})
	in := baseInput(scopedomain.ModeIntentBased)
	in.Policy.MinReplyIntervalMS = 0
	in.Policy.MaxRepliesPerHour = 1
	in.Activity = domain.BotActivity{Known: true, RepliesLastHour: 0}
	prepared, err := d.PrepareSimulation(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	if prepared.Activity.RepliesLastHour != 1 {
		t.Fatalf("simulation lost real hourly usage: %+v", prepared.Activity)
	}
	rule, _ := EvaluateRuleChain(prepared, nil, nil)
	final, _ := d.EvaluateSimulation(context.Background(), prepared, rule, true)
	if final.ReasonCode != "rate_limited" || semantic.calls != 0 {
		t.Fatalf("full simulation bypassed actual quota: %+v calls=%d", final, semantic.calls)
	}
}

type transcriptFixture struct {
	calls        int
	saved        string
	onTranscribe func()
}

func (f *transcriptFixture) Transcribe(_ context.Context, _ int64, _ []obdomain.Segment, text string) (string, error) {
	f.calls++
	if f.onTranscribe != nil {
		f.onTranscribe()
	}
	return text + "转写内容", nil
}
func (f *transcriptFixture) SaveTranscript(_ context.Context, _ int64, _ string, text string) error {
	f.saved = text
	return nil
}

// TestDisabledIntentStillTranscribesAuthorizedVoice verifies transcription is independent of reply participation.
// TestDisabledIntentStillTranscribesAuthorizedVoice 验证语音转写独立于意图回复开关。
func TestDisabledIntentStillTranscribesAuthorizedVoice(t *testing.T) {
	fixture := &transcriptFixture{}
	recorder := &fakeRecorder{}
	scopes := &fakeScopes{policies: map[string]scopedomain.Policy{keyOf(100, "group", 700): {Enabled: true, StoreMessages: true, ToolPermissions: map[string]bool{"media.voice.transcribe": true}, ReplyMode: scopedomain.ModeAlwaysReply}}}
	d := newDecider(t, Options{Scopes: scopes, Recorder: recorder, Media: fixture, Transcripts: fixture, Dynamic: func() RuntimeConfig { return RuntimeConfig{Enabled: false} }})
	task := groupTask()
	task.Segments = []obdomain.Segment{{Type: obdomain.SegRecord}}
	if err := d.ProcessTask(context.Background(), task); err != nil {
		t.Fatal(err)
	}
	if fixture.calls != 1 || fixture.saved == "" || len(recorder.decisions) != 0 {
		t.Fatal("disabled decision blocked authorized transcription")
	}
}

// TestTranscriptionRevocationDiscardsDerivedText verifies revocation while ASR is running prevents storage.
// TestTranscriptionRevocationDiscardsDerivedText 验证 ASR 进行中撤销权限会阻止转写落库。
func TestTranscriptionRevocationDiscardsDerivedText(t *testing.T) {
	for _, kind := range []string{"disabled", "storage", "observe"} {
		t.Run(kind, func(t *testing.T) {
			key := keyOf(100, "group", 700)
			scopes := &fakeScopes{policies: map[string]scopedomain.Policy{key: {Enabled: true, StoreMessages: true, ToolPermissions: map[string]bool{"media.voice.transcribe": true}, ReplyMode: scopedomain.ModeAlwaysReply}}}
			fixture := &transcriptFixture{}
			fixture.onTranscribe = func() {
				p := scopes.policies[key]
				switch kind {
				case "disabled":
					p.Enabled = false
				case "storage":
					p.StoreMessages = false
				case "observe":
					p.ReplyMode = scopedomain.ModeObserveOnly
				}
				scopes.policies[key] = p
			}
			recorder := &fakeRecorder{}
			d := newDecider(t, Options{Scopes: scopes, Recorder: recorder, Media: fixture, Transcripts: fixture})
			if err := d.ProcessTask(context.Background(), groupTask()); err != nil {
				t.Fatal(err)
			}
			if fixture.saved != "" || len(recorder.decisions) != 0 {
				t.Fatal("revoked transcription persisted or triggered a decision")
			}
		})
	}
}
