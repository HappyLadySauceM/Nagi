// Tests for the Jev HTTP adapter's request shape and defensive parsing.
// Jev HTTP 适配器请求形态与防御性解析的测试。
package infrastructure

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

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
    "intent": {"choice": "chat"}
  }
}`

// TestJevClientParsesValidAnswers verifies the happy path and value mapping.
// TestJevClientParsesValidAnswers 验证正常路径与值映射。
func TestJevClientParsesValidAnswers(t *testing.T) {
	var gotAuth, gotModel string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		var body struct {
			Model string         `json:"model"`
			State map[string]any `json:"state"`
		}
		_ = readJSON(r, &body)
		gotModel = body.Model
		if body.State == nil {
			t.Error("state is required")
		}
		w.Write([]byte(validAnswers))
	}))
	defer srv.Close()

	answer, err := testClient(t, srv.URL).Decide(context.Background(), testInput())
	if err != nil {
		t.Fatalf("decide: %v", err)
	}
	if !answer.Available || answer.ContinuesBotThread != 0.88 || answer.AddressedToOther != 0.10 || answer.IntentCategory != "chat" {
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
		"intent":               {Type: "choice", Instructions: "custom intent instruction", Criteria: map[string]string{"chat": "chat"}},
	}}}
	if _, err := testClient(t, srv.URL).Decide(context.Background(), in); err != nil {
		t.Fatalf("decide: %v", err)
	}
	if gotInstruction != "custom directed instruction" {
		t.Fatalf("questions did not use prompt snapshot: %q", gotInstruction)
	}
}
