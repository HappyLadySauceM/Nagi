// Tests for the optional Langfuse prompt client.
// 可选 Langfuse Prompt 客户端测试。
package infrastructure

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/HappyLadySauceM/Nagi/internal/intent/domain"
)

func TestLangfusePromptClientFetchValidatesAuthAndPayload(t *testing.T) {
	content := domain.PromptContentJSON(domain.BuiltinPrompt(domain.PromptProviderJev))
	text := string(content)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, pass, ok := r.BasicAuth()
		if !ok || user != "public" || pass != "secret" {
			t.Errorf("basic auth = %q/%q ok=%v", user, pass, ok)
		}
		if r.URL.Path != "/api/public/v2/prompts/intent.participation" {
			t.Errorf("path = %q", r.URL.Path)
		}
		if r.URL.Query().Get("version") != "3" {
			t.Errorf("version = %q", r.URL.Query().Get("version"))
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"name": "intent.participation", "version": 3, "type": "text", "prompt": text, "updatedAt": "2026-11-12T00:00:00Z",
		})
	}))
	defer server.Close()

	client := NewLangfusePromptClient(LangfusePromptConfig{BaseURL: server.URL, PublicKey: "public", SecretKey: "secret", Timeout: time.Second}, nil)
	if !client.Configured() {
		t.Fatal("client should be configured")
	}
	remote, err := client.Fetch(context.Background(), "intent.participation", 3, "")
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if remote.Name != "intent.participation" || remote.Version != 3 {
		t.Fatalf("remote = %+v", remote)
	}
	parsed, err := decodeRemotePromptContentForTest(domain.PromptProviderJev, remote.Prompt)
	if err != nil {
		t.Fatalf("parse remote prompt: %v", err)
	}
	if parsed.SystemOne == nil || len(parsed.SystemOne.Questions) == 0 {
		t.Fatalf("parsed prompt = %+v", parsed)
	}
}

func TestLangfusePromptClientMapsFailures(t *testing.T) {
	tests := []struct {
		name   string
		status int
		want   error
	}{
		{"not found", http.StatusNotFound, domain.ErrPromptRemoteNotFound},
		{"auth", http.StatusUnauthorized, domain.ErrPromptRemoteAuth},
		{"server", http.StatusInternalServerError, domain.ErrPromptRemoteUnreachable},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tc.status)
			}))
			defer server.Close()
			client := NewLangfusePromptClient(LangfusePromptConfig{BaseURL: server.URL, PublicKey: "p", SecretKey: "s", Timeout: time.Second}, nil)
			if _, err := client.Fetch(context.Background(), "intent.participation", 1, ""); !errors.Is(err, tc.want) {
				t.Fatalf("err = %v, want %v", err, tc.want)
			}
		})
	}
}

func TestLangfusePromptClientRejectsInvalidJSON(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"name":"intent.participation","version":0}`))
	}))
	defer server.Close()
	client := NewLangfusePromptClient(LangfusePromptConfig{BaseURL: server.URL, PublicKey: "p", SecretKey: "s", Timeout: time.Second}, nil)
	if _, err := client.Fetch(context.Background(), "intent.participation", 1, ""); !errors.Is(err, domain.ErrPromptRemoteInvalid) {
		t.Fatalf("err = %v, want invalid", err)
	}
}

func TestLangfusePromptClientTimesOut(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(200 * time.Millisecond)
	}))
	defer server.Close()
	client := NewLangfusePromptClient(LangfusePromptConfig{BaseURL: server.URL, PublicKey: "p", SecretKey: "s", Timeout: 20 * time.Millisecond}, nil)
	if _, err := client.Fetch(context.Background(), "intent.participation", 1, ""); !errors.Is(err, domain.ErrPromptRemoteTimeout) {
		t.Fatalf("err = %v, want timeout", err)
	}
}

func TestLangfusePromptClientUnconfiguredDoesNotCall(t *testing.T) {
	client := NewLangfusePromptClient(LangfusePromptConfig{}, nil)
	if client.Configured() {
		t.Fatal("empty config should be unconfigured")
	}
	if _, err := client.Fetch(context.Background(), "intent.participation", 1, ""); !errors.Is(err, domain.ErrPromptRemoteNotConfigured) {
		t.Fatalf("err = %v, want not configured", err)
	}
}

func decodeRemotePromptContentForTest(provider string, raw json.RawMessage) (domain.Prompt, error) {
	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		return domain.ParsePromptContent(provider, []byte(text))
	}
	return domain.ParsePromptContent(provider, raw)
}
