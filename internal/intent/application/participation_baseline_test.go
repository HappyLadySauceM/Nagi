// Offline regression samples for the participation decision baseline.
// 参与判断基线的离线回归样本。
package application

import (
	"context"
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
			wantDecision: domain.DecisionReply, wantReason: "explicit_mention", wantExplicit: true,
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
	rule := EvaluateRules(in)
	semantic := &fakeSemantic{answer: domain.SemanticAnswer{Available: true, ContinuesBotThread: 0.90, AddressedToOther: 0.10, IntentCategory: "chat"}}
	recorder := &fakeRecorder{}
	executor := &fakeExecutor{}
	d := newDecider(t, Options{
		Scopes: &fakeScopes{}, Semantic: semantic, Recorder: recorder, Executor: executor,
		Thresholds: baselineThresholds, Provider: "jev", Model: "jev-latest",
	})
	final, steps := d.EvaluateSimulation(context.Background(), in, rule, true)
	if final.Decision != domain.DecisionReply || final.ReasonCode != "active_thread_continuation" {
		t.Fatalf("simulation decision = %+v", final)
	}
	if len(steps) != 5 || len(recorder.decisions) != 0 || len(executor.tasks) != 0 {
		t.Fatalf("simulation steps = %d, recorded = %d, executed = %d", len(steps), len(recorder.decisions), len(executor.tasks))
	}
}

func TestClusterGroupCalls(t *testing.T) {
	base := baselineGroupInput("", nil)
	base.BotName = "Nagi"
	base.Policy.AllowHistoryRead = true
	base.Recent = nil
	base.Activity.Known = true

	first := domain.Message{MessageID: 21, SenderID: 1, Text: "Nagi在吗？", CreatedAt: baselineNow}
	second := domain.Message{MessageID: 22, SenderID: 1, Text: "有人嘛", CreatedAt: baselineNow.Add(20 * time.Second)}
	third := domain.Message{MessageID: 23, SenderID: 1, Text: "大家好", CreatedAt: baselineNow.Add(29 * time.Second)}
	answer := func(directed, continuation, other, join float64) domain.SemanticAnswer {
		return domain.SemanticAnswer{Available: true, DirectedToBot: directed, ContinuesBotThread: continuation, AddressedToOther: other, AppropriateToJoin: join, Scores: map[string]float64{"intent": 0.02}}
	}
	for _, tc := range []struct {
		name     string
		msg      domain.Message
		history  []domain.Message
		semantic domain.SemanticAnswer
		want     string
	}{
		{"cluster 21", first, []domain.Message{first, second}, answer(.07, .34, .24, .15), "direct_name_call"},
		{"cluster 22", second, []domain.Message{first, second, third}, answer(.04, .28, .27, .18), "unanswered_call_followup"},
		{"cluster 23", third, []domain.Message{first, second, third}, answer(.02, .11, .19, .21), "ambiguous"},
		{"cluster 30", domain.Message{Text: "Nagi 是谁？"}, nil, answer(.41, .34, .23, .39), "direct_name_call"},
		{"cluster 34", domain.Message{Text: "谁是Nagi？"}, nil, answer(.32, .26, .20, .34), "direct_name_call"},
		{"lowercase suffix identity", domain.Message{Text: "谁是 nagi"}, nil, domain.SemanticAnswer{}, "direct_name_call"},
		{"cluster dinner trailing call", domain.Message{Text: "我饿了 今天吃什么啊 Nagi"}, nil, domain.SemanticAnswer{}, "direct_name_call"},
		{"lowercase trailing call", domain.Message{Text: "今天吃什么，nagi？"}, nil, domain.SemanticAnswer{}, "direct_name_call"},
		{"trailing call after a preamble", domain.Message{Text: "我饿了，今天吃什么，nagi？"}, nil, domain.SemanticAnswer{}, "direct_name_call"},
		{"trailing call after an imperative", domain.Message{Text: "帮我看一下这个报错，Nagi"}, nil, domain.SemanticAnswer{}, "direct_name_call"},
		{"different addressee before a trailing call", domain.Message{Text: "小王，你要点什么啊，Nagi"}, nil, answer(.04, .28, .27, .18), "ambiguous"},
		{"trailing name as object", domain.Message{Text: "你为什么部署了 Nagi？"}, nil, answer(.04, .28, .27, .18), "ambiguous"},
		{"trailing name addressed to another member", domain.Message{Text: "小王，你为什么部署了 Nagi？"}, nil, answer(.04, .28, .27, .18), "ambiguous"},
		{"trailing name in statement", domain.Message{Text: "我说的是 Nagi"}, nil, answer(.04, .28, .27, .18), "ambiguous"},
		{"name fragment", domain.Message{Text: "今天吃什么啊 Naginata"}, nil, answer(.04, .28, .27, .18), "ambiguous"},
		{"identity question to the group", domain.Message{Text: "你们知道谁是 Nagi 吗？"}, nil, answer(.32, .26, .20, .34), "ambiguous"},
		{"cluster 33", domain.Message{Text: "nagi告诉我几点了"}, nil, answer(.71, .51, .18, .69), "direct_name_call"},
		{"mixed case identity", domain.Message{Text: "NaGi，你是谁？"}, nil, domain.SemanticAnswer{}, "direct_name_call"},
		{"bare lowercase name", domain.Message{Text: "nagi"}, nil, domain.SemanticAnswer{}, "direct_name_call"},
		{"polite lowercase request", domain.Message{Text: "nagi，请告诉我现在几点了？"}, nil, domain.SemanticAnswer{}, "direct_name_call"},
		{"named request", domain.Message{Text: "Nagi，你觉得哪个方案更好？"}, nil, domain.SemanticAnswer{}, "direct_name_call"},
		{"explanation request", domain.Message{Text: "Nagi，请解释一下这个报错"}, nil, domain.SemanticAnswer{}, "direct_name_call"},
		{"question particle without punctuation", domain.Message{Text: "Nagi，你能解释一下这个报错吗"}, nil, domain.SemanticAnswer{}, "direct_name_call"},
		{"question without punctuation", domain.Message{Text: "Nagi，你怎么看这个报错"}, nil, domain.SemanticAnswer{}, "direct_name_call"},
		{"question with a possessive", domain.Message{Text: "Nagi，你怎么看我的方案？"}, nil, domain.SemanticAnswer{}, "direct_name_call"},
		{"statement with question words", domain.Message{Text: "Nagi，你怎么看都比它强。"}, nil, domain.SemanticAnswer{Available: true}, "ambiguous"},
		{"statement about visibility", domain.Message{Text: "Nagi，你怎么看也看不清这张图。"}, nil, domain.SemanticAnswer{Available: true}, "ambiguous"},
		{"advice to another person", domain.Message{Text: "Nagi 你可以从 GitHub 下载，小王"}, nil, answer(.04, .28, .27, .18), "ambiguous"},
		{"question addressed to the group", domain.Message{Text: "你们知道 Nagi 是谁吗？"}, nil, answer(.04, .28, .27, .18), "ambiguous"},
		{"reports speech", domain.Message{Text: "Nagi告诉我昨天停电了"}, nil, answer(.04, .28, .27, .18), "ambiguous"},
		{"answered", second, []domain.Message{first, {SenderID: 2, IsBot: true, Text: "在", CreatedAt: first.CreatedAt.Add(time.Second)}}, answer(.04, .28, .27, .18), "ambiguous"},
		{"interrupted", second, []domain.Message{first, {SenderID: 2, Text: "有人", CreatedAt: first.CreatedAt.Add(time.Second)}}, answer(.04, .28, .27, .18), "ambiguous"},
		{"late", domain.Message{SenderID: 1, Text: "有人吗", CreatedAt: first.CreatedAt.Add(31 * time.Second)}, []domain.Message{first}, answer(.04, .28, .27, .18), "ambiguous"},
		{"name at start of a statement", domain.Message{SenderID: 1, Text: "Nagi：一个开源项目"}, nil, answer(.04, .28, .27, .18), "ambiguous"},
		{"ASCII name prefix", domain.Message{SenderID: 1, Text: "NagiHi"}, nil, answer(.04, .28, .27, .18), "ambiguous"},
		{"mention in prose", domain.Message{SenderID: 1, Text: "我刚部署了 Nagi"}, nil, answer(.04, .28, .27, .18), "ambiguous"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			in := base
			in.Task.Message, in.Recent = tc.msg, tc.history
			rule := EvaluateRules(in)
			got := EvaluateReplyPolicy(in, rule, tc.semantic)
			if got.ReasonCode != tc.want || (tc.want != "ambiguous" && rule.NeedSemantic) {
				t.Fatalf("decision=%+v rule=%+v want reason %s", got, rule, tc.want)
			}
		})
	}
}

func TestNameWakeWithImage(t *testing.T) {
	task := domain.Task{Segments: []obdomain.Segment{obdomain.Text("Nagi"), obdomain.Image("sample"), obdomain.Text("OK")}}
	if !mentionsBotName(task, "Nagi") {
		t.Fatal("image hid the name")
	}
	task.Segments = []obdomain.Segment{obdomain.Text("Na"), obdomain.Image("sample"), obdomain.Text("gi")}
	if mentionsBotName(task, "Nagi") {
		t.Fatal("image joined parts of a name")
	}
	task.Segments = []obdomain.Segment{obdomain.Text("Nagi"), obdomain.Image("sample"), obdomain.Text("Bot")}
	if mentionsBotName(task, "Nagi Bot") || directNameCall("", task.Segments, "Nagi Bot") {
		t.Fatal("image joined parts of a name containing a space")
	}
	for _, segments := range [][]obdomain.Segment{
		{obdomain.Text("Nagi，帮我看看这张图"), obdomain.Image("sample")},
		{obdomain.Text("Nagi，"), obdomain.Image("sample"), obdomain.Text("帮我看看这张图")},
		{obdomain.Text("Nagi，你"), obdomain.Image("sample"), obdomain.Text("能帮我看看这张图吗？")},
		{obdomain.Text("Nagi，你能解释这个吗"), obdomain.Image("sample"), obdomain.Text("！")},
	} {
		if !directNameCall("", segments, "Nagi") {
			t.Fatal("image prevented a direct request")
		}
	}
}

func TestNameWakeDoesNotRequireReply(t *testing.T) {
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
	request := domain.Message{MessageID: 11, SenderID: 101, Text: "Nagi你能看看我下面发的这个内容嘛？"}
	input := domain.Input{
		BotName:  "Nagi",
		Policy:   scopedomain.Policy{Enabled: true, AllowHistoryRead: true, ReplyMode: scopedomain.ModeIntentBased},
		Activity: domain.BotActivity{Known: true},
		Task:     domain.Task{Kind: "group", BotID: 900, Message: request},
	}
	decision := EvaluateReplyPolicy(input, EvaluateRules(input), domain.SemanticAnswer{})
	if decision.Decision != domain.DecisionDefer || decision.ReasonCode != "awaiting_content" {
		t.Fatalf("request decision = %+v", decision)
	}

	unrelated := domain.Message{MessageID: 12, SenderID: 202, Segments: []obdomain.Segment{obdomain.Image("unrelated")}}
	input.Recent = []domain.Message{request, unrelated}
	input.Task.Message = domain.Message{MessageID: 13, SenderID: request.SenderID}
	input.Task.Segments = []obdomain.Segment{obdomain.Image("requested")}
	input.AwaitedRequest = &request
	decision = EvaluateReplyPolicy(input, EvaluateRules(input), domain.SemanticAnswer{})
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
	input.Task.Segments = []obdomain.Segment{obdomain.Text("这是需要你查看的完整说明。")}
	if !awaitingReply(input) {
		t.Fatal("text did not match the pre-announced text request")
	}
	input.Task.Segments = []obdomain.Segment{obdomain.Reply(11), obdomain.Text("选 B")}
	if !awaitingReply(input) {
		t.Fatal("short text quoting the request did not match")
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
