// Tests for the Jev HTTP adapter's request shape and defensive parsing.
// Jev HTTP 适配器请求形态与防御性解析的测试。
package infrastructure

import (
	"context"
	"encoding/json"
	"fmt"
	scopedomain "github.com/HappyLadySauceM/Nagi/internal/scope/domain"
	"maps"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"github.com/HappyLadySauceM/Nagi/internal/intent/domain"
)

func testInput() domain.Input {
	now := time.Now()
	return domain.Input{
		Task: domain.Task{
			JobID: 1, BotID: 100, Kind: "group", TargetID: 700,
			Message: domain.Message{MessageID: 5001, SenderID: 200, Text: "那你觉得呢？"},
		},
		Recent: []domain.Message{
			{MessageID: 5000, SenderID: 200, Text: "小雪，Go 合适吗？", CreatedAt: now.Add(-40 * time.Second)},
			{MessageID: 4990, SenderID: 100, IsBot: true, Text: "适合。", CreatedAt: now.Add(-35 * time.Second)},
		},
		Now: now,
	}
}

func testClient(t *testing.T, url string) *JevClient {
	t.Helper()
	c, err := NewJevClient(JevConfig{Endpoint: url, Model: "jev-latest", BotName: "小雪", APIKey: "test-key", Timeout: time.Second}, discardLogger())
	if err != nil {
		t.Fatalf("new jev client: %v", err)
	}
	return c
}

const validAnswers = `{
  "answers": {
    "directed_to_bot": {"noul": 0.42},
    "continues_bot_thread": {"noul": 0.88},
    "addressed_to_other": {"noul": 0.10},
    "appropriate_to_join": {"noul": 0.30},
    "intent": {"noul": 0.2}
  }
}`

// TestJevClientParsesValidAnswers verifies the happy path and value mapping.
// TestJevClientParsesValidAnswers 验证正常路径与值映射。
func TestJevClientParsesValidAnswers(t *testing.T) {
	var gotAuth, gotModel string
	var questions json.RawMessage
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		var body struct {
			Model     string          `json:"model"`
			State     map[string]any  `json:"state"`
			Questions json.RawMessage `json:"questions"`
		}
		_ = readJSON(r, &body)
		gotModel = body.Model
		questions = body.Questions
		if body.State == nil {
			t.Error("state is required")
		}
		w.Write([]byte(validAnswers))
	}))
	defer srv.Close()

	recorder := tracetest.NewSpanRecorder()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
	ctx, span := provider.Tracer("test").Start(context.Background(), "systemone")
	answer, err := testClient(t, srv.URL).Decide(ctx, testInput())
	span.End()
	values := make(map[string]string)
	for _, attr := range recorder.Ended()[0].Attributes() {
		values[string(attr.Key)] = attr.Value.Emit()
	}
	if values["langfuse.observation.metadata.systemone_question_hash"] != domain.PromptTextHash(string(questions)) ||
		values["langfuse.observation.type"] != "generation" || values["langfuse.observation.model.name"] != "jev-latest" ||
		values["langfuse.observation.metadata.prompt_source"] != "builtin" || values["langfuse.observation.metadata.prompt_version"] != "1" {
		t.Fatalf("unexpected SystemOne evidence: %v", values)
	}
	if err != nil {
		t.Fatalf("decide: %v", err)
	}
	if !answer.Available || answer.ContinuesBotThread != 0.88 || answer.AddressedToOther != 0.10 || answer.Scores["intent"] != 0.2 {
		t.Fatalf("answer = %+v", answer)
	}
	if gotAuth != "Bearer test-key" || gotModel != "jev-latest" {
		t.Fatalf("auth=%q model=%q", gotAuth, gotModel)
	}
}

// TestJevClientRejectsMalformedResponses verifies that missing, malformed or
// out-of-range answers are unusable instead of silently zero-valued.
// TestJevClientRejectsMalformedResponses 验证缺失、畸形或越界的回答不可用，
// 而不是被静默当零值。
func TestJevClientRejectsMalformedResponses(t *testing.T) {
	cases := map[string]string{
		"missing answer": `{"answers": {"directed_to_bot": {"noul": 0.5}}}`,
		"out of range": `{"answers": {
			"directed_to_bot": {"noul": 1.5},
			"continues_bot_thread": {"noul": 0.1},
			"addressed_to_other": {"noul": 0.1},
			"appropriate_to_join": {"noul": 0.1},
			"intent": {"choice": "chat"}}}`,
		"wrong type": `{"answers": {
			"directed_to_bot": {"score": 0.5},
			"continues_bot_thread": {"noul": 0.1},
			"addressed_to_other": {"noul": 0.1},
			"appropriate_to_join": {"noul": 0.1},
			"intent": {"choice": "chat"}}}`,
		"empty answers": `{}`,
		"choice without category": `{"answers": {
			"directed_to_bot": {"noul": 0.5},
			"continues_bot_thread": {"noul": 0.1},
			"addressed_to_other": {"noul": 0.1},
			"appropriate_to_join": {"noul": 0.1},
			"intent": {}}}`,
	}
	for name, body := range cases {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Write([]byte(body))
		}))
		_, err := testClient(t, srv.URL).Decide(context.Background(), testInput())
		srv.Close()
		if err == nil {
			t.Fatalf("%s: expected error", name)
		}
	}
}

// TestJevClientRejectsHTTPError verifies non-200 responses are errors.
// TestJevClientRejectsHTTPError 验证非 200 响应视为错误。
func TestJevClientRejectsHTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer srv.Close()
	if _, err := testClient(t, srv.URL).Decide(context.Background(), testInput()); err == nil {
		t.Fatal("expected error for 429")
	}
}

// TestJevClientUsesPromptSnapshot verifies that a selected prompt snapshot, not
// a hard-coded question set, is sent to the SystemOne endpoint.
// TestJevClientUsesPromptSnapshot 验证发送到 SystemOne 的是选定的 Prompt 快照，
// 而不是硬编码问题集。
func TestJevClientUsesPromptSnapshot(t *testing.T) {
	var gotInstruction string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Questions map[string]struct {
				Instructions string `json:"instructions"`
			} `json:"questions"`
		}
		_ = readJSON(r, &body)
		gotInstruction = body.Questions["directed_to_bot"].Instructions
		w.Write([]byte(validAnswers))
	}))
	defer srv.Close()
	in := testInput()
	in.Prompt = domain.Prompt{SystemOne: &domain.SystemOnePrompt{Questions: map[string]domain.PromptQuestion{
		"directed_to_bot":      {Type: "noul", Instructions: "custom directed instruction"},
		"continues_bot_thread": {Type: "noul", Instructions: "custom continuation instruction"},
		"addressed_to_other":   {Type: "noul", Instructions: "custom other instruction"},
		"appropriate_to_join":  {Type: "noul", Instructions: "custom join instruction"},
		"intent":               {Type: "noul", Instructions: "custom intent instruction"},
	}}}
	if _, err := testClient(t, srv.URL).Decide(context.Background(), in); err != nil {
		t.Fatalf("decide: %v", err)
	}
	if gotInstruction != "custom directed instruction" {
		t.Fatalf("questions did not use prompt snapshot: %q", gotInstruction)
	}
}

// TestJevClientParsesCustomQuestionSet verifies parsing follows the configured
// questions: custom noul questions land in Scores and drive the aggregate
// participation score, and Role/Weight are not sent to the model.
// TestJevClientParsesCustomQuestionSet 验证解析跟随配置的问题：custom noul 问题写入
// Scores 并驱动综合分，且 Role/Weight 不会发送给模型。
func TestJevClientParsesCustomQuestionSet(t *testing.T) {
	var sentKeys map[string]map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Questions map[string]map[string]any `json:"questions"`
		}
		_ = readJSON(r, &body)
		sentKeys = body.Questions
		w.Write([]byte(`{"answers":{"intent":{"choice":"closing"},"urgency":{"noul":0.8}}}`))
	}))
	defer srv.Close()

	in := testInput()
	in.Prompt = domain.Prompt{SystemOne: &domain.SystemOnePrompt{Questions: map[string]domain.PromptQuestion{
		"intent":  {Type: "choice", Role: domain.QuestionRoleIntent, Criteria: map[string]string{"closing": "告别"}},
		"urgency": {Type: "noul", Role: domain.QuestionRoleCustom, Weight: 10, Instructions: "是否紧急？"},
	}}}
	answer, err := testClient(t, srv.URL).Decide(context.Background(), in)
	if err != nil {
		t.Fatalf("decide: %v", err)
	}
	if !answer.Available || answer.IntentCategory != "closing" || answer.Scores["urgency"] != 0.8 {
		t.Fatalf("answer = %+v", answer)
	}
	if !answer.RolePresent["intent"] {
		t.Fatalf("intent role not present: %+v", answer.RolePresent)
	}
	if !answer.HasParticipation || answer.ParticipationScore < 0.8-1e-9 || answer.ParticipationScore > 0.8+1e-9 {
		t.Fatalf("participation = %v/%v, want 0.8", answer.ParticipationScore, answer.HasParticipation)
	}
	for key, fields := range sentKeys {
		if _, ok := fields["role"]; ok {
			t.Fatalf("question %q leaked role to model: %+v", key, fields)
		}
		if _, ok := fields["weight"]; ok {
			t.Fatalf("question %q leaked weight to model: %+v", key, fields)
		}
	}
}

func TestJevTopicQuestionsDoNotReplacePublishedQuestions(t *testing.T) {
	action := domain.TopicActionKey(0)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			Questions map[string]jevQuestion `json:"questions"`
		}
		_ = readJSON(r, &request)
		answers := map[string]any{}
		for key, question := range request.Questions {
			switch {
			case question.Type == "choice":
				answers[key] = map[string]string{"choice": domain.TopicClose}
			case key == action:
				answers[key] = map[string]float64{"noul": .31}
			default:
				answers[key] = map[string]float64{"noul": .93}
			}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"answers": answers})
	}))
	defer srv.Close()
	in := testInput()
	in.Topics = &domain.TopicContext{Candidates: []domain.TopicCandidate{{ID: 23, Active: true}}}
	in.Prompt = domain.Prompt{SystemOne: &domain.SystemOnePrompt{Questions: map[string]domain.PromptQuestion{
		action: {Type: "noul", Role: domain.QuestionRoleCustom, Weight: 1, Instructions: "原有题目"},
	}}}
	answer, err := testClient(t, srv.URL).Decide(context.Background(), in)
	if err != nil || len(answer.Scores) != 1 || answer.Scores[action] != .31 || len(answer.TopicJudgments) != 1 ||
		answer.TopicJudgments[0] != (domain.TopicJudgment{Affinity: .93, Action: domain.TopicClose}) {
		t.Fatalf("answer = %+v, err=%v", answer, err)
	}
}

// TestJevClientComputesParticipationFromDefaultQuestions verifies the built-in
// five questions still populate named roles and a default-weighted aggregate.
// TestJevClientComputesParticipationFromDefaultQuestions 验证内置五问仍填充具名角色，
// 并按默认权重计算综合分。
func TestJevClientComputesParticipationFromDefaultQuestions(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(validAnswers))
	}))
	defer srv.Close()

	answer, err := testClient(t, srv.URL).Decide(context.Background(), testInput())
	if err != nil {
		t.Fatalf("decide: %v", err)
	}
	if answer.Roles["addressed_to_other"] != 0.10 || !answer.RolePresent["directed_to_bot"] {
		t.Fatalf("roles = %+v present=%+v", answer.Roles, answer.RolePresent)
	}
	// (0.42 + 0.88 - 0.10 + 0.30) / 4
	want := (0.42 + 0.88 - 0.10 + 0.30) / 4
	if !answer.HasParticipation || answer.ParticipationScore < want-1e-9 || answer.ParticipationScore > want+1e-9 {
		t.Fatalf("participation = %v, want %v", answer.ParticipationScore, want)
	}
}

func TestJevAsksCallInSameRequestAsCustomQuestion(t *testing.T) {
	for _, tc := range []struct {
		label, callName, nickname string
		want                      map[string]string
	}{
		{"both escaped", `薄荷"<角色>`, "橘子\n第二行", map[string]string{"call_name": `薄荷"<角色>`, "qq_nickname": "橘子\n第二行"}},
		{"call name only", "薄荷", "", map[string]string{"call_name": "薄荷"}},
		{"nickname only", "", "橘子", map[string]string{"qq_nickname": "橘子"}},
		{"same name", "薄荷", "薄荷", map[string]string{"call_name": "薄荷"}},
	} {
		t.Run(tc.label, func(t *testing.T) {
			var calls atomic.Int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				var req struct {
					State     map[string]json.RawMessage
					Questions map[string]jevQuestion
				}
				if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
					t.Error(err)
					return
				}
				question := req.Questions[domain.CallQuestionKey]
				var names map[string]string
				_, rendered, found := strings.Cut(question.Instructions, "当前机器人称呼：")
				if !found || json.Unmarshal(req.State["bot_identity"], &names) != nil || !maps.Equal(names, tc.want) || req.State["bot_name"] != nil ||
					rendered != string(req.State["bot_identity"]) || len(question.Criteria) != 3 || len(req.Questions) != 2 || req.Questions["custom"].Type != "noul" {
					t.Errorf("wrong effective request: %+v", req)
				}
				fmt.Fprintf(w, `{"answers":{"custom":{"noul":0.12},%q:{"choice":"yes"}}}`, domain.CallQuestionKey)
			}))
			defer srv.Close()
			in := testInput()
			in.Policy.ReplyMode = scopedomain.ModeIntentBased
			in.CallIdentity = &domain.CallIdentity{BotID: in.Task.BotID, CallName: tc.callName, QQNickname: tc.nickname}
			in.Prompt.SystemOne = &domain.SystemOnePrompt{Questions: map[string]domain.PromptQuestion{"custom": {Type: "noul", Instructions: "fixture"}}}
			out, err := testClient(t, srv.URL).Decide(context.Background(), in)
			if err != nil || out.CallAddressing != domain.CallYes || out.ExternalRequests != 1 || calls.Load() != 1 || out.Scores["custom"] != .12 {
				t.Fatalf("answer=%+v err=%v calls=%d", out, err, calls.Load())
			}
		})
	}
}

func TestJevKeepsCallAndCustomKeyWhenOtherQuestionFails(t *testing.T) {
	for _, result := range []domain.CallAddressing{domain.CallYes, domain.CallNo} {
		t.Run(string(result), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var payload struct{ Questions map[string]jevQuestion }
				if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
					t.Error(err)
					return
				}
				key := domain.CallQuestionKey
				if payload.Questions[key].Type != "noul" || payload.Questions[key+"_"].Type != "choice" {
					t.Errorf("colliding question was overwritten: %+v", payload.Questions)
				}
				fmt.Fprintf(w, `{"answers":{%q:{"noul":0.12},%q:{"choice":%q},"nagi_topic_1":{"noul":0.95},"nagi_topic_1_action":{"choice":"close"}}}`, key, key+"_", result)
			}))
			defer server.Close()
			in := testInput()
			in.Policy.ReplyMode = scopedomain.ModeIntentBased
			in.CallIdentity = &domain.CallIdentity{BotID: in.Task.BotID, QQNickname: "薄荷"}
			in.Topics = &domain.TopicContext{Candidates: []domain.TopicCandidate{{ID: 1, Active: true}}}
			in.Prompt.SystemOne = &domain.SystemOnePrompt{Questions: map[string]domain.PromptQuestion{
				domain.CallQuestionKey: {Type: "noul", Instructions: "custom"},
				"missing":              {Type: "noul", Instructions: "omitted"},
			}}
			out, err := testClient(t, server.URL).Decide(context.Background(), in)
			if err != nil || out.CallAddressing != result || out.FailureCode != "decision_questions_degraded" || out.Scores[domain.CallQuestionKey] != .12 || len(out.TopicJudgments) != 1 || out.TopicJudgments[0].Action != domain.TopicClose {
				t.Fatalf("partial response: %+v err=%v", out, err)
			}
		})
	}
}

func TestJevPreservesTopicCloseWhenCallAndCustomAnswersAreMissing(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"answers":{"nagi_topic_1":{"noul":0.97},"nagi_topic_1_action":{"choice":"close"}}}`)
	}))
	defer srv.Close()
	in := testInput()
	in.Policy.ReplyMode = scopedomain.ModeIntentBased
	in.CallIdentity = &domain.CallIdentity{BotID: in.Task.BotID, QQNickname: "薄荷"}
	in.Topics = &domain.TopicContext{QuotedID: 1, Candidates: []domain.TopicCandidate{{ID: 1, Active: true}}}
	in.Prompt.SystemOne = &domain.SystemOnePrompt{Questions: map[string]domain.PromptQuestion{"custom": {Type: "noul", Instructions: "fixture"}}}
	out, err := testClient(t, srv.URL).Decide(context.Background(), in)
	if err != nil || !out.Available || out.CallAddressing != domain.CallUnknown || out.CallFailureCode != "question_missing" || out.FailureCode != "decision_questions_degraded" || len(out.TopicJudgments) != 1 || out.TopicJudgments[0].Action != domain.TopicClose {
		t.Fatalf("partial response: %+v err=%v", out, err)
	}
}

func TestJevDistinguishesMissingTopicScoreFromZero(t *testing.T) {
	for _, tc := range []struct {
		name, competitor string
		missing          bool
	}{
		{"missing", "", true},
		{"null", `,"nagi_topic_2":{"noul":null}`, true},
		{"zero", `,"nagi_topic_2":{"noul":0}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				fmt.Fprintf(w, `{"answers":{"nagi_topic_1":{"noul":0.97},"nagi_topic_1_action":{"choice":"close"},"nagi_topic_2_action":{"choice":"other"}%s}}`, tc.competitor)
			}))
			defer srv.Close()
			in := testInput()
			in.Policy.ReplyMode = scopedomain.ModeIntentBased
			in.CallIdentity = &domain.CallIdentity{BotID: in.Task.BotID, QQNickname: "薄荷"}
			in.Topics = &domain.TopicContext{Candidates: []domain.TopicCandidate{{ID: 1, Active: true}, {ID: 2, Active: true}}}
			got, err := testClient(t, srv.URL).Decide(context.Background(), in)
			if err != nil || !got.Available || got.ExternalRequests != 1 || got.CallFailureCode != "question_missing" ||
				len(got.TopicJudgments) != 2 || got.TopicJudgments[0] != (domain.TopicJudgment{Affinity: .97, Action: domain.TopicClose}) ||
				got.TopicJudgments[1] != (domain.TopicJudgment{AffinityMissing: tc.missing, Action: "other"}) {
				t.Fatalf("answer = %+v, err = %v", got, err)
			}
		})
	}
}

func TestFixedJevValidatesEachAskedTopicAction(t *testing.T) {
	for _, action := range []string{"missing", "null", "unsupported", "other"} {
		t.Run(action, func(t *testing.T) {
			var count atomic.Int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				count.Add(1)
				var request struct{ Questions map[string]jevQuestion }
				if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
					t.Error(err)
					return
				}
				answers := make(map[string]any)
				for key, q := range request.Questions {
					if q.Type == "noul" {
						answers[key] = map[string]int{"noul": 0}
					} else if key == domain.TopicActionKey(1) {
						switch action {
						case "missing":
						case "null":
							answers[key] = nil
						default:
							answers[key] = map[string]string{"choice": action}
						}
					} else {
						answers[key] = map[string]string{"choice": "other"}
					}
				}
				_ = json.NewEncoder(w).Encode(map[string]any{"answers": answers, "usage": map[string]any{"input_tokens": "unknown", "output_tokens": 0}})
			}))
			defer srv.Close()
			in := testInput()
			in.FixedBackground = true
			in.Policy.AllowHistoryRead = true
			in.Topics = &domain.TopicContext{Candidates: []domain.TopicCandidate{{ID: 1}, {ID: 2}}}
			out, err := testClient(t, srv.URL).Decide(context.Background(), in)
			valid := action == "other"
			if count.Load() != 1 || (err == nil) != valid || out.Available != valid || out.Usage.InputValue() != nil || out.Usage.OutputValue() == nil || *out.Usage.OutputValue() != 0 {
				t.Fatalf("action=%q requests=%d answer=%+v err=%v", action, count.Load(), out, err)
			}
			if valid && (len(out.TopicJudgments) != 2 || out.TopicJudgments[1].Action != "other" || out.TopicJudgments[1].AffinityMissing || out.TopicJudgments[1].Affinity != 0) {
				t.Fatalf("explicit zero or valid action lost: %+v", out.TopicJudgments)
			}
			t.Logf("action=%s requests=%d accepted=%t explicit_output_zero=true", action, count.Load(), valid)
		})
	}
}
