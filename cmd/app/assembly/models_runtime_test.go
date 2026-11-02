package assembly

import (
	"context"
	"encoding/json"
	"fmt"
	agent "github.com/HappyLadySauceM/Nagi/internal/agent/contracts"
	models "github.com/HappyLadySauceM/Nagi/internal/models/contracts"
	settings "github.com/HappyLadySauceM/Nagi/internal/settings/application"
	settingsdomain "github.com/HappyLadySauceM/Nagi/internal/settings/domain"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

type runtimeModels struct{ url string }

func (m runtimeModels) ResolveModel(_ context.Context, _ string, _ int64, _ string, target int64) (models.Resolved, error) {
	name := fmt.Sprintf("model-%d", target)
	return models.Resolved{Profile: models.Profile{ID: name, Model: name, Version: 1, BaseURL: m.url + "/v1", MaxOutputTokens: 100, TimeoutSeconds: 3}, APIKey: "test-key"}, nil
}
func (runtimeModels) BilibiliCredential(context.Context) (string, error) { return "", nil }

// TestModelRuntimeDoesNotSerializeConversations verifies routing and releases the cache lock before HTTP generation.
// TestModelRuntimeDoesNotSerializeConversations 验证模型路由并确保 HTTP 生成时已释放缓存锁。
func TestModelRuntimeDoesNotSerializeConversations(t *testing.T) {
	arrived := make(chan string, 2)
	release := make(chan struct{})
	defer close(release)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct{ Model string }
		if json.NewDecoder(r.Body).Decode(&req) != nil {
			t.Error("invalid model request")
		}
		arrived <- req.Model
		select {
		case <-release:
		case <-r.Context().Done():
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"id":"test","model":%q,"choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}]}`, req.Model)
	}))
	defer server.Close()
	provider := settings.NewProvider(settings.Snapshot{Settings: settingsdomain.Settings{AI: settingsdomain.AI{Enabled: true}, Agent: settingsdomain.Agent{MaxSteps: 2, MaxRunDurationSeconds: 5}}})
	runtime := newModelRuntime(provider, runtimeModels{server.URL}, nil, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	done := make(chan error, 2)
	for _, target := range []int64{1, 2} {
		go func(target int64) {
			_, err := runtime.Run(context.Background(), agent.Request{BotID: 1, Kind: "private", TargetID: target, Messages: []agent.Message{{Role: "user", Content: "hello"}}})
			done <- err
		}(target)
	}
	seen := map[string]bool{}
	for range 2 {
		select {
		case name := <-arrived:
			seen[name] = true
		case <-time.After(2 * time.Second):
			t.Fatal("model calls were serialized by the cache lock")
		}
	}
	if !seen["model-1"] || !seen["model-2"] {
		t.Fatal("conversation model routing lost")
	}
	// Both handlers have received their requests; cancellation completes them without sending external traffic.
	// 两个处理器已收到请求；取消测试请求即可结束，无需外部访问。
	server.CloseClientConnections()
	for range 2 {
		<-done
	}
	if len(runtime.runtimes) != 2 {
		t.Fatal("model versions were not independently cached")
	}
}

// TestMediaPermissionPreservesConfiguredRunTimeout prevents media grants from extending plain text work.
// TestMediaPermissionPreservesConfiguredRunTimeout 验证媒体授权不会隐式延长普通文字运行时限。
func TestMediaPermissionPreservesConfiguredRunTimeout(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-time.After(3 * time.Second):
			w.WriteHeader(500)
		}
	}))
	defer server.Close()
	provider := settings.NewProvider(settings.Snapshot{Settings: settingsdomain.Settings{AI: settingsdomain.AI{Enabled: true}, Agent: settingsdomain.Agent{MaxSteps: 2, MaxRunDurationSeconds: 1}}})
	runtime := newModelRuntime(provider, runtimeModels{server.URL}, nil, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	start := time.Now()
	_, err := runtime.Run(context.Background(), agent.Request{BotID: 1, Kind: "private", TargetID: 1, PermissionOverrides: map[string]bool{"media.video.read": true, "media.voice.send": true}, Messages: []agent.Message{{Role: "user", Content: "hello"}}})
	if err == nil || time.Since(start) > 2*time.Second {
		t.Fatalf("media authorization bypassed configured timeout: err=%v", err)
	}
}
