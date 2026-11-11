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
			semantic:     domain.SemanticAnswer{Available: true, ContinuesBotThread: 0.90, AddressedToOther: 0.10, IntentCategory: "closing"},
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
			// Known conflict reported for the first-round baseline: interjection
			// is checked before the addressed-to-other rejection and does not
			// consult AddressedOtherMax. This sample freezes today's observable
			// behavior; the product decision may change it deliberately.
			// 首轮基线报告的已知冲突：interjection 在 addressed_to_other 拒绝前判定，
			// 且未检查 AddressedOtherMax。本样例固定当前可观察行为，待产品决策后有意变更。
			name: "conflict_interjection_addressed_to_other", input: baselineGroupInput("你们俩聊什么呢？", nil),
			semantic:     domain.SemanticAnswer{Available: true, AppropriateToJoin: 0.97, AddressedToOther: 0.90, IntentCategory: "chat"},
			wantDecision: domain.DecisionReply, wantReason: "interjection", wantNeedSemantic: true,
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
			decision := EvaluateReplyPolicy(tc.input, rule, tc.semantic, baselineThresholds)
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
	if version != "scope:41@7;global:42@3" {
		t.Fatalf("ruleset version = %q, want scope:41@7;global:42@3", version)
	}
	semantic := domain.SemanticAnswer{Available: false, FailureCode: "semantic_unavailable"}
	decision := EvaluateReplyPolicy(in, rule, semantic, baselineThresholds)
	decision.RulesetVersion = version
	steps := decisionSteps(in.Policy, rule, semantic, decision, 12, true, baselineThresholds)
	if len(steps) != 4 {
		t.Fatalf("steps = %d, want policy/rules/semantic/final", len(steps))
	}
	if steps[0].Phase != domain.StepPolicy || steps[1].Phase != domain.StepRules ||
		steps[2].Phase != domain.StepSemantic || steps[3].Phase != domain.StepFinal {
		t.Fatalf("phases = %+v", steps)
	}
	evaluations, ok := steps[1].Detail["evaluations"].([]domain.RuleEvaluation)
	if !ok || len(evaluations) != 1 || evaluations[0].RulesetID != 41 ||
		evaluations[0].Version != 7 || evaluations[0].Scope != "specific" || evaluations[0].Status != "matched" {
		t.Fatalf("rule evaluations = %+v", steps[1].Detail["evaluations"])
	}
	if steps[2].Detail["available"] != false || steps[2].Detail["failure_code"] != "semantic_unavailable" {
		t.Fatalf("semantic step = %+v", steps[2].Detail)
	}
	thresholds, ok := steps[3].Detail["thresholds"].(map[string]float64)
	if !ok || thresholds["continuation"] != baselineThresholds.Continuation ||
		thresholds["interjection"] != baselineThresholds.Interjection ||
		thresholds["addressed_other_max"] != baselineThresholds.AddressedOtherMax {
		t.Fatalf("threshold snapshot = %+v", steps[3].Detail["thresholds"])
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
	if len(steps) != 4 || len(recorder.decisions) != 0 || len(executor.tasks) != 0 {
		t.Fatalf("simulation steps = %d, recorded = %d, executed = %d", len(steps), len(recorder.decisions), len(executor.tasks))
	}
}
