package infrastructure

import (
	"context"
	"encoding/json"
	scopedomain "github.com/HappyLadySauceM/Nagi/internal/scope/domain"

	"github.com/HappyLadySauceM/Nagi/internal/intent/domain"
	ob "github.com/HappyLadySauceM/Nagi/internal/onebot/domain"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestLocalCandidateProbabilities(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" {
			t.Error("empty-key adapter must omit authorization")
		}
		data, _ := io.ReadAll(r.Body)
		if strings.Contains(string(data), "base64://") || strings.Contains(string(data), "signed-secret") {
			t.Error("media resource entered text intent prompt")
		}
		var body struct {
			Model     string `json:"model"`
			Logprobs  bool   `json:"logprobs"`
			MaxTokens int    `json:"max_tokens"`
		}
		if err := json.Unmarshal(data, &body); err != nil {
			t.Error(err)
		}
		if body.Model != "test" || !body.Logprobs || body.MaxTokens != 1 {
			t.Error("missing scoring contract")
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"logprobs":{"content":[{"token":"\n","top_logprobs":[]},{"token":"A","top_logprobs":[{"token":"A","logprob":-0.1},{"token":"B","logprob":-2},{"token":"C","logprob":-3},{"token":"D","logprob":-4},{"token":"E","logprob":-5}]}]}}]}`))
	}))
	defer server.Close()
	client := &LocalClient{Config: JevConfig{Endpoint: server.URL, Model: "test", Timeout: time.Second}, Client: server.Client()}
	input := testInput()
	input.Recent[0].Segments = []ob.Segment{{Type: ob.SegImage, Data: map[string]string{"file": "base64://" + strings.Repeat("a", 100000), "url": "https://image.example/signed-secret"}}}
	answer, err := client.Decide(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if !answer.Available || answer.DirectedToBot < 0.8 || answer.Scores["intent"] < 0.8 {
		t.Fatalf("unexpected answer %+v", answer)
	}
}

func TestLocalRejectsIncompleteCandidates(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(`{"choices":[]}`)) }))
	defer server.Close()
	client := &LocalClient{Config: JevConfig{Endpoint: server.URL, Timeout: time.Second}, Client: server.Client()}
	if _, err := client.Decide(context.Background(), testInput()); err == nil || !strings.Contains(err.Error(), "candidate") {
		t.Fatalf("expected unavailable: %v", err)
	}
}

func TestLocalClientSharesConcurrencyAcrossDecisions(t *testing.T) {
	var active, maximum atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := active.Add(1)
		defer active.Add(-1)
		for old := maximum.Load(); n > old; old = maximum.Load() {
			if maximum.CompareAndSwap(old, n) {
				break
			}
		}
		time.Sleep(time.Millisecond)
		_, _ = w.Write([]byte(`{"choices":[{"logprobs":{"content":[{"top_logprobs":[{"token":"A","logprob":-0.1},{"token":"B","logprob":-2},{"token":"C","logprob":-3},{"token":"D","logprob":-4},{"token":"E","logprob":-5}]}]}}]}`))
	}))
	defer server.Close()
	c := &LocalClient{Config: JevConfig{Endpoint: server.URL, Timeout: time.Second}, Client: server.Client()}
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := c.Decide(context.Background(), testInput()); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if maximum.Load() > 2 {
		t.Fatalf("shared concurrency exceeded: %d", maximum.Load())
	}
}

func TestLocalClientUsesOpenAIUserTemplate(t *testing.T) {
	var mu sync.Mutex
	var sawTemplate bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Messages []map[string]string `json:"messages"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		if len(body.Messages) > 1 {
			content := body.Messages[1]["content"]
			if strings.Contains(content, "STATE=") && strings.Contains(content, "QUESTION=") && strings.Contains(content, "那你觉得呢？") {
				mu.Lock()
				sawTemplate = true
				mu.Unlock()
			}
		}
		_, _ = w.Write([]byte(`{"choices":[{"logprobs":{"content":[{"top_logprobs":[{"token":"A","logprob":-0.1},{"token":"B","logprob":-2},{"token":"C","logprob":-3},{"token":"D","logprob":-4},{"token":"E","logprob":-5}]}]}}]}`))
	}))
	defer server.Close()
	client := &LocalClient{Config: JevConfig{Endpoint: server.URL, Model: "test", Timeout: time.Second}, Client: server.Client()}
	input := testInput()
	input.Prompt = domain.Prompt{OpenAI: &domain.OpenAIPrompt{
		SystemPrompt:  "CUSTOM SYSTEM",
		UserTemplate:  "STATE={{state}}\nQUESTION={{question}}",
		ResponseShape: "single-letter",
	}}
	if _, err := client.Decide(context.Background(), input); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if !sawTemplate {
		t.Fatal("OpenAI user template was not applied")
	}
}

func TestLocalReusesFirstRequestForThreeWayCall(t *testing.T) {
	for _, want := range []domain.CallAddressing{domain.CallYes, domain.CallNo, domain.CallUncertain} {
		t.Run(string(want), func(t *testing.T) {
			var requests, callQuestions atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				var body struct{ Messages []map[string]string }
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Error(err)
					return
				}
				values := map[string]float64{"A": -.1, "B": -3, "C": -3}
				query := body.Messages[1]["content"]
				if strings.Contains(query, "是否适合在此时加入讨论") {
					http.Error(w, "fixture auxiliary failure", http.StatusServiceUnavailable)
					return
				}
				if strings.Contains(query, "期待它回应吗") {
					callQuestions.Add(1)
					if !strings.Contains(query, `当前机器人称呼：{"call_name":"薄荷","qq_nickname":"橘子"}`) ||
						!strings.Contains(query, `"bot_identity":{"call_name":"薄荷","qq_nickname":"橘子"}`) || strings.Contains(query, `"bot_name"`) {
						t.Error("nickname mismatch")
					}
					values = map[string]float64{"A": -3, "B": -3, "C": -3}
					values[map[domain.CallAddressing]string{domain.CallYes: "A", domain.CallNo: "B", domain.CallUncertain: "C"}[want]] = -.1
				}
				var top []map[string]any
				for token, lp := range values {
					top = append(top, map[string]any{"token": token, "logprob": lp})
				}
				json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"logprobs": map[string]any{"content": []any{map[string]any{"token": "A", "top_logprobs": top}}}}}})
			}))
			defer server.Close()
			client := &LocalClient{Config: JevConfig{Endpoint: server.URL, Model: "fixture", Timeout: time.Second}, Client: server.Client()}
			in := testInput()
			in.Policy.ReplyMode = scopedomain.ModeIntentBased
			in.CallIdentity = &domain.CallIdentity{BotID: in.Task.BotID, CallName: "薄荷", QQNickname: "橘子"}
			in.Prompt.OpenAI = &domain.OpenAIPrompt{UserTemplate: "仅自定义文字"}
			out, err := client.Decide(context.Background(), in)
			if err != nil || out.CallAddressing != want || out.FailureCode != "decision_questions_degraded" || out.ExternalRequests != 5 || requests.Load() != 5 || callQuestions.Load() != 1 {
				t.Fatalf("answer=%+v err=%v requests=%d call_questions=%d", out, err, requests.Load(), callQuestions.Load())
			}
		})
	}
}
