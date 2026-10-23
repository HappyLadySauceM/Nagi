// Tests for the OpenAI-compatible chat adapter.
// OpenAI 兼容对话适配器的测试。
package infrastructure

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	agentdomain "github.com/HappyLadySauceM/QQBot/internal/agent/domain"
)

// TestOpenAIGenerateParsesResponse verifies the request payload and parsing.
// TestOpenAIGenerateParsesResponse 验证请求负载与解析。
func TestOpenAIGenerateParsesResponse(t *testing.T) {
	var gotPath, gotAuth string
	var gotBody struct {
		Model     string `json:"model"`
		MaxTokens int    `json:"max_tokens"`
		Messages  []struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		} `json:"messages"`
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		_ = decodeBody(r, &gotBody)
		w.Write([]byte(`{"choices":[{"message":{"content":"你好！"}}],"usage":{"prompt_tokens":10,"completion_tokens":3}}`))
	}))
	defer srv.Close()

	adapter, err := NewOpenAI(OpenAIConfig{BaseURL: srv.URL, APIKey: "key-1", Model: "test-model", Timeout: time.Second, MaxOutputTokens: 50})
	if err != nil {
		t.Fatalf("new openai: %v", err)
	}
	result, err := adapter.Generate(context.Background(), agentdomain.ChatRequest{
		Messages:  []agentdomain.ChatMessage{{Role: "system", Content: "s"}, {Role: "user", Content: "u"}},
		MaxTokens: 50,
	})
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	if result.Text != "你好！" || result.CompletionTokens != 3 {
		t.Fatalf("result = %+v", result)
	}
	if gotPath != "/chat/completions" || gotAuth != "Bearer key-1" {
		t.Fatalf("path=%q auth=%q", gotPath, gotAuth)
	}
	if gotBody.Model != "test-model" || gotBody.MaxTokens != 50 || len(gotBody.Messages) != 2 {
		t.Fatalf("body = %+v", gotBody)
	}
}

// TestOpenAIRejectsErrors verifies non-200 and empty choices are errors.
// TestOpenAIRejectsErrors 验证非 200 与空 choices 视为错误。
func TestOpenAIRejectsErrors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()
	adapter, _ := NewOpenAI(OpenAIConfig{BaseURL: srv.URL, Model: "m", Timeout: time.Second})
	if _, err := adapter.Generate(context.Background(), agentdomain.ChatRequest{}); err == nil {
		t.Fatal("expected error for 500")
	}

	empty := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"choices":[]}`))
	}))
	defer empty.Close()
	adapter2, _ := NewOpenAI(OpenAIConfig{BaseURL: empty.URL, Model: "m", Timeout: time.Second})
	if _, err := adapter2.Generate(context.Background(), agentdomain.ChatRequest{}); err == nil {
		t.Fatal("expected error for empty choices")
	}
}
