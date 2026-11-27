package assembly

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	intentapp "github.com/HappyLadySauceM/Nagi/internal/intent/application"
	intentdomain "github.com/HappyLadySauceM/Nagi/internal/intent/domain"
	memorycontracts "github.com/HappyLadySauceM/Nagi/internal/memory/contracts"
	models "github.com/HappyLadySauceM/Nagi/internal/models/contracts"
	ob "github.com/HappyLadySauceM/Nagi/internal/onebot/domain"
	scopedomain "github.com/HappyLadySauceM/Nagi/internal/scope/domain"
	settingsapp "github.com/HappyLadySauceM/Nagi/internal/settings/application"
	settingscontracts "github.com/HappyLadySauceM/Nagi/internal/settings/contracts"
)

type backgroundResolver struct{ profile models.Profile }

func (m backgroundResolver) ResolveModel(context.Context, string, int64, string, int64) (models.Resolved, error) {
	return models.Resolved{Profile: m.profile}, nil
}

type backgroundProbe struct {
	allowed       atomic.Bool
	reads         atomic.Int32
	readHook      func(context.Context, []string) error
	queryHook     func(string, string, []string)
	route         atomic.Pointer[settingscontracts.IntentDestination]
	policy        scopedomain.Policy
	currentPolicy atomic.Pointer[scopedomain.Policy]
	fact          memorycontracts.IntentFact
}

func (p *backgroundProbe) IntentRoleBackground(context.Context, intentdomain.Input) (*intentdomain.PromptIntentContext, []intentdomain.BackgroundRoleLayer, bool) {
	return &intentdomain.PromptIntentContext{Identity: "SYNTH_ROLE"}, nil, true
}
func (p *backgroundProbe) FrozenIntentRole(_ context.Context, _ intentdomain.Input, _ []intentdomain.BackgroundRoleLayer, digest string) (*intentdomain.PromptIntentContext, bool) {
	role := &intentdomain.PromptIntentContext{Identity: "SYNTH_ROLE"}
	return role, digest == intentapp.RoleBackgroundHash(role)
}
func (p *backgroundProbe) IntentFacts(ctx context.Context, _ int64, _ int64, _, query, topic string, subjects, ids []string) ([]memorycontracts.IntentFact, error) {
	p.reads.Add(1)
	if len(ids) == 0 && p.queryHook != nil {
		p.queryHook(query, topic, subjects)
	}
	if p.readHook != nil {
		if err := p.readHook(ctx, ids); err != nil {
			return nil, err
		}
	}
	return []memorycontracts.IntentFact{p.fact}, nil
}
func (p *backgroundProbe) MayDiscloseIntentMemory(_ context.Context, bot, group int64, dest settingscontracts.IntentDestination, endpoint string) bool {
	return bot == 1 && group == 22 && dest.Endpoint == endpoint && p.allowed.Load()
}
func (p *backgroundProbe) IntentMemoryRouteCurrent(_ context.Context, dest settingscontracts.IntentDestination) bool {
	active := p.route.Load()
	return active == nil || *active == dest
}
func (p *backgroundProbe) Resolve(context.Context, int64, scopedomain.ScopeType, int64) (scopedomain.Policy, error) {
	if policy := p.currentPolicy.Load(); policy != nil {
		return *policy, nil
	}
	return p.policy, nil
}

func TestSemanticBackgroundPhysicalRequests(t *testing.T) {
	for _, protocol := range []string{"jev", "openai"} {
		for _, history := range []bool{false, true} {
			for _, memory := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/history=%v/memory=%v", protocol, history, memory), func(t *testing.T) {
					probe := &backgroundProbe{policy: scopedomain.Policy{Enabled: true, AllowMemoryRead: memory, AllowHistoryRead: history}, fact: memorycontracts.IntentFact{ID: "fixture", SourceMessageID: 21, Content: "SYNTH_FACT", UpdatedAt: time.Now()}}
					probe.allowed.Store(true)
					var count atomic.Int32
					server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
						count.Add(1)
						body, err := io.ReadAll(req.Body)
						if err != nil {
							t.Error(err)
						}
						s := string(body)
						if !strings.Contains(s, "SYNTH_ROLE") || strings.Contains(s, "SYNTH_FACT") != memory || strings.Contains(s, "SYNTH_HISTORY") != history {
							t.Errorf("projection history=%v memory=%v: %s", history, memory, s)
						}
						if protocol == "jev" {
							_, _ = io.WriteString(w, `{"answers":{},"usage":{"prompt_tokens":7,"completion_tokens":2}}`)
							return
						}
						_, _ = io.WriteString(w, `{"choices":[{"logprobs":{"content":[{"token":"B","top_logprobs":[{"token":"A","logprob":-3},{"token":"B","logprob":-0.1}]}]}}],"usage":{"prompt_tokens":7,"completion_tokens":2}}`)
					}))
					defer server.Close()
					profile := models.Profile{ID: "fixture", Version: 1, Protocol: protocol, Model: "fixture", BaseURL: server.URL + "/intent", TimeoutSeconds: 5}
					router := newSemanticRouter(settingsapp.NewProvider(settingsapp.Snapshot{}), backgroundResolver{profile}, slog.New(slog.NewTextHandler(io.Discard, nil)))
					router.background = intentapp.IntentBackground{Roles: probe, Facts: probe, Grants: probe, Scopes: probe}
					in := intentdomain.Input{Task: intentdomain.Task{JobID: 1, BotID: 1, Kind: "group", TargetID: 22, ConversationID: "fixture", Message: intentdomain.Message{MessageID: 12, Text: "message"}}, Policy: probe.policy}
					if history {
						in.Recent = []intentdomain.Message{{MessageID: 11, Text: "SYNTH_HISTORY"}}
					}
					answer, _ := router.Decide(context.Background(), in)
					want := int32(1)
					if protocol == "openai" {
						want = 5
					}
					if count.Load() != want || answer.ExternalRequests != int(want) || answer.Usage.Responses != int64(want) {
						t.Fatalf("requests got=%d accounted=%d usage=%+v want=%d", count.Load(), answer.ExternalRequests, answer.Usage, want)
					}
					if (probe.reads.Load() != 0) != memory || answer.BackgroundEvidence == nil || (len(answer.BackgroundEvidence.Memories) != 0) != memory {
						t.Fatalf("reads=%d evidence=%+v", probe.reads.Load(), answer.BackgroundEvidence)
					}
				})
			}
		}
	}
}

func TestFixedIntentBackgroundStopsAfterRevocation(t *testing.T) {
	probe := &backgroundProbe{policy: scopedomain.Policy{Enabled: true, AllowMemoryRead: true}, fact: memorycontracts.IntentFact{ID: "fixture", SourceMessageID: 1, Content: "SYNTH_FACT", UpdatedAt: time.Now()}}
	var sent atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		sent.Add(1)
		probe.allowed.Store(false)
		_, _ = io.WriteString(w, `{"choices":[{"logprobs":{"content":[{"token":"B","top_logprobs":[{"token":"A","logprob":-3},{"token":"B","logprob":-0.1}]}]}}],"usage":{"prompt_tokens":7,"completion_tokens":2}}`)
	}))
	defer server.Close()
	profile := models.Profile{ID: "fixture", Version: 1, Protocol: "openai", Model: "fixture", BaseURL: server.URL + "/intent", TimeoutSeconds: 5}
	route, _ := settingsapp.IntentModelDestination(profile)
	router := newSemanticRouter(settingsapp.NewProvider(settingsapp.Snapshot{}), backgroundResolver{profile}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	router.background = intentapp.IntentBackground{Roles: probe, Facts: probe, Grants: probe, Scopes: probe}
	role, _, _ := probe.IntentRoleBackground(context.Background(), intentdomain.Input{})
	in := intentdomain.Input{Task: intentdomain.Task{BotID: 1, TargetID: 22, Kind: "group", ConversationID: "fixture"}, Policy: probe.policy, FixedBackground: true, FrozenBackground: &intentdomain.BackgroundEvidence{Version: intentapp.BackgroundVersion, Destination: route.Fingerprint, RoleHash: intentapp.RoleBackgroundHash(role), MemoryOutcome: "matched", Memories: []intentdomain.BackgroundMemoryRef{intentapp.IntentFactRef(probe.fact)}}}
	if got := router.CheckFrozenBackground(context.Background(), in); got != "background_disclosure_denied" {
		t.Fatalf("preflight without grant: %s", got)
	}
	probe.allowed.Store(true)
	if got := router.CheckFrozenBackground(context.Background(), in); got != "" {
		t.Fatalf("preflight with grant: %s", got)
	}
	result, err := router.Decide(context.Background(), in)
	if err != nil || result.Available || result.BackgroundFailure != "background_disclosure_denied" || result.ExternalRequests != int(sent.Load()) || sent.Load() < 1 || sent.Load() > 2 || result.Usage.Responses > int64(sent.Load()) {
		t.Fatalf("answer=%+v sent=%d err=%v", result, sent.Load(), err)
	}
}

func (backgroundResolver) BilibiliCredential(context.Context) (string, error) { return "", nil }

const backgroundOpenAIAnswer = `{"choices":[{"logprobs":{"content":[{"token":"B","top_logprobs":[{"token":"A","logprob":-3},{"token":"B","logprob":0}]}]}}],"usage":{"prompt_tokens":0,"completion_tokens":"unknown"}}`

func newBackgroundFixture(protocol, endpoint string) (*semanticRouter, *backgroundProbe, intentdomain.Input) {
	policy := scopedomain.Policy{Enabled: true, AllowMemoryRead: true}
	probe := &backgroundProbe{policy: policy, fact: memorycontracts.IntentFact{ID: "sample", SourceMessageID: 21, Content: "SYNTH_FACT", UpdatedAt: time.Now()}}
	probe.allowed.Store(true)
	profile := models.Profile{ID: "fixture", Version: 1, Protocol: protocol, Model: "fixture", BaseURL: endpoint, TimeoutSeconds: 8}
	router := newSemanticRouter(settingsapp.NewProvider(settingsapp.Snapshot{}), backgroundResolver{profile}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	router.background = intentapp.IntentBackground{Roles: probe, Facts: probe, Grants: probe, Scopes: probe}
	in := intentdomain.Input{Task: intentdomain.Task{JobID: 1, BotID: 1, Kind: "group", TargetID: 22, ConversationID: "fixture", Message: intentdomain.Message{MessageID: 12, Text: "message"}}, Policy: policy}
	return router, probe, in
}

func TestIntentMemoryRevokedDuringConnect(t *testing.T) {
	for _, protocol := range []string{"jev", "openai"} {
		t.Run(protocol, func(t *testing.T) {
			var requests, leaked atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				body, _ := io.ReadAll(r.Body)
				if strings.Contains(string(body), "SYNTH_FACT") {
					leaked.Add(1)
				}
				if protocol == "jev" {
					_, _ = io.WriteString(w, `{"answers":{}}`)
					return
				}
				_, _ = io.WriteString(w, backgroundOpenAIAnswer)
			}))
			defer server.Close()
			router, probe, in := newBackgroundFixture(protocol, server.URL)
			router.transport.Dial = func(ctx context.Context, network, addr string) (net.Conn, error) {
				conn, err := (&net.Dialer{}).DialContext(ctx, network, addr)
				if err == nil {
					probe.allowed.Store(false)
				}
				return conn, err
			}
			out, _ := router.Decide(context.Background(), in)
			if requests.Load() != 0 || leaked.Load() != 0 || out.ExternalRequests != 0 || out.UnknownRequests != 0 || out.BackgroundFailure != "background_disclosure_denied" {
				t.Fatalf("requests=%d old=%d counted=%d unknown=%d reason=%s", requests.Load(), leaked.Load(), out.ExternalRequests, out.UnknownRequests, out.BackgroundFailure)
			}
			t.Logf("requests=%d old_memory=%d counted=%d", requests.Load(), leaked.Load(), out.ExternalRequests)
		})
	}
}

func TestLiveIntentRevocationDiscardsInFlightScores(t *testing.T) {
	for _, partial := range []bool{false, true} {
		t.Run(fmt.Sprint(partial), func(t *testing.T) {
			var total atomic.Int32
			arrivals := make(chan int32, 5)
			respond := make(chan struct{})
			cancelled := make(chan struct{}, 5)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = io.Copy(io.Discard, r.Body)
				n := total.Add(1)
				arrivals <- n
				if partial && n == 1 {
					select {
					case <-respond:
						_, _ = io.WriteString(w, backgroundOpenAIAnswer)
					case <-r.Context().Done():
					}
					return
				}
				select {
				case <-r.Context().Done():
					cancelled <- struct{}{}
				case <-time.After(5 * time.Second):
					t.Error("loopback response was not cancelled")
				}
			}))
			defer server.Close()
			ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
			defer cancel()
			router, probe, in := newBackgroundFixture("openai", server.URL)
			firstPair := make(chan struct{})
			var dials atomic.Int32
			router.transport.Dial = func(ctx context.Context, network, addr string) (net.Conn, error) {
				if partial && dials.Add(1) == 3 {
					close(firstPair)
					<-ctx.Done()
					return nil, ctx.Err()
				}
				return (&net.Dialer{}).DialContext(ctx, network, addr)
			}
			result := make(chan intentdomain.SemanticAnswer, 1)
			go func() { out, _ := router.Decide(ctx, in); result <- out }()
			for range 2 {
				select {
				case <-arrivals:
				case <-ctx.Done():
					t.Fatal(ctx.Err())
				}
			}
			if partial {
				close(respond)
				select {
				case <-firstPair:
				case <-ctx.Done():
					t.Fatal(ctx.Err())
				}
			}
			probe.allowed.Store(false)
			select {
			case out := <-result:
				if out.Available || len(out.Scores) != 0 || out.ExternalRequests != 2 || out.UnknownRequests != 0 || total.Load() != 2 || out.BackgroundFailure != "background_disclosure_denied" || (out.Usage.InputResponses == 1) != partial {
					t.Fatalf("arrivals=%d result=%+v", total.Load(), out)
				}
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
			want := 2
			if partial {
				want = 1
			}
			for range want {
				select {
				case <-cancelled:
				case <-ctx.Done():
					t.Fatal(ctx.Err())
				}
			}
		})
	}
}

func TestFixedIntentBackgroundRequiresConsistentEvidence(t *testing.T) {
	for _, tc := range []struct {
		kind, outcome string
		known, valid  bool
	}{
		{"group", "matched", true, false}, {"group", "", true, false}, {"group", "no_match", true, true},
		{"private", "not_applicable", true, true}, {"private", "matched", true, false}, {"private", "", false, false},
	} {
		router, probe, in := newBackgroundFixture("openai", "http://127.0.0.1:1")
		in.Task.Kind = tc.kind
		in.FixedBackground = true
		_, dest, _ := router.resolve(context.Background())
		var role *intentdomain.PromptIntentContext
		if tc.kind == "group" {
			role, _, _ = probe.IntentRoleBackground(context.Background(), in)
		}
		if tc.known {
			in.FrozenBackground = &intentdomain.BackgroundEvidence{Version: intentapp.BackgroundVersion, Destination: dest.Fingerprint, RoleHash: intentapp.RoleBackgroundHash(role), MemoryOutcome: tc.outcome}
		}
		if reason := router.CheckFrozenBackground(context.Background(), in); (reason == "") != tc.valid {
			t.Errorf("%+v: %s", tc, reason)
		}
	}
}

type changingIntentRoute struct {
	backgroundResolver
	current atomic.Pointer[models.Profile]
}

func (r *changingIntentRoute) ResolveCurrentIntent(context.Context) (models.Resolved, error) {
	return models.Resolved{Profile: *r.current.Load()}, nil
}

func TestLiveIntentRemainingQuestionsUseCurrentRoute(t *testing.T) {
	for _, fixed := range []bool{false, true} {
		t.Run(fmt.Sprint(fixed), func(t *testing.T) {
			var oldCount, newCount atomic.Int32
			var probe *backgroundProbe
			var route *changingIntentRoute
			newServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				newCount.Add(1)
				body, _ := io.ReadAll(r.Body)
				if strings.Contains(string(body), "SYNTH_FACT") {
					t.Error("old fact disclosed to the new destination")
				}
				_, _ = io.WriteString(w, backgroundOpenAIAnswer)
			}))
			defer newServer.Close()
			oldServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				oldCount.Add(1)
				body, _ := io.ReadAll(r.Body)
				if !strings.Contains(string(body), "SYNTH_FACT") {
					t.Error("first request missing approved fact")
				}
				current := route.profile
				current.BaseURL = newServer.URL
				current.Version++
				changed, _ := settingsapp.IntentModelDestination(current)
				route.current.Store(&current)
				probe.route.Store(&changed)
				_, _ = io.WriteString(w, backgroundOpenAIAnswer)
			}))
			defer oldServer.Close()
			router, p, in := newBackgroundFixture("openai", oldServer.URL)
			probe = p
			route = &changingIntentRoute{backgroundResolver: router.models.(backgroundResolver)}
			route.current.Store(&route.profile)
			router.models = route
			if fixed {
				_, dest, _ := router.resolve(context.Background())
				role, _, _ := probe.IntentRoleBackground(context.Background(), in)
				in.FixedBackground = true
				in.FrozenBackground = &intentdomain.BackgroundEvidence{Version: intentapp.BackgroundVersion, Destination: dest.Fingerprint, RoleHash: intentapp.RoleBackgroundHash(role), MemoryOutcome: "matched", Memories: []intentdomain.BackgroundMemoryRef{intentapp.IntentFactRef(probe.fact)}}
			}
			out, err := router.Decide(context.Background(), in)
			if (err == nil) != fixed || oldCount.Load() < 1 || oldCount.Load() > 2 || newCount.Load() != 0 || out.ExternalRequests != int(oldCount.Load()) || out.Available || out.BackgroundFailure != "background_destination_changed" {
				t.Fatalf("old=%d new=%d out=%+v err=%v", oldCount.Load(), newCount.Load(), out, err)
			}
		})
	}
}

func TestIntentAdaptersDoNotProjectPrivateBackground(t *testing.T) {
	for _, protocol := range []string{"jev", "openai"} {
		t.Run(protocol, func(t *testing.T) {
			var count atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				count.Add(1)
				body, _ := io.ReadAll(r.Body)
				if strings.Contains(string(body), "SYNTH_ROLE") || strings.Contains(string(body), "SYNTH_FACT") {
					t.Error("private request disclosed group-only background")
				}
				if protocol == "jev" {
					_, _ = io.WriteString(w, `{"answers":{}}`)
					return
				}
				_, _ = io.WriteString(w, backgroundOpenAIAnswer)
			}))
			defer server.Close()
			router, probe, in := newBackgroundFixture(protocol, server.URL)
			in.Task.Kind = "private"
			out, _ := router.Decide(context.Background(), in)
			want := 1
			if protocol == "openai" {
				want = 5
			}
			if out.ExternalRequests != want || count.Load() != int32(want) || probe.reads.Load() != 0 || out.BackgroundEvidence.MemoryOutcome != "not_applicable" {
				t.Fatalf("requests=%d answer=%+v", count.Load(), out)
			}
			t.Logf("requests=%d memory_reads=0 private_background=empty", count.Load())
		})
	}
}

func TestFixedIntentMalformedProbabilityStopsDispatch(t *testing.T) {
	for _, value := range []string{"null", "missing", "1", "0"} {
		t.Run(value, func(t *testing.T) {
			var count atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = io.Copy(io.Discard, r.Body)
				count.Add(1)
				body := backgroundOpenAIAnswer
				if value == "missing" {
					body = strings.Replace(body, `,"logprob":0`, "", 1)
				} else {
					body = strings.Replace(body, `"logprob":0`, `"logprob":`+value, 1)
				}
				if !json.Valid([]byte(body)) {
					t.Fatal("invalid fixture")
				}
				_, _ = io.WriteString(w, body)
			}))
			defer server.Close()
			router, probe, in := newBackgroundFixture("openai", server.URL)
			_, dest, _ := router.resolve(context.Background())
			role, _, _ := probe.IntentRoleBackground(context.Background(), in)
			in.FixedBackground = true
			in.FrozenBackground = &intentdomain.BackgroundEvidence{Version: intentapp.BackgroundVersion, Destination: dest.Fingerprint, RoleHash: intentapp.RoleBackgroundHash(role), MemoryOutcome: "matched", Memories: []intentdomain.BackgroundMemoryRef{intentapp.IntentFactRef(probe.fact)}}
			out, err := router.Decide(context.Background(), in)
			minRequests, maxRequests := 1, 2
			if value == "0" {
				minRequests, maxRequests = 5, 5
			}
			want := int(count.Load())
			if out.Available != (value == "0") || (err == nil) != (value == "0") || out.Usage.InputResponses < 1 || out.Usage.InputResponses > int64(want) || out.Usage.OutputResponses != 0 || out.ExternalRequests < minRequests || out.ExternalRequests > maxRequests || want < minRequests || want > out.ExternalRequests {
				t.Fatalf("value=%s requests=%d answer=%+v err=%v", value, count.Load(), out, err)
			}
			t.Logf("value=%s requests=%d verified_input_usage=%d verified_output_usage=%d", value, out.ExternalRequests, out.Usage.InputResponses, out.Usage.OutputResponses)
		})
	}
}

func TestIntentBackgroundRedirectLeavesTargetUntouched(t *testing.T) {
	for _, protocol := range []string{"jev", "openai"} {
		t.Run(protocol, func(t *testing.T) {
			var source, target atomic.Int32
			receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { target.Add(1) }))
			defer receiver.Close()
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				source.Add(1)
				body, _ := io.ReadAll(r.Body)
				if !strings.Contains(string(body), "SYNTH_FACT") {
					t.Error("approved facts missing from initial dispatch")
				}
				http.Redirect(w, r, receiver.URL, http.StatusTemporaryRedirect)
			}))
			defer server.Close()
			router, _, in := newBackgroundFixture(protocol, server.URL)
			answer, err := router.Decide(context.Background(), in)
			if err == nil || source.Load() < 1 || source.Load() > 2 || target.Load() != 0 || answer.ExternalRequests != int(source.Load()) {
				t.Fatalf("source=%d target=%d accounted=%d err=%v", source.Load(), target.Load(), answer.ExternalRequests, err)
			}
			t.Logf("source=%d redirect_target=%d", source.Load(), target.Load())
		})
	}
}

type fixtureIntentWriteFailure struct {
	net.Conn
	partial bool
}

func (c fixtureIntentWriteFailure) Write(p []byte) (int, error) {
	if c.partial && len(p) > 1 {
		n, err := c.Conn.Write(p[:1])
		if err != nil {
			return n, err
		}
		return n, io.ErrShortWrite
	}
	return 0, io.ErrUnexpectedEOF
}

func TestIntentFactSubjectRequiresExplicitIdentity(t *testing.T) {
	task := intentdomain.Task{BotID: 1, Message: intentdomain.Message{SenderID: 22, Text: "同名阿白是什么称呼"}}
	if got := intentapp.IntentFactSubjects(task); len(got) != 0 {
		t.Fatalf("name ambiguity inferred subject: %v", got)
	}
	task.Segments = []ob.Segment{{Type: ob.SegAt, Data: map[string]string{"qq": "33"}}}
	if got := intentapp.IntentFactSubjects(task); len(got) != 1 || got[0] != "33" {
		t.Fatalf("structured subject unavailable: %v", got)
	}
	task.Segments = nil
	task.Message.Text = "请称呼我阿白"
	if got := intentapp.IntentFactSubjects(task); len(got) != 1 || got[0] != "22" {
		t.Fatalf("self reference unavailable: %v", got)
	}
}
