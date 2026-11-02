package infrastructure

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	models "github.com/HappyLadySauceM/Nagi/internal/models/contracts"
	"image/png"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestHTTPTesterExercisesDeclaredCapabilities checks actual upstream payloads rather than presence in a model list.
// TestHTTPTesterExercisesDeclaredCapabilities 验证真实接口载荷而不是模型列表是否存在。
func TestHTTPTesterExercisesDeclaredCapabilities(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Header.Get("Authorization") != "Bearer secret" {
			t.Error("missing authentication")
		}
		switch r.URL.Path {
		case "/chat/completions":
			var body map[string]any
			if json.NewDecoder(r.Body).Decode(&body) != nil {
				t.Error("bad chat payload")
			}
			if body["tools"] != nil {
				if body["reasoning_effort"] != "none" && body["tool_choice"] != "auto" {
					t.Error("thinking tools must use auto choice")
				}
				fmt.Fprint(w, `{"choices":[{"message":{"tool_calls":[{"function":{"name":"model_probe","arguments":"{}"}}]}}]}`)
			} else {
				fmt.Fprint(w, `{"choices":[{"message":{"content":"OK"}}]}`)
			}
		case "/audio/transcriptions":
			if err := r.ParseMultipartForm(1 << 20); err != nil {
				t.Error(err)
			}
			f, _, err := r.FormFile("file")
			if err != nil {
				t.Error("missing built-in audio")
			} else {
				f.Close()
			}
			fmt.Fprint(w, `{"text":"Hello"}`)
		case "/audio/speech":
			w.Write(probeMP3)
		default:
			w.WriteHeader(404)
		}
	}))
	defer server.Close()
	for _, usage := range []string{"generation", "asr", "tts"} {
		err := (HTTPTester{Client: server.Client()}).TestModel(context.Background(), models.Resolved{Profile: models.Profile{Usage: usage, BaseURL: server.URL, Model: "model", Thinking: true, ReasoningEffort: "high", Images: true, Tools: true, Voice: "voice"}, APIKey: "secret"})
		if err != nil {
			t.Fatal(usage, err)
		}
	}
	if calls != 5 {
		t.Fatalf("declared capabilities not probed: %d", calls)
	}
}
func TestHTTPTesterRedactsFailureAndHonorsCancellation(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(401)
		fmt.Fprint(w, "credential-secret signed-url")
	}))
	defer server.Close()
	m := models.Resolved{Profile: models.Profile{Usage: "generation", BaseURL: server.URL, Model: "test"}}
	err := (HTTPTester{Client: server.Client()}).TestModel(context.Background(), m)
	if err == nil || err.Error() != "文字测试失败：模型服务返回 HTTP 401" {
		t.Fatal("unsafe error", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err = (HTTPTester{}).TestModel(ctx, m); err == nil {
		t.Fatal("cancel ignored")
	}
}

// TestProbeImageDecodes guards against a corrupt image causing false model capability failures.
// TestProbeImageDecodes 防止损坏测试图导致错误判定模型能力。
func TestProbeImageDecodes(t *testing.T) {
	img, err := png.Decode(bytes.NewReader(probeImage))
	if err != nil || img.Bounds().Dx() < 32 || img.Bounds().Dy() < 32 {
		t.Fatal("invalid model probe image", err)
	}
}
