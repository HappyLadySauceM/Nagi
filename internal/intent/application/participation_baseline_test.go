// Offline regression samples for the participation decision baseline.
// 参与判断基线的离线回归样本。
package application

import (
	"context"
	"strconv"
	"testing"
	"time"

	obdomain "github.com/HappyLadySauceM/Nagi/internal/onebot/domain"
	scopedomain "github.com/HappyLadySauceM/Nagi/internal/scope/domain"

	"github.com/HappyLadySauceM/Nagi/internal/intent/domain"
)

const (
	baselineBotID   int64 = 1341939164
	baselineUserID  int64 = 1552089234
	baselineGroupID int64 = 827645392
)

var (
	baselineNow = time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)
	// baselineThresholds is the fixed comparison baseline for this round. It is
	// intentionally not derived from runtime defaults so the samples do not
	// drift when defaults change. Values: continuation=0.80,
	// interjection=0.95, addressed_other_max=0.60.
	// baselineThresholds 是本轮固定对比基线，刻意不跟随运行时默认值，
	// 避免默认值变化让基线漂移。本轮取值：continuation=0.80、
	// interjection=0.95、addressed_other_max=0.60。
	baselineThresholds = domain.Thresholds{Continuation: 0.80, Interjection: 0.95, AddressedOtherMax: 0.60}
)

// baselineGroupInput builds a deterministic group conversation fixture.
// baselineGroupInput 构造确定性的群聊会话夹具。
func baselineGroupInput(text string, segments []obdomain.Segment) domain.Input {
	return domain.Input{
		Task: domain.Task{
			JobID: 101, BotID: baselineBotID, Kind: "group", TargetID: baselineGroupID,
			ConversationID: "baseline-group",
			Message:        domain.Message{MessageID: 7001, SenderID: baselineUserID, Text: text},
			Segments:       segments,
		},
		Recent: []domain.Message{
			{MessageID: 6996, SenderID: baselineUserID, Text: "在吗", CreatedAt: baselineNow.Add(-40 * time.Second)},
			{MessageID: 6997, SenderID: baselineUserID, Text: "问个配置", CreatedAt: baselineNow.Add(-35 * time.Second)},
			{MessageID: 7000, SenderID: baselineBotID, IsBot: true, Text: "在，直接说。", CreatedAt: baselineNow.Add(-30 * time.Second)},
		},
		Policy: scopedomain.Policy{
			ScopeType: scopedomain.ScopeGroup, ScopeID: baselineGroupID,
			Enabled: true, ReplyMode: scopedomain.ModeIntentBased,
		},
		Now: baselineNow,
	}
}

// baselinePrivateInput builds the matching one-to-one fixture.
// baselinePrivateInput 构造对应的私聊夹具。
func baselinePrivateInput(text string) domain.Input {
	in := baselineGroupInput(text, nil)
	in.Task.Kind = "private"
	in.Task.TargetID = baselineUserID
	in.Task.ConversationID = "baseline-private"
	in.Policy.ScopeType = scopedomain.ScopePrivate
	in.Policy.ScopeID = baselineUserID
	return in
}

// baselineAtBot returns an at segment targeting the bot.
// baselineAtBot 返回指向机器人的 at 消息段。
func baselineAtBot() []obdomain.Segment {
	return []obdomain.Segment{{Type: obdomain.SegAt, Data: map[string]string{"qq": "1341939164"}}}
}

// baselineQuoteBot returns a reply segment referencing the bot message in the
// fixture.
// baselineQuoteBot 返回引用夹具中机器人消息的 reply 消息段。
func baselineQuoteBot() []obdomain.Segment {
	return []obdomain.Segment{{Type: obdomain.SegReply, Data: map[string]string{"id": "7000"}}}
}

// baselineConsecutiveShortMessages mimics several short user messages followed
// by a quick follow-up; the sample keeps the same conversation shape used by
// live history reads.
// baselineConsecutiveShortMessages 模拟连续多条用户短消息后紧接一条追问，
// 保持真实历史读取的会话形状。
func baselineConsecutiveShortMessages() domain.Input {
	in := baselineGroupInput("这个怎么弄", nil)
	in.Recent = []domain.Message{
		{MessageID: 6990, SenderID: baselineUserID, Text: "在吗", CreatedAt: baselineNow.Add(-60 * time.Second)},
		{MessageID: 6991, SenderID: baselineUserID, Text: "有空吗", CreatedAt: baselineNow.Add(-50 * time.Second)},
		{MessageID: 6992, SenderID: baselineUserID, Text: "问一下", CreatedAt: baselineNow.Add(-40 * time.Second)},
		{MessageID: 7000, SenderID: baselineBotID, IsBot: true, Text: "在的，你说。", CreatedAt: baselineNow.Add(-30 * time.Second)},
	}
	return in
}

type participationBaselineCase struct {
	name             string
	input            domain.Input
	semantic         domain.SemanticAnswer
	wantDecision     domain.Decision
	wantReason       string
	wantExplicit     bool
	wantNeedSemantic bool
}

// TestParticipationDecisionBaseline pins the current participation behavior for
// explicit addressing, quoting, natural follow-ups, addressing others, closing,
// consecutive short messages, semantic unavailability and conflicting scores.
// TestParticipationDecisionBaseline 固定当前参与判断行为：明确 @、引用、自然追问、
// 对别人说话、结束语、连续短消息、语义不可用与分数冲突。
func TestParticipationDecisionBaseline(t *testing.T) {
	t.Logf("fixed baseline thresholds: continuation=%.2f interjection=%.2f addressed_other_max=%.2f",
		baselineThresholds.Continuation, baselineThresholds.Interjection, baselineThresholds.AddressedOtherMax)
	cases := []participationBaselineCase{
		{
			name: "explicit_at", input: baselineGroupInput("小雪，这个怎么配？", baselineAtBot()),
			wantDecision: domain.DecisionReply, wantReason: "explicit_mention", wantExplicit: true,
		},
		{
			name: "reply_quote", input: baselineGroupInput("你刚才说的那个呢？", baselineQuoteBot()),
			semantic:     domain.SemanticAnswer{Available: true, CallAddressing: domain.CallYes},
			wantDecision: domain.DecisionReply, wantReason: "semantic_direct_call", wantExplicit: true, wantNeedSemantic: true,
		},
		{
			name: "natural_follow_up", input: baselineGroupInput("那你觉得哪个更稳？", nil),
			semantic:     domain.SemanticAnswer{Available: true, ContinuesBotThread: 0.90, AddressedToOther: 0.10, IntentCategory: "chat"},
			wantDecision: domain.DecisionReply, wantReason: "active_thread_continuation", wantNeedSemantic: true,
		},
		{
			name: "addressed_to_other", input: baselineGroupInput("小王你周末去哪？", nil),
			semantic:     domain.SemanticAnswer{Available: true, ContinuesBotThread: 0.10, AppropriateToJoin: 0.10, AddressedToOther: 0.90, IntentCategory: "chat"},
			wantDecision: domain.DecisionSilent, wantReason: "addressed_to_other", wantNeedSemantic: true,
		},
		{
			name: "closing", input: baselineGroupInput("好，那先这样。", nil),
			semantic:     domain.SemanticAnswer{Available: true, ContinuesBotThread: 0.90, AddressedToOther: 0.10, Scores: map[string]float64{"intent": 0.9}},
			wantDecision: domain.DecisionSilent, wantReason: "closing", wantNeedSemantic: true,
		},
		{
			name: "consecutive_short_messages", input: baselineConsecutiveShortMessages(),
			semantic:     domain.SemanticAnswer{Available: true, ContinuesBotThread: 0.85, AddressedToOther: 0.20, IntentCategory: "chat"},
			wantDecision: domain.DecisionReply, wantReason: "active_thread_continuation", wantNeedSemantic: true,
		},
		{
			name: "semantic_unavailable_group", input: baselineGroupInput("这个呢？", nil),
			semantic:     domain.SemanticAnswer{Available: false, FailureCode: "semantic_unavailable"},
			wantDecision: domain.DecisionSilent, wantReason: "jev_unavailable", wantNeedSemantic: true,
		},
		{
			name: "semantic_unavailable_private", input: baselinePrivateInput("这个呢？"),
			semantic:     domain.SemanticAnswer{Available: false, FailureCode: "semantic_unavailable"},
			wantDecision: domain.DecisionReply, wantReason: "private_semantic_unavailable", wantNeedSemantic: true,
		},
		{
			name: "private_greeting", input: baselinePrivateInput("在吗，早上好"),
			semantic:     domain.SemanticAnswer{Available: true, DirectedToBot: 0.21, ContinuesBotThread: 0.10, AddressedToOther: 0.10, AppropriateToJoin: 0.10, IntentCategory: "chat"},
			wantDecision: domain.DecisionReply, wantReason: "private_normal", wantNeedSemantic: true,
		},
		{
			name: "private_question", input: baselinePrivateInput("这个配置怎么改？"),
			semantic:     domain.SemanticAnswer{Available: true, DirectedToBot: 0.20, ContinuesBotThread: 0.10, AddressedToOther: 0.10, AppropriateToJoin: 0.10, IntentCategory: "question"},
			wantDecision: domain.DecisionReply, wantReason: "private_normal", wantNeedSemantic: true,
		},
		{
			name: "private_chat", input: baselinePrivateInput("今天天气不错"),
			semantic:     domain.SemanticAnswer{Available: true, DirectedToBot: 0.10, ContinuesBotThread: 0.10, AddressedToOther: 0.10, AppropriateToJoin: 0.10, IntentCategory: "chat"},
			wantDecision: domain.DecisionReply, wantReason: "private_normal", wantNeedSemantic: true,
		},
		{
			name: "private_closing", input: baselinePrivateInput("好，那先这样"),
			semantic:     domain.SemanticAnswer{Available: true, DirectedToBot: 0.10, ContinuesBotThread: 0.10, AddressedToOther: 0.10, AppropriateToJoin: 0.10, Scores: map[string]float64{"intent": 0.9}},
			wantDecision: domain.DecisionSilent, wantReason: "closing", wantNeedSemantic: true,
		},
		{
			// Being addressed to another person must suppress interjection.
			// 对别人说话时必须优先抑制插话。
			name: "conflict_interjection_addressed_to_other", input: baselineGroupInput("你们俩聊什么呢？", nil),
			semantic:     domain.SemanticAnswer{Available: true, AppropriateToJoin: 0.97, AddressedToOther: 0.90, IntentCategory: "chat"},
			wantDecision: domain.DecisionSilent, wantReason: "addressed_to_other", wantNeedSemantic: true,
		},
		{
			name: "conflict_continuation_addressed_to_other", input: baselineGroupInput("你们俩聊什么呢？", nil),
			semantic:     domain.SemanticAnswer{Available: true, ContinuesBotThread: 0.90, AddressedToOther: 0.90, IntentCategory: "chat"},
			wantDecision: domain.DecisionSilent, wantReason: "addressed_to_other", wantNeedSemantic: true,
		},
		{
			name: "conflict_directed_to_bot_addressed_to_other", input: baselineGroupInput("你们俩聊什么呢？", nil),
			semantic:     domain.SemanticAnswer{Available: true, DirectedToBot: 0.90, AddressedToOther: 0.90, IntentCategory: "chat"},
			wantDecision: domain.DecisionSilent, wantReason: "addressed_to_other", wantNeedSemantic: true,
		},
		{
			name: "interjection_clean", input: baselineGroupInput("你们在说部署吗？", nil),
			semantic:     domain.SemanticAnswer{Available: true, AppropriateToJoin: 0.96, AddressedToOther: 0.30, IntentCategory: "chat"},
			wantDecision: domain.DecisionReply, wantReason: "interjection", wantNeedSemantic: true,
		},
		{
			name: "multi_intent_mixed", input: baselineGroupInput("帮我查下明天天气，再提醒我九点开会", nil),
			semantic:     domain.SemanticAnswer{Available: true, DirectedToBot: 0.90, ContinuesBotThread: 0.20, AddressedToOther: 0.10, AppropriateToJoin: 0.10, IntentCategory: "chat"},
			wantDecision: domain.DecisionReply, wantReason: "directed_to_bot", wantNeedSemantic: true,
		},
		{
			name: "negation_question", input: baselineGroupInput("你不是说过不用改配置吗？", nil),
			semantic:     domain.SemanticAnswer{Available: true, DirectedToBot: 0.90, ContinuesBotThread: 0.20, AddressedToOther: 0.10, AppropriateToJoin: 0.10, IntentCategory: "question"},
			wantDecision: domain.DecisionReply, wantReason: "directed_to_bot", wantNeedSemantic: true,
		},
		{
			name: "rhetorical_question", input: baselineGroupInput("这还用问吗？", nil),
			semantic:     domain.SemanticAnswer{Available: true, DirectedToBot: 0.20, ContinuesBotThread: 0.20, AddressedToOther: 0.20, AppropriateToJoin: 0.40, IntentCategory: "question"},
			wantDecision: domain.DecisionSilent, wantReason: "ambiguous", wantNeedSemantic: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rule := EvaluateRules(tc.input)
			if rule.Blocked {
				t.Fatalf("rules blocked the baseline sample: %+v", rule)
			}
			if rule.Explicit != tc.wantExplicit || rule.NeedSemantic != tc.wantNeedSemantic {
				t.Fatalf("rules = %+v, want explicit=%v need_semantic=%v", rule, tc.wantExplicit, tc.wantNeedSemantic)
			}
			decision := EvaluateReplyPolicy(tc.input, rule, tc.semantic)
			if decision.Decision != tc.wantDecision || decision.ReasonCode != tc.wantReason {
				t.Fatalf("decision = %+v, want %s/%s", decision, tc.wantDecision, tc.wantReason)
			}
		})
	}
}

// TestParticipationBaselineRecordsRuleVersionAndFailure proves the recorded
// decision keeps the ruleset version, rule evaluations, semantic failure code
// and threshold snapshot used by the existing audit path.
// TestParticipationBaselineRecordsRuleVersionAndFailure 证明既有审计路径保留了
// 规则集版本、规则求值、语义失败原因码与阈值快照。
func TestParticipationBaselineRecordsRuleVersionAndFailure(t *testing.T) {
	in := baselineGroupInput("看看这个配置", nil)
	specific := &domain.Ruleset{ID: 41, Version: 7, Entries: []domain.RuleEntry{{
		Priority: 0,
		Match:    domain.RuleMatch{TextContains: "看看"},
		Action:   domain.RuleAction{Type: domain.ActionRequireSemantic, ReasonCode: "review_semantic"},
	}}}
	global := &domain.Ruleset{ID: 42, Version: 3, BuiltinRules: map[string]bool{"closing": false}}
	rule, version := EvaluateRuleChain(in, specific, global)
	if version != "scope:41@7" {
		t.Fatalf("ruleset version = %q, want scope:41@7", version)
	}
	semantic := domain.SemanticAnswer{Available: false, FailureCode: "semantic_unavailable"}
	decision := EvaluateReplyPolicy(in, rule, semantic)
	decision.RulesetVersion = version
	steps := decisionSteps(in, rule, semantic, decision, 12, true, false)
	if len(steps) != 5 {
		t.Fatalf("steps = %d, want policy/context/rules/semantic/final", len(steps))
	}
	if steps[0].Phase != domain.StepPolicy || steps[1].Phase != domain.StepContext || steps[2].Phase != domain.StepRules ||
		steps[3].Phase != domain.StepSemantic || steps[4].Phase != domain.StepFinal {
		t.Fatalf("phases = %+v", steps)
	}
	questions, ok := steps[2].Detail["Questions"].([]map[string]any)
	if !ok || len(questions) != 0 {
		t.Fatalf("specific questions = %+v", steps[2].Detail["Questions"])
	}
	if steps[3].Detail["Available"] != false || steps[3].Detail["FailureCode"] != "semantic_unavailable" {
		t.Fatalf("semantic step = %+v", steps[3].Detail)
	}
	if _, exists := steps[4].Detail["Thresholds"]; exists {
		t.Fatalf("global thresholds still recorded: %+v", steps[4].Detail["Thresholds"])
	}
}

// TestParticipationBaselineSimulationMatchesPolicy verifies the existing
// simulate path reuses the live policy calculation instead of a separate one.
// TestParticipationBaselineSimulationMatchesPolicy 验证既有模拟路径复用实时策略
// 计算，而不是另建一套。
func TestParticipationBaselineSimulationMatchesPolicy(t *testing.T) {
	in := baselineGroupInput("那你觉得哪个更稳？", nil)
	in.BotName = "FixtureBot"
	rule := EvaluateRules(in)
	semantic := &fakeSemantic{answer: domain.SemanticAnswer{Available: true, CallAddressing: domain.CallYes, ContinuesBotThread: 0.90, AddressedToOther: 0.10, IntentCategory: "chat"}}
	recorder := &fakeRecorder{}
	executor := &fakeExecutor{}
	d := newDecider(t, Options{
		Scopes: &fakeScopes{}, Semantic: semantic, Recorder: recorder, Executor: executor,
		Thresholds: baselineThresholds, Provider: "jev", Model: "jev-latest",
	})
	final, steps := d.EvaluateSimulation(context.Background(), in, rule, true)
	if final.Decision != domain.DecisionReply || final.ReasonCode != "semantic_direct_call" {
		t.Fatalf("simulation decision = %+v", final)
	}
	if len(steps) != 5 || len(recorder.decisions) != 0 || len(executor.tasks) != 0 {
		t.Fatalf("simulation steps = %d, recorded = %d, executed = %d", len(steps), len(recorder.decisions), len(executor.tasks))
	}
}

func TestNaturalCallsRequireSemanticEvidence(t *testing.T) {
	for _, text := range []string{
		"Nagi在吗？", "Nagi，你觉得哪个方案更好？", "今天吃什么，Nagi？", "Nagi告诉我昨天停电了",
	} {
		in := baselineGroupInput(text, []obdomain.Segment{obdomain.Text(text)})
		in.BotName = "Nagi"
		rule := EvaluateRules(in)
		if decision := EvaluateReplyPolicy(in, rule, domain.SemanticAnswer{}); !rule.NeedSemantic || decision.ReasonCode != "jev_unavailable" {
			t.Errorf("%q: rule=%+v decision=%+v", text, rule, decision)
		}
	}
}

func TestNameMentionWithImage(t *testing.T) {
	task := domain.Task{Segments: []obdomain.Segment{obdomain.Text("Nagi"), obdomain.Image("sample"), obdomain.Text("OK")}}
	if !mentionsBotName(task, "Nagi") {
		t.Fatal("image hid the name")
	}
	task.Segments = []obdomain.Segment{obdomain.Text("Na"), obdomain.Image("sample"), obdomain.Text("gi")}
	if mentionsBotName(task, "Nagi") {
		t.Fatal("image joined parts of a name")
	}
	task.Segments = []obdomain.Segment{obdomain.Text("Nagi"), obdomain.Image("sample"), obdomain.Text("Bot")}
	if mentionsBotName(task, "Nagi Bot") {
		t.Fatal("image joined parts of a name containing a space")
	}

}

func TestNameMentionDoesNotRequireReply(t *testing.T) {
	for _, tc := range []struct {
		text string
		want bool
	}{
		{"你昨天部署的nagi怎么样？", true},
		{"大家好", false},
		{"我昨天部署了 Nagios", false},
	} {
		in := baselineGroupInput(tc.text, nil)
		in.BotName = "Nagi"
		rule := EvaluateRules(in)
		semantic := domain.SemanticAnswer{Available: true, AddressedToOther: .99}
		decision := EvaluateReplyPolicy(in, rule, semantic)
		steps := decisionSteps(in, rule, semantic, decision, 1, true, false)
		if decision.Decision != domain.DecisionSilent || steps[2].Detail["BotNameMentioned"] != tc.want {
			t.Fatalf("%q: decision=%+v detail=%+v", tc.text, decision, steps[2].Detail)
		}
	}
}

func TestGroupWaitsForTheNamedSendersImage(t *testing.T) {
	request := domain.Message{MessageID: 11, SenderID: 101, Text: "Nagi你能看看我下面发的图片嘛？"}
	input := domain.Input{
		BotName:  "Nagi",
		Policy:   scopedomain.Policy{Enabled: true, AllowHistoryRead: true, ReplyMode: scopedomain.ModeIntentBased},
		Activity: domain.BotActivity{Known: true},
		Task:     domain.Task{Kind: "group", BotID: 900, Message: request},
	}
	decision := EvaluateReplyPolicy(input, EvaluateRules(input), domain.SemanticAnswer{Available: true, CallAddressing: domain.CallYes})
	if decision.Decision != domain.DecisionDefer || decision.ReasonCode != "awaiting_content" {
		t.Fatalf("request decision = %+v", decision)
	}

	unrelated := domain.Message{MessageID: 12, SenderID: 202, Segments: []obdomain.Segment{obdomain.Image("unrelated")}}
	input.Recent = []domain.Message{request, unrelated}
	input.Task.Message = domain.Message{MessageID: 13, SenderID: request.SenderID}
	input.Task.Segments = []obdomain.Segment{obdomain.Image("requested")}
	input.AwaitedRequest = &request
	decision = EvaluateReplyPolicy(input, EvaluateRules(input), domain.SemanticAnswer{Available: true, CallAddressing: domain.CallUncertain})
	if decision.Decision != domain.DecisionReply || decision.ReasonCode != "awaited_content_received" {
		t.Fatalf("image decision = %+v", decision)
	}
	if history := replyHistory(input, decision.ReasonCode); len(history) != 1 || history[0].MessageID != request.MessageID {
		t.Fatalf("generation context = %+v", history)
	}

	input.Task.Message = unrelated
	decision = EvaluateReplyPolicy(input, EvaluateRules(input), domain.SemanticAnswer{})
	if decision.Decision != domain.DecisionSilent {
		t.Fatalf("other sender decision = %+v", decision)
	}
}

func TestAwaitedContentUsesCorrectReferenceAndContentType(t *testing.T) {
	request := domain.Message{MessageID: 11, SenderID: 101, Text: "Nagi你能看看我下面发的图片嘛？"}
	input := domain.Input{
		Policy:         scopedomain.Policy{AllowHistoryRead: true},
		AwaitedRequest: &request,
		Task:           domain.Task{BotID: 900, Message: domain.Message{SenderID: request.SenderID}},
	}
	input.Task.Segments = []obdomain.Segment{obdomain.Reply(11), obdomain.Image("requested")}
	if !awaitingReply(input) {
		t.Fatal("image quoting the request did not match")
	}
	input.Task.Segments[0] = obdomain.Reply(10)
	if awaitingReply(input) {
		t.Fatal("image quoting a different message matched")
	}
	request.Text = "Nagi你能看看我下面发的文字嘛？"
	input.Task.Segments = []obdomain.Segment{obdomain.Text("文字：这是需要你查看的完整说明。")}
	if !awaitingReply(input) {
		t.Fatal("text did not match the pre-announced text request")
	}
	input.Task.Segments = []obdomain.Segment{obdomain.Reply(11), obdomain.Text("稍等一下")}
	if awaitingReply(input) {
		t.Fatal("a quote without the awaited material consumed the wait")
	}
	input.Task.Segments[1] = obdomain.Text("文字：选 B")
	if !awaitingReply(input) {
		t.Fatal("labeled text quoting the request did not match")
	}
}

func TestReplyHistoryKeepsEarlierDiscussionTextWithoutAttachments(t *testing.T) {
	input := domain.Input{
		Recent: []domain.Message{{MessageID: 11, Segments: []obdomain.Segment{obdomain.Image("unrelated"), obdomain.Text("选 B，因为它能直接转换。")}}},
		Task: domain.Task{Kind: "group", BotID: 900, Message: domain.Message{MessageID: 12}, Segments: []obdomain.Segment{
			obdomain.At(900), obdomain.Text("刚才为什么选 B？"),
		}},
	}
	for i := int64(20); i < 25; i++ {
		input.Recent = append(input.Recent, domain.Message{MessageID: i, Segments: []obdomain.Segment{obdomain.Image("unrelated")}})
	}
	history := replyHistory(input, "explicit_mention")
	if len(history) != 1 || len(history[0].Segments) != 0 || history[0].Text != "选 B，因为它能直接转换。" {
		t.Fatalf("discussion context = %+v", history)
	}
}

func TestGroupTopicParticipation(t *testing.T) {
	for _, tc := range []struct {
		name, action string
		scores       []float64
		quoted       bool
		replies      int
		want         domain.Decision
		wantTopic    int64
		skipCooldown bool
	}{
		{"another member follows up during cooldown", domain.TopicFollowUp, []float64{.95, .12}, false, 1, domain.DecisionReply, 10, true},
		{"ambiguous between parallel topics", domain.TopicFollowUp, []float64{.96, .90}, false, 1, domain.DecisionSilent, 0, false},
		{"exact quote overrides affinity", "other", []float64{.10, .98}, true, 1, domain.DecisionReply, 10, true},
		{"leave keeps topic open", domain.TopicLeave, []float64{.95, .12}, false, 1, domain.DecisionSilent, 10, false},
		{"close affects a single topic", domain.TopicClose, []float64{.09, .92}, false, 1, domain.DecisionSilent, 20, false},
		{"hourly limit still applies to a quote", domain.TopicFollowUp, []float64{.95, .12}, true, 5, domain.DecisionSilent, 0, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			in := baselineGroupInput("那为什么？", nil)
			in.Task.JobID = 901
			in.Task.Message.SenderID = 77
			in.Policy.AllowHistoryRead = true
			in.Policy.MinReplyIntervalMS = 10000
			in.Policy.MaxRepliesPerHour = 5
			in.Activity = domain.BotActivity{Known: true, LastReplyAt: in.Now.Add(-2 * time.Second), RepliesLastHour: tc.replies}
			in.Topics = &domain.TopicContext{IdleSeconds: 300, Candidates: []domain.TopicCandidate{
				{ID: 10, Active: true, ExpiresAt: in.Now.Add(30 * time.Second), History: []domain.Message{{Text: "数据库超时", SenderID: 31}}},
				{ID: 20, Active: true, ExpiresAt: in.Now.Add(60 * time.Second), History: []domain.Message{{Text: "晚饭安排", SenderID: 22}}},
			}}
			if tc.quoted {
				in.Topics.QuotedID, in.Topics.QuotedBot = 10, true
			}
			semantic := domain.SemanticAnswer{Available: true, TopicJudgments: []domain.TopicJudgment{
				{Affinity: tc.scores[0], Action: tc.action}, {Affinity: tc.scores[1], Action: tc.action},
			}}
			gate, _ := topicRuleInput(in)
			got := evaluateTopicDecision(in, EvaluateRules(gate), semantic)
			if got.Decision != tc.want || (got.Topic == nil) != (tc.wantTopic == 0) {
				t.Fatalf("unexpected decision: %+v", got)
			}
			if got.Topic != nil {
				if got.Topic.ID != tc.wantTopic || got.Topic.CooldownBypassed != tc.skipCooldown {
					t.Fatalf("unexpected change: %+v", got.Topic)
				}
				if tc.action == domain.TopicLeave && !got.Topic.ExpiresAt.Equal(in.Topics.Candidates[0].ExpiresAt) {
					t.Fatal("personal exit refreshed the topic")
				}
				if got.Decision == domain.DecisionReply {
					history := topicReplyHistory(in, got)
					if len(history) != 1 || history[0].Text != "数据库超时" {
						t.Fatalf("mixed parallel histories: %+v", history)
					}
				}
			}
		})
	}
}

func TestCooldownResolutionCapturesActualTopicBranches(t *testing.T) {
	for _, tc := range []struct {
		name, action, branch string
		quoted               bool
		want                 domain.Decision
	}{
		{"continuation", domain.TopicFollowUp, "bypassed", true, domain.DecisionReply},
		{"unrelated", "other", "restored", false, domain.DecisionSilent},
		{"closed", domain.TopicClose, "not_revisited", true, domain.DecisionSilent},
	} {
		t.Run(tc.name, func(t *testing.T) {
			in := baselineGroupInput("这条呢？", nil)
			in.Task.JobID = 17
			in.Policy.AllowHistoryRead = true
			in.Policy.MinReplyIntervalMS = 6000
			in.Policy.MaxRepliesPerHour = 0
			in.Activity = domain.BotActivity{Known: true, LastReplyAt: in.Now.Add(-time.Second)}
			in.Topics = &domain.TopicContext{IdleSeconds: 300, Candidates: []domain.TopicCandidate{{ID: 9, Active: true}}}
			if tc.quoted {
				in.Topics.QuotedID, in.Topics.QuotedBot = 9, true
			} else {
				in.Task.Segments = []obdomain.Segment{domainSegmentForTest(strconv.FormatInt(in.Task.BotID, 10))}
			}
			answer := domain.SemanticAnswer{Available: true, TopicJudgments: []domain.TopicJudgment{{Action: tc.action}}}
			reason, first := domain.EvaluateCooldownGate(in, true)
			decision := evaluateTopicDecision(in, evaluateRules(in, reason), answer, true)
			events := cooldownEvents(first, decision)
			if decision.Decision != tc.want || len(events) != 2 || events[0].Branch != "deferred" ||
				events[1].Sequence != 2 || events[1].Branch != tc.branch || events[1].ElapsedMS == nil || *events[1].ElapsedMS != 1000 {
				t.Fatalf("decision=%+v events=%+v", decision, events)
			}
		})
	}
}

func TestCallAddressingControlsParticipationAndWaiting(t *testing.T) {
	const requestID = 51
	for _, tc := range []struct {
		name     string
		call     domain.CallAddressing
		text     string
		segments []obdomain.Segment
		withWait bool
		end      bool
		want     string
	}{
		{"unlisted call", domain.CallYes, "薄荷，劳驾指出哪里还没说明白", nil, false, false, "semantic_direct_call"},
		{"reported speech", domain.CallNo, "小陈说薄荷答得好", nil, false, false, "addressed_to_other"},
		{"uncertain high old scores", domain.CallUncertain, "这话怎么说", nil, false, false, "call_addressing_uncertain"},
		{"new wait", domain.CallYes, "薄荷，我下面发个截图", nil, false, false, "awaiting_content"},
		{"missing picture now", domain.CallYes, "薄荷，你认得这张图吗", nil, false, false, "semantic_direct_call"},
		{"supplement", domain.CallUncertain, "", []obdomain.Segment{obdomain.Image("fixture")}, true, false, "awaited_content_received"},
		{"explicitly for someone else", domain.CallUncertain, "", []obdomain.Segment{obdomain.At(42), obdomain.Image("fixture")}, true, false, "addressed_to_other"},
		{"natural no preserves wait", domain.CallNo, "小陈看图", []obdomain.Segment{obdomain.Image("fixture")}, true, false, "addressed_to_other"},
		{"topic close beats call", domain.CallYes, "薄荷，结束这个话题", baselineQuoteBot(), false, true, "topic_close"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			in := baselineGroupInput(tc.text, tc.segments)
			in.BotName, in.Policy.AllowHistoryRead = "薄荷", true
			in.Task.Message.CreatedAt = baselineNow
			if tc.withWait {
				in.AwaitedRequest = &domain.Message{MessageID: requestID, SenderID: in.Task.Message.SenderID, Text: "我下面发截图", CreatedAt: baselineNow.Add(-time.Second)}
			}
			answer := domain.SemanticAnswer{Available: true, CallAddressing: tc.call, DirectedToBot: .12, AppropriateToJoin: .99}
			if tc.end {
				in.Topics = &domain.TopicContext{QuotedID: 2, QuotedBot: true, Candidates: []domain.TopicCandidate{{ID: 2, Active: true}}}
				answer.TopicJudgments = []domain.TopicJudgment{{Affinity: .98, Action: domain.TopicClose}}
			}
			got := evaluateTopicDecision(in, EvaluateRules(in), answer)
			if got.ReasonCode != tc.want || tc.end && (got.Topic == nil || got.Topic.Action != domain.TopicClose) {
				t.Fatalf("decision = %+v, want %q", got, tc.want)
			}
		})
	}
}

func TestTextWaitRequiresEvidenceAndAnnotatedImageCanClose(t *testing.T) {
	in := baselineGroupInput("今天办公室里好冷", nil)
	in.BotName, in.Policy.AllowHistoryRead = "薄荷", true
	in.Task.Message.CreatedAt = baselineNow
	in.AwaitedRequest = &domain.Message{MessageID: 123, SenderID: in.Task.Message.SenderID, Text: "我稍后发日志", CreatedAt: baselineNow.Add(-time.Second)}
	semantic := domain.SemanticAnswer{Available: true, CallAddressing: domain.CallUncertain}
	if got := evaluateTopicDecision(in, EvaluateRules(in), semantic); got.ReasonCode != "call_addressing_uncertain" {
		t.Fatalf("chatter consumed wait: %+v", got)
	}
	in.Task.Message.Text = "日志：INFO connection closed"
	if got := evaluateTopicDecision(in, EvaluateRules(in), semantic); got.ReasonCode != "awaited_content_received" {
		t.Fatalf("labeled log missed wait: %+v", got)
	}
	in.AwaitedRequest.Text = "我稍后发截图"
	in.Task.Segments = append(baselineAtBot(), obdomain.Image("fixture"), obdomain.Text("不用分析了，结束这个话题"))
	in.Topics = &domain.TopicContext{QuotedID: 2, Candidates: []domain.TopicCandidate{{ID: 2, Active: true, Waiting: true}}}
	rule := EvaluateRules(in)
	if !needsTopicSemantic(in, rule) {
		t.Fatal("annotated supplement skipped topic classification")
	}
	semantic.TopicJudgments = []domain.TopicJudgment{{Affinity: .96, Action: domain.TopicClose}}
	if got := evaluateTopicDecision(in, rule, semantic); got.Decision != domain.DecisionSilent || got.Topic == nil || got.Topic.Action != domain.TopicClose {
		t.Fatalf("closing image renewed topic: %+v", got)
	}
}

func TestQuotedWaitExitDoesNotRequireMaterial(t *testing.T) {
	for _, tc := range []struct {
		name, action string
		quoteSender  int64
		call         domain.CallAddressing
		wantExit     bool
	}{
		{"close", domain.TopicClose, 77, domain.CallUncertain, true},
		{"personal exit", domain.TopicLeave, 77, domain.CallUncertain, true},
		{"other sender", domain.TopicClose, 78, domain.CallUncertain, false},
		{"opposite addressing", domain.TopicClose, 77, domain.CallNo, false},
		{"ordinary supplement", domain.TopicContribution, 77, domain.CallUncertain, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			text := "不用看了，结束这个话题"
			switch tc.action {
			case domain.TopicContribution:
				text = "稍等一下"
			case domain.TopicLeave:
				text = "我自己退出这个话题"
			}
			in := baselineGroupInput(text, []obdomain.Segment{obdomain.Reply(51), obdomain.Text(text)})
			in.BotName, in.Policy.AllowHistoryRead = "薄荷", true
			in.Task.Message.SenderID, in.Task.Message.CreatedAt = 77, baselineNow
			in.AwaitedRequest = &domain.Message{MessageID: 52, SenderID: 77, Text: "我稍后发日志", CreatedAt: baselineNow.Add(-time.Second)}
			in.Topics = &domain.TopicContext{
				QuotedID:   2,
				Reference:  &domain.Message{MessageID: 51, SenderID: tc.quoteSender, Text: "我稍后发日志", CreatedAt: baselineNow.Add(-2 * time.Second)},
				Candidates: []domain.TopicCandidate{{ID: 2, Active: true, Waiting: true}, {ID: 3, Active: true}},
			}
			sem := domain.SemanticAnswer{Available: true, CallAddressing: tc.call, TopicJudgments: []domain.TopicJudgment{
				{AffinityMissing: true, Action: tc.action}, {Affinity: .99, Action: domain.TopicClose},
			}}
			rule := EvaluateRules(in)
			if awaitingReply(in) || !needsTopicSemantic(in, rule) {
				t.Fatal("quote without material should request a topic judgment without resuming the wait")
			}
			got := evaluateTopicDecision(in, rule, sem)
			if got.Decision != domain.DecisionSilent || (got.Topic != nil) != tc.wantExit ||
				got.Topic != nil && (got.Topic.ID != 2 || got.Topic.Action != tc.action || got.Topic.Source != "quote" || got.Topic.Waiting) {
				t.Fatalf("decision = %+v, topic = %+v", got, got.Topic)
			}
		})
	}
}

func TestUnquotedTopicExitNeedsAllCompetingScores(t *testing.T) {
	for _, tc := range []struct {
		name       string
		competitor []domain.TopicJudgment
		wantClose  bool
	}{
		{"missing score", []domain.TopicJudgment{{AffinityMissing: true, Action: "other"}}, false},
		{"missing judgment", nil, false},
		{"explicit zero", []domain.TopicJudgment{{Affinity: 0, Action: "other"}}, true},
		{"complete scores", []domain.TopicJudgment{{Affinity: .65, Action: "other"}}, true},
		{"close competition", []domain.TopicJudgment{{Affinity: .92, Action: "other"}}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			in := baselineGroupInput("不用看了，结束这个话题", nil)
			in.BotName, in.Policy.AllowHistoryRead = "薄荷", true
			in.Topics = &domain.TopicContext{Candidates: []domain.TopicCandidate{{ID: 1, Active: true}, {ID: 2, Active: true}}}
			sem := domain.SemanticAnswer{Available: true, CallAddressing: domain.CallUncertain, TopicJudgments: append(
				[]domain.TopicJudgment{{Affinity: .97, Action: domain.TopicClose}}, tc.competitor...),
			}
			got := evaluateTopicDecision(in, EvaluateRules(in), sem)
			if got.Decision != domain.DecisionSilent || (got.Topic != nil) != tc.wantClose ||
				got.Topic != nil && (got.Topic.ID != 1 || got.Topic.Action != domain.TopicClose) {
				t.Fatalf("decision = %+v, topic = %+v", got, got.Topic)
			}
		})
	}
}
