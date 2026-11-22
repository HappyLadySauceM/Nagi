package eino

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	pc "github.com/HappyLadySauceM/Nagi/internal/plugins/contracts"
	einoTools "github.com/HappyLadySauceM/Nagi/internal/plugins/infrastructure/eino"
	"github.com/cloudwego/eino/components/tool"

	"github.com/HappyLadySauceM/Nagi/internal/agent/contracts"
	audit "github.com/HappyLadySauceM/Nagi/internal/audit/contracts"
	"github.com/HappyLadySauceM/Nagi/internal/audit/domain"
	media "github.com/HappyLadySauceM/Nagi/internal/media/contracts"
)

// observedMedia stores only the two media-specific audit facts for the HTTP assertions.
type observedMedia struct {
	audit.AIRecorder
	mu      sync.Mutex
	stages  []string
	outputs []string
}

func (r *observedMedia) RecordEvent(_ context.Context, event domain.AIEvent) (domain.AIEvent, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	event.ID = event.OperationID
	r.stages = append(r.stages, event.Stage)
	return event, nil
}

func (r *observedMedia) FinishEvent(_ context.Context, _, _, _ string, output []byte) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.outputs = append(r.outputs, string(output))
	return nil
}

func TestMediaEvidenceMatchesSerializedGeneration(t *testing.T) {
	one, two := 1, 2
	const image = "aW1hZ2U="
	png := func(index *int) media.Part {
		return media.Part{PictureSegment: index, MIME: "image/png", Base64: image}
	}
	for _, c := range []struct {
		name       string
		inbound    []media.Part
		history    []media.Part
		allow      bool
		wireImages int
	}{
		{"two current pictures", []media.Part{png(&one), png(&two)}, nil, true, 2},
		{"missing picture", []media.Part{{Text: "fixture hint", PictureSegment: &one}}, nil, true, 0},
		{"unsupported current picture", []media.Part{png(&one)}, nil, false, 0},
		{"only historical picture", nil, []media.Part{png(&one)}, true, 1},
		{"plain text", nil, nil, true, 0},
	} {
		t.Run(c.name, func(t *testing.T) {
			type contentPart struct {
				Type     string `json:"type"`
				ImageURL struct {
					URL string `json:"url"`
				} `json:"image_url"`
			}
			got := make(chan int, 4)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var body struct {
					Stream   bool `json:"stream"`
					Messages []struct {
						Content json.RawMessage `json:"content"`
					} `json:"messages"`
				}
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Error(err)
					http.Error(w, "fixture", 400)
					return
				}
				n := 0
				for _, message := range body.Messages {
					if strings.Contains(string(message.Content), inboundPictureTag) {
						t.Error("internal segment metadata escaped")
					}
					var parts []contentPart
					if json.Unmarshal(message.Content, &parts) != nil {
						continue
					}
					for _, part := range parts {
						if part.Type == "image_url" && part.ImageURL.URL == "data:image/png;base64,"+image {
							n++
						}
					}
				}
				got <- n
				if body.Stream {
					w.Header().Set("Content-Type", "text/event-stream")
					_, _ = w.Write([]byte("data: {\"id\":\"fixture\",\"object\":\"chat.completion.chunk\",\"model\":\"fixture\",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"content\":\"fixture response\"},\"finish_reason\":null}]}\n\ndata: {\"id\":\"fixture\",\"object\":\"chat.completion.chunk\",\"model\":\"fixture\",\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n"))
					return
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"id":"fixture","object":"chat.completion","model":"fixture","choices":[{"index":0,"message":{"role":"assistant","content":"fixture response"},"finish_reason":"stop"}]}`))
			}))
			defer server.Close()
			recorder := &observedMedia{}
			ctx := audit.WithRun(context.Background(), recorder, "fixture")
			runtime, err := NewRuntime(ctx, Config{BaseURL: server.URL, Model: "fixture", Timeout: 3 * time.Second})
			if err != nil {
				t.Fatal(err)
			}
			req := testRequest()
			req.ModelImages = c.allow
			req.Messages = []contracts.Message{{Role: "user", Content: "fixture history", Parts: c.history}, {Role: "user", Content: "fixture current", CurrentInbound: true, Parts: c.inbound}}
			if _, err := runtime.Run(ctx, req); err != nil {
				t.Fatal(err)
			}
			if len(got) != 1 || <-got != c.wireImages {
				t.Fatal("unexpected count of wire requests or image parts")
			}
			if !reflect.DeepEqual(recorder.stages, []string{"generation.media_request"}) || len(recorder.outputs) != 1 {
				t.Fatalf("stages=%v outputs=%d", recorder.stages, len(recorder.outputs))
			}
			if strings.Contains(recorder.outputs[0], image) || strings.Contains(recorder.outputs[0], "fixture hint") {
				t.Fatal("media payload leaked into audit")
			}
			var report media.ModelMediaCall
			if err := json.Unmarshal([]byte(recorder.outputs[0]), &report); err != nil {
				t.Fatal(err)
			}
			if len(report.Attempts) != 1 || report.Attempts[0].State != media.MediaUnknown || report.Attempts[0].Shape != media.MediaUnknown || report.Attempts[0].HTTPStatus != http.StatusOK || len(report.Attempts[0].Images) != 0 {
				t.Fatalf("attempts=%+v", report.Attempts)
			}
		})
	}
}

func TestMediaEvidenceExcludesToolPictureInEachGenerationRound(t *testing.T) {
	var calls atomic.Int32
	const encodedPicture = "Zml4dHVyZS10b29sLWltYWdl"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Stream   bool
			Messages json.RawMessage
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
			http.Error(w, "fixture", 400)
			return
		}
		round := calls.Add(1)
		wire := strings.Contains(string(body.Messages), "data:image/png;base64,"+encodedPicture)
		if wire != (round == 2) {
			t.Errorf("tool image on round %d: %t", round, wire)
		}
		if body.Stream {
			t.Error("unexpected streaming request")
			http.Error(w, "fixture", 400)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if round == 1 {
			_, _ = fmt.Fprint(w, `{"id":"fixture","model":"fixture","object":"chat.completion","choices":[{"index":0,"message":{"role":"assistant","tool_calls":[{"id":"call_1","type":"function","function":{"name":"fixture_picture","arguments":"{}"}}]},"finish_reason":"tool_calls"}]}`)
			return
		}
		_, _ = fmt.Fprint(w, `{"id":"fixture","model":"fixture","object":"chat.completion","choices":[{"index":0,"message":{"role":"assistant","content":"fixture complete"},"finish_reason":"stop"}]}`)
	}))
	defer server.Close()
	recorder := &observedMedia{}
	ctx := audit.WithRun(context.Background(), recorder, "fixture")
	cfg := Config{BaseURL: server.URL, Model: "fixture", Timeout: 3 * time.Second, Tools: func(context.Context, contracts.Request) []tool.BaseTool {
		return einoTools.ToBaseTools([]pc.RegisteredTool{{Descriptor: pc.ToolDescriptor{ID: "fixture.picture", Description: "fixture picture", Multimodal: true}, Invoke: func(context.Context, string) (string, error) {
			b, err := json.Marshal(struct{ Parts []media.Part }{[]media.Part{{MIME: "image/png", Base64: encodedPicture}}})
			return string(b), err
		}}})
	}}
	runtime, err := NewRuntime(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	req := testRequest()
	req.ModelImages = true
	req.Messages[len(req.Messages)-1].CurrentInbound = true
	result, err := runtime.Run(ctx, req)
	if err != nil || result.Text != "fixture complete" || calls.Load() != 2 {
		t.Fatalf("reply=%q calls=%d err=%v", result.Text, calls.Load(), err)
	}
	if len(recorder.outputs) != 2 {
		t.Fatalf("audited attempts=%d", len(recorder.outputs))
	}
	for _, output := range recorder.outputs {
		if strings.Contains(output, encodedPicture) {
			t.Fatal("tool picture leaked into audit")
		}
		var report media.ModelMediaCall
		if err := json.Unmarshal([]byte(output), &report); err != nil {
			t.Fatal(err)
		}
		if len(report.Attempts) != 1 || report.Attempts[0].Shape != media.MediaUnknown || report.Attempts[0].State != media.MediaUnknown || len(report.Attempts[0].Images) != 0 {
			t.Fatalf("unexpected media report: %s", output)
		}
	}
}

func TestMediaEvidencePreservesGuardedGenerationTransport(t *testing.T) {
	for _, c := range []struct {
		name          string
		status        int
		stopListening bool
	}{
		{"server disconnect", 0, false},
		{"server disconnect and listener closes", 0, true},
		{"too many requests", http.StatusTooManyRequests, false},
		{"service unavailable", http.StatusServiceUnavailable, false},
	} {
		status := c.status
		t.Run(c.name, func(t *testing.T) {
			const picture = "aW1hZ2U="
			const warmup = "fixture connection warmup"
			type capture struct{ address, body string }
			wire := make(chan capture, 8)
			var requests atomic.Int32
			var server *httptest.Server
			server = httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, err := io.ReadAll(r.Body)
				if err != nil || !json.Valid(body) || r.Header.Get("Idempotency-Key") != "fixture" {
					t.Error("invalid fixture request")
					http.Error(w, "invalid request", 400)
					return
				}
				wire <- capture{r.RemoteAddr, string(body)}
				if requests.Add(1) == 2 {
					if status != 0 {
						w.Header().Set("Retry-After", "0")
						w.WriteHeader(status)
						_, _ = io.WriteString(w, `{"error":{"message":"fixture unavailable"}}`)
						return
					}
					conn, _, err := w.(http.Hijacker).Hijack()
					if err != nil {
						t.Errorf("hijack: %v", err)
						return
					}
					if c.stopListening {
						_ = server.Listener.Close()
					}
					_ = conn.Close()
					return
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, `{"id":"fixture","object":"chat.completion","model":"fixture","choices":[{"index":0,"message":{"role":"assistant","content":"fixture complete"},"finish_reason":"stop"}]}`)
			}))
			server.Start()
			defer server.Close()
			runtime, err := NewRuntime(context.Background(), Config{BaseURL: server.URL, Model: "fixture", Timeout: 3 * time.Second, HTTPHeaders: map[string]string{"Idempotency-Key": "fixture"}})
			if err != nil {
				t.Fatal(err)
			}
			req := testRequest()
			req.Messages[1].Content = warmup
			if _, err := runtime.Run(context.Background(), req); err != nil {
				t.Fatal(err)
			}
			segment := 3
			req.ModelImages = true
			req.Messages[1] = contracts.Message{Role: "user", CurrentInbound: true, Parts: []media.Part{{MIME: "image/png", Base64: picture, PictureSegment: &segment}}}
			recorder := &observedMedia{}
			result, err := runtime.Run(audit.WithRun(context.Background(), recorder, "fixture"), req)
			if err == nil || result.Text != "" {
				t.Fatalf("guarded transport result=%q err=%v", result.Text, err)
			}
			if len(wire) != 2 || len(recorder.outputs) != 1 {
				t.Fatalf("wire=%d audit_events=%d", len(wire), len(recorder.outputs))
			}
			first, target := <-wire, <-wire
			if !strings.Contains(first.body, warmup) || first.address == target.address || !strings.Contains(target.body, "data:image/png;base64,"+picture) || strings.Contains(target.body, inboundPictureTag) {
				t.Fatal("target did not use a fresh guarded connection with the current picture")
			}
			var report media.ModelMediaCall
			if err := json.Unmarshal([]byte(recorder.outputs[0]), &report); err != nil {
				t.Fatal(err)
			}
			if report.Outcome != media.MediaFailed || report.AttemptCountUnknown != (status == 0) || status == 0 && report.Reason != media.MediaUnattributed {
				t.Fatalf("unexpected call outcome: %+v", report)
			}
			if len(report.Attempts) != 1 || strings.Contains(recorder.outputs[0], picture) {
				t.Fatal("invalid attempt count or picture content leaked")
			}
			attempt := report.Attempts[0]
			if attempt.HTTPStatus != status || attempt.State != media.MediaUnknown || attempt.Shape != media.MediaUnknown || len(attempt.Images) != 0 {
				t.Fatalf("attempt: %+v", attempt)
			}
			t.Logf("status=%d actual_target_requests=1 fresh_connection=true segment=%d total_unknown=%t call=%s reason=%s", status, segment, report.AttemptCountUnknown, report.Outcome, report.Reason)
		})
	}
}
