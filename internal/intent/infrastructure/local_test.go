package infrastructure

import (
	"context"
	"encoding/json"
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
		var body struct {
			Model     string `json:"model"`
			Logprobs  bool   `json:"logprobs"`
			MaxTokens int    `json:"max_tokens"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		if body.Model != "test" || !body.Logprobs || body.MaxTokens != 1 {
			t.Error("missing scoring contract")
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"logprobs":{"content":[{"top_logprobs":[{"token":"A","logprob":-0.1},{"token":"B","logprob":-2},{"token":"C","logprob":-3},{"token":"D","logprob":-4},{"token":"E","logprob":-5}]}]}}]}`))
	}))
	defer server.Close()
	client := &LocalClient{Config: JevConfig{Endpoint: server.URL, Model: "test", Timeout: time.Second}, Client: server.Client()}
	answer, err := client.Decide(context.Background(), testInput())
	if err != nil {
		t.Fatal(err)
	}
	if !answer.Available || answer.DirectedToBot < 0.8 || answer.IntentCategory != "question" {
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
