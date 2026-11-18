// Tests for the ruleset question API round-trip.
// 规则集问题集 API 往返测试。
package intent

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/HappyLadySauceM/Nagi/cmd/app/svc"
	intentdomain "github.com/HappyLadySauceM/Nagi/internal/intent/domain"
	intentinfra "github.com/HappyLadySauceM/Nagi/internal/intent/infrastructure"
	scopeapp "github.com/HappyLadySauceM/Nagi/internal/scope/application"
	scopeinfra "github.com/HappyLadySauceM/Nagi/internal/scope/infrastructure"
	"github.com/HappyLadySauceM/Nagi/pkg/db/dbtest"
)

// TestSimulationPromptUsesEffectiveQuestionSet verifies the semantic simulation
// builds its prompt from the specific (request) question set, falling back to
// the global set, and returns no prompt when neither set carries questions.
// TestSimulationPromptUsesEffectiveQuestionSet 验证语义模拟优先使用具体（请求）问题集，
// 缺失时回退全局集，两者都没有问题时返回无 Prompt。
func TestSimulationPromptUsesEffectiveQuestionSet(t *testing.T) {
	specificQuestions := map[string]intentdomain.PromptQuestion{
		"directed_to_bot": {Type: "noul", Role: "directed_to_bot", Weight: 1, Instructions: "对机器人说话？", Order: 50, Threshold: 0.8, Action: intentdomain.ActionReply, Scope: intentdomain.QuestionScopePrivate},
	}
	globalQuestions := map[string]intentdomain.PromptQuestion{
		"appropriate_to_join": {Type: "noul", Role: "appropriate_to_join", Weight: 1, Instructions: "适合加入？", Order: 40, Threshold: 0.95, Action: intentdomain.ActionReply, Scope: intentdomain.QuestionScopeGroup},
	}

	prompt, ok := simulationPrompt(
		&intentdomain.Ruleset{Version: 7, Questions: specificQuestions},
		&intentdomain.Ruleset{Version: 3, Questions: globalQuestions},
		42, "private", 99, "Nagi",
	)
	if !ok {
		t.Fatal("expected a prompt when the specific set has questions")
	}
	if got := prompt.SystemOne.Questions["directed_to_bot"].Instructions; got != "对机器人说话？" {
		t.Fatalf("questions = %+v", prompt.SystemOne.Questions)
	}
	if prompt.Version != 7 || prompt.Kind != intentdomain.PromptKindIntentParticipation ||
		prompt.Provider != intentdomain.PromptProviderJev || prompt.Source != intentdomain.PromptSourceLocal {
		t.Fatalf("prompt meta = %+v", prompt)
	}
	if prompt.BotID != 42 || prompt.TargetType != "private" || prompt.TargetID != 99 {
		t.Fatalf("prompt target = %+v", prompt)
	}
	if prompt.Variables["scope_type"] != "private" || prompt.Variables["bot_id"] != "42" || prompt.Variables["target_id"] != "99" {
		t.Fatalf("variables = %+v", prompt.Variables)
	}
	if prompt.Variables["bot_name"] != "Nagi" {
		t.Fatalf("bot_name variable = %+v", prompt.Variables)
	}
	if prompt.Hash == "" || prompt.FinalHash == "" {
		t.Fatalf("hashes missing: %+v", prompt)
	}
	if prompt.RenderedHash != intentdomain.PromptHash(prompt) {
		t.Fatalf("rendered hash = %q, want %q", prompt.RenderedHash, intentdomain.PromptHash(prompt))
	}

	// An empty specific set is authoritative and does not fall back to global questions.
	if _, ok := simulationPrompt(&intentdomain.Ruleset{Version: 5}, &intentdomain.Ruleset{Version: 3, Questions: globalQuestions}, 42, "group", 0, ""); ok {
		t.Fatal("empty specific set fell back to global questions")
	}
	fallback, ok := simulationPrompt(nil, &intentdomain.Ruleset{Version: 3, Questions: globalQuestions}, 42, "group", 0, "")
	if !ok || fallback.Version != 3 || len(fallback.SystemOne.Questions) != 1 {
		t.Fatalf("global questions = %+v ok=%v", fallback, ok)
	}
	if _, exists := fallback.Variables["target_id"]; exists {
		t.Fatalf("target_id should be omitted when scopeID <= 0: %+v", fallback.Variables)
	}

	// No questions anywhere leaves the caller's default prompt untouched.
	if _, ok := simulationPrompt(nil, &intentdomain.Ruleset{Version: 3}, 42, "group", 0, ""); ok {
		t.Fatal("expected no prompt without questions")
	}
}

// captureSimulator records the input the handler hands to semantic simulation.
// captureSimulator 记录处理器交给语义模拟的输入。
type captureSimulator struct {
	prepared  bool
	evaluated bool
	in        intentdomain.Input
}

func (c *captureSimulator) PrepareSimulation(_ context.Context, in intentdomain.Input) (intentdomain.Input, error) {
	c.prepared = true
	c.in = in
	return in, nil
}

func (c *captureSimulator) EvaluateSimulation(_ context.Context, in intentdomain.Input, _ intentdomain.RuleResult, _ bool) (intentdomain.FinalDecision, []intentdomain.Step) {
	c.evaluated = true
	c.in = in
	return intentdomain.FinalDecision{}, nil
}

// TestRulesSimulationIgnoresRequestQuestions verifies a rules-only preview does
// not validate or apply request questions, so a half-edited empty question set
// still succeeds instead of returning 400.
// TestRulesSimulationIgnoresRequestQuestions 验证仅规则预览不校验也不应用请求问题集，
// 编辑中的空问题集仍能成功而非返回 400。
func TestRulesSimulationIgnoresRequestQuestions(t *testing.T) {
	pool := dbtest.Open(t)
	repo, err := intentinfra.NewRulesetsRepo(pool)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	botID := time.Now().UnixNano()
	defer pool.Exec(ctx, "DELETE FROM intent_rulesets WHERE bot_id=$1", botID)
	mux := newRulesServer(repo)
	body, _ := json.Marshal(map[string]any{
		"bot_id": botID, "scope_type": "private", "scope_id": 55, "kind": "private",
		"reply_mode": "intent_based", "mode": "rules", "seconds_since_reply": 10,
		"questions": map[string]any{
			"directed_to_bot": map[string]any{"Type": "noul", "Instructions": ""},
		},
	})
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/v1/intent/simulate", bytes.NewReader(body)))
	if rec.Code != http.StatusOK {
		t.Fatalf("rules preview with empty question = %d: %s", rec.Code, rec.Body.String())
	}
}

// TestSemanticSimulationAppliesRequestQuestions verifies that only semantic
// simulation forwards the request question set into the prompt passed to the
// semantic evaluator.
// TestSemanticSimulationAppliesRequestQuestions 验证仅语义模拟把请求问题集写入
// 传给语义求值器的 Prompt。
func TestSemanticSimulationAppliesRequestQuestions(t *testing.T) {
	pool := dbtest.Open(t)
	repo, err := intentinfra.NewRulesetsRepo(pool)
	if err != nil {
		t.Fatal(err)
	}
	scopeRepo, err := scopeinfra.NewRepo(pool)
	if err != nil {
		t.Fatal(err)
	}
	scopeSvc, err := scopeapp.NewService(scopeRepo)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	botID := time.Now().UnixNano()
	defer pool.Exec(ctx, "DELETE FROM intent_rulesets WHERE bot_id=$1", botID)
	draft, err := repo.CreateDraft(ctx, botID, "private", 55, "semantic sim", false)
	if err != nil {
		t.Fatal(err)
	}
	sim := &captureSimulator{}
	mux := http.NewServeMux()
	NewHandler(&svc.ServiceContext{Rulesets: repo, Scope: scopeSvc, IntentSimulator: sim}).RegisterRules(mux)

	body, _ := json.Marshal(map[string]any{
		"bot_id": botID, "scope_type": "private", "scope_id": 55, "kind": "private",
		"reply_mode": "intent_based", "mode": "semantic", "text": "hi",
		"ruleset_id": draft.ID,
		"questions": map[string]any{
			"directed_to_bot": map[string]any{"Type": "noul", "Role": "directed_to_bot", "Weight": 1, "Instructions": "是否在与我说话？", "Order": 50, "Threshold": 0.8, "Action": "reply", "Scope": "private"},
		},
	})
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/v1/intent/simulate", bytes.NewReader(body)))
	if rec.Code != http.StatusOK {
		t.Fatalf("semantic preview = %d: %s", rec.Code, rec.Body.String())
	}
	if !sim.evaluated {
		t.Fatal("semantic evaluator was not called")
	}
	if sim.in.Prompt.SystemOne == nil {
		t.Fatalf("prompt systemone missing: %+v", sim.in.Prompt)
	}
	if got := sim.in.Prompt.SystemOne.Questions["directed_to_bot"].Instructions; got != "是否在与我说话？" {
		t.Fatalf("semantic prompt questions = %+v", sim.in.Prompt.SystemOne.Questions)
	}
}

// rulesetState captures the fields needed to assert PATCH preservation.
// rulesetState 捕获用于断言 PATCH 保留语义的字段。
type rulesetState struct {
	Questions map[string]struct {
		Type     string
		Role     string
		Criteria map[string]string
	}
	Entries []struct {
		Priority int
		Action   struct {
			ReasonCode string `json:"reason_code"`
		}
	}
	BuiltinRules map[string]bool
}

// TestRulesetQuestionsUpdateAndGet verifies PATCH stores a question set that a
// later GET returns, that a questions-only PATCH leaves stored entries and
// builtin switches untouched, that an invalid set is rejected, and that an
// entries field is no longer accepted.
// TestRulesetQuestionsUpdateAndGet 验证 PATCH 保存问题集后 GET 能返回；仅提交 questions
// 时已有条目与内置开关保持不变；无效问题集被拒绝；entries 字段不再接受。
func TestRulesetQuestionsUpdateAndGet(t *testing.T) {
	pool := dbtest.Open(t)
	repo, err := intentinfra.NewRulesetsRepo(pool)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	botID := time.Now().UnixNano()
	defer pool.Exec(ctx, "DELETE FROM intent_rulesets WHERE bot_id=$1", botID)
	set, err := repo.CreateDraft(ctx, botID, "private", 321, "questions api", false)
	if err != nil {
		t.Fatal(err)
	}
	seedEntries := []intentdomain.RuleEntry{{Priority: 0, Action: intentdomain.RuleAction{Type: intentdomain.ActionReply, ReasonCode: "seed"}}}
	seedBuiltin := map[string]bool{"closing": false}
	if err := repo.ReplaceEntries(ctx, set.ID, &seedEntries, intentinfra.RuleOptions{BuiltinRules: &seedBuiltin}); err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	NewHandler(&svc.ServiceContext{Rulesets: repo}).RegisterRules(mux)
	path := fmt.Sprintf("/api/v1/intent/rulesets/%d", set.ID)

	readState := func() rulesetState {
		get := httptest.NewRecorder()
		mux.ServeHTTP(get, httptest.NewRequest(http.MethodGet, path, nil))
		if get.Code != http.StatusOK {
			t.Fatalf("get status = %d: %s", get.Code, get.Body.String())
		}
		var env envelope
		if err := json.Unmarshal(get.Body.Bytes(), &env); err != nil {
			t.Fatal(err)
		}
		var state rulesetState
		if err := json.Unmarshal(env.Data, &state); err != nil {
			t.Fatal(err)
		}
		return state
	}

	body, _ := json.Marshal(map[string]any{"questions": map[string]any{
		"directed_to_bot": map[string]any{"Type": "noul", "Role": "directed_to_bot", "Weight": 1, "Instructions": "是否在对机器人说话？", "Order": 50, "Threshold": 0.8, "Action": "reply", "Scope": "private"},
		"intent":          map[string]any{"Type": "choice", "Role": "intent", "Instructions": "当前意图？", "Criteria": map[string]string{"closing": "告别", "chat": "闲聊"}, "Order": 10, "Action": "silent", "Scope": "all", "Match": "closing"},
	}})
	patch := httptest.NewRecorder()
	mux.ServeHTTP(patch, httptest.NewRequest(http.MethodPatch, path, bytes.NewReader(body)))
	if patch.Code != http.StatusOK {
		t.Fatalf("patch status = %d: %s", patch.Code, patch.Body.String())
	}

	state := readState()
	if state.Questions["intent"].Type != "choice" || state.Questions["intent"].Criteria["closing"] != "告别" || state.Questions["directed_to_bot"].Role != "directed_to_bot" {
		t.Fatalf("questions = %+v", state.Questions)
	}
	if len(state.Entries) != 1 || state.Entries[0].Action.ReasonCode != "seed" {
		t.Fatalf("questions-only patch changed entries: %+v", state.Entries)
	}
	if enabled, ok := state.BuiltinRules["closing"]; !ok || enabled {
		t.Fatalf("questions-only patch changed builtin switches: %+v", state.BuiltinRules)
	}

	bad, _ := json.Marshal(map[string]any{"questions": map[string]any{
		"intent": map[string]any{"Type": "noul", "Role": "intent", "Instructions": "x"},
	}})
	reject := httptest.NewRecorder()
	mux.ServeHTTP(reject, httptest.NewRequest(http.MethodPatch, path, bytes.NewReader(bad)))
	if reject.Code != http.StatusBadRequest {
		t.Fatalf("invalid questions status = %d, want 400: %s", reject.Code, reject.Body.String())
	}

	clearBody, _ := json.Marshal(map[string]any{"entries": []any{}})
	clearRec := httptest.NewRecorder()
	mux.ServeHTTP(clearRec, httptest.NewRequest(http.MethodPatch, path, bytes.NewReader(clearBody)))
	if clearRec.Code != http.StatusBadRequest {
		t.Fatalf("entries field status = %d, want 400: %s", clearRec.Code, clearRec.Body.String())
	}
}
