package assembly

import (
	"context"
	"crypto/tls"
	"crypto/x509"
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

	intentroute "github.com/HappyLadySauceM/Nagi/cmd/app/routes/intent"
	"github.com/HappyLadySauceM/Nagi/cmd/app/svc"
	conversationdomain "github.com/HappyLadySauceM/Nagi/internal/conversation/domain"
	conversationinfra "github.com/HappyLadySauceM/Nagi/internal/conversation/infrastructure"
	intentapp "github.com/HappyLadySauceM/Nagi/internal/intent/application"
	intentcontracts "github.com/HappyLadySauceM/Nagi/internal/intent/contracts"
	intentdomain "github.com/HappyLadySauceM/Nagi/internal/intent/domain"
	intentinfra "github.com/HappyLadySauceM/Nagi/internal/intent/infrastructure"
	memorycontracts "github.com/HappyLadySauceM/Nagi/internal/memory/contracts"
	memorydomain "github.com/HappyLadySauceM/Nagi/internal/memory/domain"
	memoryinfra "github.com/HappyLadySauceM/Nagi/internal/memory/infrastructure"
	models "github.com/HappyLadySauceM/Nagi/internal/models/contracts"
	ob "github.com/HappyLadySauceM/Nagi/internal/onebot/domain"
	scopedomain "github.com/HappyLadySauceM/Nagi/internal/scope/domain"
	scopeinfra "github.com/HappyLadySauceM/Nagi/internal/scope/infrastructure"
	settingsapp "github.com/HappyLadySauceM/Nagi/internal/settings/application"
	settingscontracts "github.com/HappyLadySauceM/Nagi/internal/settings/contracts"
	settingsdomain "github.com/HappyLadySauceM/Nagi/internal/settings/domain"
	settingsinfra "github.com/HappyLadySauceM/Nagi/internal/settings/infrastructure"
	"github.com/HappyLadySauceM/Nagi/pkg/db/dbtest"
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

func TestIntentUnobservedDispatchDoesNotRepeatQuestion(t *testing.T) {
	var confirmed atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		confirmed.Add(1)
		_, _ = io.WriteString(w, backgroundOpenAIAnswer)
	}))
	defer server.Close()
	router, probe, in := newBackgroundFixture("openai", server.URL)
	var dials atomic.Int32
	router.transport.Dial = func(ctx context.Context, network, addr string) (net.Conn, error) {
		conn, err := (&net.Dialer{}).DialContext(ctx, network, addr)
		if err == nil && dials.Add(1) == 2 {
			return fixtureIntentWriteFailure{Conn: conn}, nil
		}
		return conn, err
	}
	_, dest, _ := router.resolve(context.Background())
	role, _, _ := probe.IntentRoleBackground(context.Background(), in)
	in.FixedBackground = true
	in.FrozenBackground = &intentdomain.BackgroundEvidence{Version: intentapp.BackgroundVersion, Destination: dest.Fingerprint, RoleHash: intentapp.RoleBackgroundHash(role), MemoryOutcome: "matched", Memories: []intentdomain.BackgroundMemoryRef{intentapp.IntentFactRef(probe.fact)}}
	out, err := router.Decide(context.Background(), in)
	if err != nil || out.BackgroundFailure != "semantic_dispatch_unknown" || out.ExternalRequests != 1 || out.UnknownRequests != 1 || dials.Load() != 2 || confirmed.Load() != 1 || len(out.Attempts) != 2 || !out.Attempts[0].ReservationHeld || !out.Attempts[1].ReservationHeld {
		t.Fatalf("confirmed=%d dials=%d result=%+v err=%v", confirmed.Load(), dials.Load(), out, err)
	}
	t.Logf("confirmed=%d total_unknown=true unresolved_reservations=%d", out.ExternalRequests, out.UnknownRequests)
}

func TestIntentBackgroundIgnoresUnapprovedHistory(t *testing.T) {
	for _, protocol := range []string{"jev", "openai"} {
		t.Run(protocol, func(t *testing.T) {
			var requests, distinctQueries atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				body, _ := io.ReadAll(r.Body)
				if !strings.Contains(string(body), "SYNTH_FACT") || strings.Contains(string(body), "HISTORY_VARIANT") {
					t.Error("dispatched background depended on unauthorized history")
				}
				if protocol == "jev" {
					_, _ = io.WriteString(w, `{"answers":{}}`)
					return
				}
				_, _ = io.WriteString(w, backgroundOpenAIAnswer)
			}))
			defer server.Close()
			router, fixture, in := newBackgroundFixture(protocol, server.URL)
			fixture.queryHook = func(query, topic string, subjects []string) {
				if query != "message" || topic != "" || len(subjects) != 0 {
					distinctQueries.Add(1)
				}
			}
			for _, marker := range []string{"HISTORY_VARIANT_A", "HISTORY_VARIANT_B"} {
				in.Recent = []intentdomain.Message{{Text: marker, SenderID: 75}}
				in.Topics = &intentdomain.TopicContext{QuotedID: 1, Reference: &intentdomain.Message{Text: marker}, Candidates: []intentdomain.TopicCandidate{{ID: 1, History: in.Recent}}}
				in.Task.Message.SenderID = 75
				out, _ := router.Decide(context.Background(), in)
				if out.BackgroundEvidence == nil || len(out.BackgroundEvidence.Memories) != 1 {
					t.Fatal("authorized group fact disappeared")
				}
			}
			want := int32(2)
			if protocol == "openai" {
				want = 10
			}
			if requests.Load() != want || distinctQueries.Load() != 0 {
				t.Fatalf("requests=%d history_in_query=%d", requests.Load(), distinctQueries.Load())
			}
			t.Logf("requests=%d history_in_query=0 history_in_prompt=0", requests.Load())
		})
	}
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

func TestIntentMemoryDurableRevocationBeforeFirstWrite(t *testing.T) {
	pool := dbtest.Open(t)
	repo, err := settingsinfra.NewRepo(pool)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if _, found, err := repo.Load(ctx); err != nil || found {
		t.Fatalf("fixture requires empty settings row: found=%v err=%v", found, err)
	}
	t.Cleanup(func() {
		if _, err := pool.Exec(ctx, `DELETE FROM system_settings WHERE id=1 AND updated_by='hap56_wire_fixture'`); err != nil {
			t.Error(err)
		}
	})
	const actor = "hap56_wire_fixture"
	for i, tc := range []struct {
		protocol string
		https    bool
	}{{"jev", false}, {"openai", false}, {"jev", true}, {"openai", true}} {
		protocol := tc.protocol
		t.Run(fmt.Sprintf("%s/https=%t", protocol, tc.https), func(t *testing.T) {
			var requests, leaked atomic.Int32
			server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
			if tc.https {
				server.StartTLS()
			} else {
				server.Start()
			}
			defer server.Close()
			router, probe, in := newBackgroundFixture(protocol, server.URL)
			profile := router.models.(backgroundResolver).profile
			profile.Usage = "intent"
			profile.VerifiedVersion, profile.VerifiedAt = profile.Version, time.Now()
			destination, err := settingsapp.IntentModelDestination(profile)
			if err != nil {
				t.Fatal(err)
			}
			settings := settingsdomain.Settings{}
			settings.Intent.Enabled = true
			settings.Models = models.Catalog{Initialized: true, Profiles: []models.Profile{profile}, Defaults: map[string]string{"intent": profile.ID}}
			var revision settingsdomain.Revision
			if i == 0 {
				revision, err = repo.Insert(ctx, settings, actor)
			} else {
				prior, found, loadErr := repo.Load(ctx)
				if loadErr != nil || !found {
					t.Fatalf("load: %v %v", found, loadErr)
				}
				var updated bool
				revision, updated, err = repo.Update(ctx, settings, prior.Version, actor)
				if err == nil && !updated {
					t.Fatal("settings CAS failed")
				}
			}
			if err != nil {
				t.Fatal(err)
			}
			newService := func() *settingsapp.Service {
				s, err := settingsapp.NewService(settingsapp.Options{Repository: repo, Provider: settingsapp.NewProvider(settingsapp.Snapshot{}), Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
				if err != nil {
					t.Fatal(err)
				}
				return s
			}
			reader, writer := newService(), newService()
			version, err := writer.SetIntentMemoryGrant(ctx, in.Task.BotID, in.Task.TargetID, true, destination.Fingerprint, revision.Version, actor)
			if err != nil {
				t.Fatal(err)
			}
			router.background = intentapp.IntentBackground{Roles: probe, Facts: probe, Scopes: probe, Grants: reader}
			connected := make(chan struct{})
			resume := make(chan struct{})
			var first atomic.Bool
			router.transport.Dial = func(ctx context.Context, network, addr string) (net.Conn, error) {
				conn, err := (&net.Dialer{}).DialContext(ctx, network, addr)
				if err != nil {
					return conn, err
				}
				if first.CompareAndSwap(false, true) {
					close(connected)
				}
				select {
				case <-resume:
					return conn, nil
				case <-ctx.Done():
					_ = conn.Close()
					return nil, ctx.Err()
				}
			}
			if tc.https {
				roots := x509.NewCertPool()
				roots.AddCert(server.Certificate())
				router.transport.Dial = (&net.Dialer{}).DialContext
				router.transport.TLSConfig = &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: roots, VerifyConnection: func(state tls.ConnectionState) error {
					if len(state.VerifiedChains) == 0 {
						return fmt.Errorf("fixture TLS peer is unverified")
					}
					if first.CompareAndSwap(false, true) {
						close(connected)
					}
					<-resume
					return nil
				}}
			}
			runCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
			defer cancel()
			result := make(chan intentdomain.SemanticAnswer, 1)
			go func() { answer, _ := router.Decide(runCtx, in); result <- answer }()
			select {
			case <-connected:
			case <-runCtx.Done():
				t.Fatal(runCtx.Err())
			}
			_, err = writer.SetIntentMemoryGrant(ctx, in.Task.BotID, in.Task.TargetID, false, "", version, actor)
			close(resume)
			if err != nil {
				t.Fatal(err)
			}
			var out intentdomain.SemanticAnswer
			select {
			case out = <-result:
			case <-runCtx.Done():
				t.Fatal(runCtx.Err())
			}
			if out.ExternalRequests != 0 || requests.Load() != 0 || leaked.Load() != 0 || out.UnknownRequests != 0 || out.BackgroundFailure != "background_disclosure_denied" {
				t.Fatalf("requests=%d old_memory=%d answer=%+v", requests.Load(), leaked.Load(), out)
			}
			t.Logf("committed_revocation=true https=%t requests=%d old_memory=%d unknown=%d", tc.https, requests.Load(), leaked.Load(), out.UnknownRequests)
		})
	}
}

func TestIntentLivePolicyRecheckedAfterFacts(t *testing.T) {
	for _, mode := range []string{"mention_only", "history_during_fact_read"} {
		t.Run(mode, func(t *testing.T) {
			var requests, reads atomic.Int32
			var probe *backgroundProbe
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if requests.Add(1) == 1 && mode == "mention_only" {
					changed := probe.policy
					changed.ReplyMode = scopedomain.ModeMentionOnly
					probe.currentPolicy.Store(&changed)
				}
				_, _ = io.WriteString(w, backgroundOpenAIAnswer)
			}))
			defer server.Close()
			router, fixture, in := newBackgroundFixture("openai", server.URL)
			probe = fixture
			probe.policy.ReplyMode = scopedomain.ModeIntentBased
			probe.policy.AllowHistoryRead = true
			in.Policy = probe.policy
			in.Recent = []intentdomain.Message{{Text: "SYNTH_HISTORY", MessageID: 3}}
			probe.readHook = func(_ context.Context, ids []string) error {
				if mode == "history_during_fact_read" && len(ids) > 0 && reads.Add(1) == 1 {
					changed := probe.policy
					changed.AllowHistoryRead = false
					probe.currentPolicy.Store(&changed)
				}
				return nil
			}
			answer, _ := router.Decide(context.Background(), in)
			want := "background_scope_denied"
			if mode != "mention_only" {
				want = "background_history_denied"
			}
			if requests.Load() > 2 || answer.ExternalRequests != int(requests.Load()) || answer.Available || answer.BackgroundFailure != want {
				t.Fatalf("requests=%d reason=%s answer=%+v", requests.Load(), want, answer)
			}
			t.Logf("requests=%d reason=%s", requests.Load(), want)
		})
	}
}

func TestFixedIntentRejectsWholeResponseViolations(t *testing.T) {
	for _, protocol := range []string{"jev", "openai"} {
		for _, failure := range []string{"tail-value", "tail-garbage", "over-limit"} {
			t.Run(protocol+"/"+failure, func(t *testing.T) {
				var requests atomic.Int32
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					requests.Add(1)
					response := backgroundOpenAIAnswer
					if protocol == "jev" {
						response = `{"answers":{},"usage":{"input_tokens":20}}`
					}
					switch failure {
					case "tail-value":
						response += " {}"
					case "tail-garbage":
						response += " !"
					case "over-limit":
						response += strings.Repeat(" ", 1<<20)
					}
					_, _ = io.WriteString(w, response)
				}))
				defer server.Close()
				router, probe, in := newBackgroundFixture(protocol, server.URL)
				_, dest, _ := router.resolve(context.Background())
				role, _, _ := probe.IntentRoleBackground(context.Background(), in)
				in.FixedBackground = true
				in.FrozenBackground = &intentdomain.BackgroundEvidence{Version: intentapp.BackgroundVersion, Destination: dest.Fingerprint, RoleHash: intentapp.RoleBackgroundHash(role), MemoryOutcome: "not_allowed"}
				out, err := router.Decide(context.Background(), in)
				if err == nil || requests.Load() < 1 || requests.Load() > 2 || out.ExternalRequests != int(requests.Load()) || len(out.Attempts) != out.ExternalRequests || out.Attempts[0].Outcome != "response_unusable" || out.Usage.InputResponses != 0 {
					t.Fatalf("requests=%d result=%+v err=%v", requests.Load(), out, err)
				}
				t.Log("one physical request, rejected whole response, no later dispatch")
			})
		}
	}
}

func TestConfirmedIntentReplayBudgetThroughHTTP(t *testing.T) {
	pool := dbtest.Open(t)
	ctx := context.Background()
	conversation, err := conversationinfra.NewRepo(pool)
	if err != nil {
		t.Fatal(err)
	}
	decisions, err := intentinfra.NewDecisionsRepo(pool)
	if err != nil {
		t.Fatal(err)
	}
	evaluations, err := intentinfra.NewReplayEvaluationsRepo(pool)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, wantReason, protocol       string
		cap, perRequest, wantAmount      int64
		confirmed, target, unknown, held int
	}{
		{name: "request-only", confirmed: 5, target: 5, held: 5},
		{name: "jev-one", protocol: "jev", confirmed: 1, target: 1, held: 1},
		{name: "revoke-two", confirmed: 2, target: 2, held: 2, wantReason: "background_disclosure_denied"},
		{name: "revoke-partial", confirmed: 2, target: 2, held: 2, wantReason: "background_disclosure_denied"},
		{name: "last-amount", cap: 4, perRequest: 4, confirmed: 1, target: 1, held: 1, wantReason: "semantic_replay_budget_exhausted"},
		{name: "amount-unverified", cap: 10, wantReason: "semantic_replay_price_unverified"},
		{name: "blocked", cap: 10, perRequest: 4, wantAmount: 10, wantReason: "background_disclosure_denied"},
		{name: "dial-failed", cap: 10, perRequest: 4, wantAmount: 10, wantReason: "semantic_response_unusable"},
		{name: "missing-answer", cap: 10, perRequest: 4, wantAmount: 2, confirmed: 2, target: 2, held: 2, wantReason: "semantic_response_unusable"},
		{name: "unknown-write", cap: 10, perRequest: 4, wantAmount: 2, confirmed: 1, target: 1, unknown: 1, held: 2, wantReason: "semantic_dispatch_unknown"},
		{name: "partial-write", cap: 10, perRequest: 4, wantAmount: 6, confirmed: 1, held: 1, wantReason: "semantic_response_unusable"},
		{name: "exhausted", cap: 10, perRequest: 4, wantAmount: 2, confirmed: 2, target: 2, held: 2, wantReason: "semantic_replay_budget_exhausted"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			protocol, limit := tc.protocol, 5
			if protocol == "" {
				protocol = "openai"
			} else {
				limit = 1
			}
			var target, dials, active, peak atomic.Int32
			entered := make(chan int32, 5)
			firstResponse := make(chan struct{})
			thirdDial := make(chan struct{})
			late := make(chan struct{})
			isRevoke := strings.HasPrefix(tc.name, "revoke-")
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				body, _ := io.ReadAll(req.Body)
				n := target.Add(1)
				current := active.Add(1)
				for prior := peak.Load(); current > prior && !peak.CompareAndSwap(prior, current); prior = peak.Load() {
				}
				defer active.Add(-1)
				entered <- n
				if isRevoke {
					wait := late
					if tc.name == "revoke-partial" && n == 1 {
						wait = firstResponse
					}
					select {
					case <-wait:
					case <-time.After(4 * time.Second):
						t.Error("loopback response was not released")
						return
					}
				}
				if tc.name == "request-only" || tc.name == "missing-answer" {
					if n == 1 && tc.name == "request-only" {
						select {
						case <-firstResponse:
						case <-time.After(4 * time.Second):
							t.Error("second request did not reach the loopback provider")
							return
						}
					}
					if n == 2 && tc.name == "request-only" {
						defer close(firstResponse)
					}
				}
				if tc.name == "missing-answer" {
					if n == 1 {
						select {
						case <-firstResponse:
						case <-time.After(4 * time.Second):
							t.Error("missing-answer second request did not arrive")
							return
						}
					} else if n == 2 {
						close(firstResponse)
					}
					_, _ = io.WriteString(w, `{"choices":[],"usage":{"prompt_tokens":0}}`)
					return
				}
				if protocol == "jev" {
					var request struct {
						Questions map[string]struct {
							Type     string
							Criteria map[string]string
						}
					}
					if err := json.Unmarshal(body, &request); err != nil {
						t.Error(err)
						return
					}
					answers := make(map[string]any, len(request.Questions))
					for key, question := range request.Questions {
						answer := map[string]any{"noul": .9}
						if question.Type == "choice" {
							for choice := range question.Criteria {
								answer = map[string]any{"choice": choice}
								break
							}
						}
						answers[key] = answer
					}
					_ = json.NewEncoder(w).Encode(map[string]any{"answers": answers, "usage": map[string]int{"prompt_tokens": 7, "completion_tokens": 2}})
					return
				}
				_, _ = io.WriteString(w, strings.Replace(backgroundOpenAIAnswer, `{"token":"B","logprob":0}`, `{"token":"B","logprob":0},{"token":"C","logprob":-4}`, 1))
			}))
			defer server.Close()
			router, probe, in := newBackgroundFixture(protocol, server.URL)
			probe.policy.ReplyMode = scopedomain.ModeIntentBased
			probe.policy.AllowHistoryRead = true
			router.transport.Dial = func(ctx context.Context, network, addr string) (net.Conn, error) {
				n := dials.Add(1)
				if tc.name == "revoke-partial" && n == 3 {
					close(thirdDial)
					<-ctx.Done()
					return nil, ctx.Err()
				}
				if tc.name == "dial-failed" {
					return nil, io.ErrClosedPipe
				}
				c, err := (&net.Dialer{}).DialContext(ctx, network, addr)
				if err != nil {
					return nil, err
				}
				if tc.name == "blocked" {
					probe.allowed.Store(false)
				}
				if tc.name == "unknown-write" && n == 2 {
					return fixtureIntentWriteFailure{Conn: c}, nil
				}
				if tc.name == "partial-write" {
					if n != 1 {
						<-ctx.Done()
						_ = c.Close()
						return nil, ctx.Err()
					}
					return fixtureIntentWriteFailure{Conn: c, partial: true}, nil
				}
				return c, nil
			}
			inserted, err := conversation.Ingest(ctx, conversationdomain.IngestInput{BotID: 1, Kind: conversationdomain.KindGroup, TargetID: 22, SenderID: 123, PlatformMessageID: time.Now().UnixNano(), Text: "fixture"})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if _, err := pool.Exec(ctx, `DELETE FROM message_jobs WHERE id=$1`, inserted.JobID); err != nil {
					t.Errorf("delete fixture job: %v", err)
				}
				if _, err := pool.Exec(ctx, `DELETE FROM messages WHERE id=$1`, inserted.MessageID); err != nil {
					t.Errorf("delete fixture message: %v", err)
				}
			})
			in.Task.JobID = inserted.JobID
			in.Task.ConversationID = inserted.ConversationID
			in.Policy = probe.policy
			role, _, _ := probe.IntentRoleBackground(ctx, in)
			evidence := intentdomain.BackgroundEvidence{Version: intentapp.BackgroundVersion, Destination: router.ReplayDestination(ctx), RoleHash: intentapp.RoleBackgroundHash(role), MemoryOutcome: "matched", Memories: []intentdomain.BackgroundMemoryRef{intentapp.IntentFactRef(probe.fact)}}
			snapshot := intentdomain.DecisionSnapshot{Version: intentdomain.SnapshotVersion, Input: intentdomain.SnapshotInput{Task: in.Task, BotName: "Fixture", Policy: in.Policy, Now: time.Now()}, Semantic: intentdomain.SemanticAnswer{BackgroundEvidence: &evidence}, Prompt: intentdomain.BuiltinPrompt(map[string]string{"openai": intentdomain.PromptProviderOpenAI, "jev": intentdomain.PromptProviderJev}[protocol]), Rule: intentdomain.RuleResult{NeedSemantic: true}}
			if err := decisions.Record(ctx, inserted.JobID, intentdomain.FinalDecision{Decision: intentdomain.DecisionSilent, Provider: protocol, Model: "fixture", Snapshot: &snapshot}, dbtest.Claim(t, pool, inserted.JobID)); err != nil {
				t.Fatal(err)
			}
			var id int64
			if err := pool.QueryRow(ctx, `SELECT id FROM intent_decisions WHERE job_id=$1`, inserted.JobID).Scan(&id); err != nil {
				t.Fatalf("fixture decision: %v", err)
			}
			d, err := intentapp.NewDecider(intentapp.Options{Scopes: probe, Recorder: decisions, Semantic: router, Provider: protocol, Model: "fixture", Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
			if err != nil {
				t.Fatal(err)
			}
			replay := intentapp.NewReplayService(d, decisions, decisions, evaluations)
			if tc.cap != 0 {
				replay.WithReplayBudgetPlanner(func(_ context.Context, p intentcontracts.ReplayParameters) intentcontracts.ReplayBudgetPlan {
					plan := p.Budget
					plan.AmountLimitMicros = &tc.cap
					plan.Currency = "CNY"
					plan.PriceVersion = "fixture-cap-v1"
					if tc.perRequest > 0 {
						plan.RequestUpperBoundMicros = &tc.perRequest
					}
					return plan
				})
			}
			mux := http.NewServeMux()
			intentroute.NewHandler(&svc.ServiceContext{IntentReplayer: replay}).Register(mux)
			path := fmt.Sprintf("/api/v1/intent/decisions/%d/replay-model", id)
			call := func(url, body string) *httptest.ResponseRecorder {
				w := httptest.NewRecorder()
				mux.ServeHTTP(w, httptest.NewRequest("POST", url, strings.NewReader(body)))
				return w
			}
			estimateResp := call(path+"/estimate", "")
			var estimate struct {
				Data intentcontracts.ReplayCostEstimate
			}
			if estimateResp.Code != 200 || json.Unmarshal(estimateResp.Body.Bytes(), &estimate) != nil {
				t.Fatalf("estimate: %d %s", estimateResp.Code, estimateResp.Body.String())
			}
			token := estimate.Data.ConfirmationToken
			if tc.name == "amount-unverified" {
				if token != "" || estimate.Data.ReasonCode != tc.wantReason || target.Load() != 0 || dials.Load() != 0 {
					t.Fatalf("preflight=%+v target=%d", estimate.Data, target.Load())
				}
				t.Log("unverified amount bound: provider=0 reservation=0")
				return
			}
			if token == "" || estimate.Data.Budget == nil || estimate.Data.Budget.RequestLimit != limit {
				t.Fatalf("estimate=%+v", estimate.Data)
			}
			body := fmt.Sprintf(`{"ConfirmationToken":%q}`, token)
			var result *httptest.ResponseRecorder
			if isRevoke {
				results := make(chan *httptest.ResponseRecorder, 1)
				go func() { results <- call(path, body) }()
				deadline := time.After(4 * time.Second)
				for range 2 {
					select {
					case <-entered:
					case <-deadline:
						close(late)
						t.Fatal("expected two in-flight requests")
					}
				}
				if tc.name == "revoke-partial" {
					close(firstResponse)
					select {
					case <-thirdDial:
					case <-deadline:
						close(late)
						t.Fatal("first response was not collected")
					}
				}
				revoked := time.Now()
				probe.allowed.Store(false)
				select {
				case result = <-results:
				case <-deadline:
					close(late)
					t.Fatal("replay did not settle after revocation")
				}
				close(late)
				t.Logf("revoked=%s terminal=%s late_response_released=%s target=%d peak=%d", revoked.Format(time.RFC3339Nano), time.Now().Format(time.RFC3339Nano), time.Now().Format(time.RFC3339Nano), target.Load(), peak.Load())
			} else {
				result = call(path, body)
			}
			var decoded struct {
				Data struct {
					Evaluation intentcontracts.ReplayEvaluation
				}
			}
			if result.Code != 200 || json.Unmarshal(result.Body.Bytes(), &decoded) != nil {
				t.Fatalf("replay: %d %s", result.Code, result.Body.String())
			}
			persisted, err := evaluations.Get(ctx, decoded.Data.Evaluation.ID)
			if err != nil {
				t.Fatal(err)
			}
			report := persisted.After.Replay
			if report == nil || report.Budget == nil || report.Budget.RemainingRequests != limit-tc.held || report.ExternalRequests != tc.confirmed || report.UnresolvedReservations != tc.unknown || report.ReasonCode != tc.wantReason || int(target.Load()) != tc.target {
				t.Fatalf("report=%+v target=%d dials=%d", report, target.Load(), dials.Load())
			}
			if tc.cap > 0 && (report.Budget.RemainingAmountMicros == nil || *report.Budget.RemainingAmountMicros != tc.wantAmount) {
				t.Fatalf("amount balance=%+v", report.Budget)
			}
			if tc.held > 0 && (persisted.Cost.Known || persisted.Cost.Amount != nil) {
				t.Fatalf("reservation appeared as actual cost: %+v", persisted.Cost)
			}
			held := 0
			for _, row := range report.Attempts {
				if row.Budget == nil || !row.Budget.RequestReserved {
					t.Fatalf("attempt without budget row: %+v", row)
				}
				if row.Budget.RequestHeld {
					held++
				}
			}
			if held != tc.held {
				t.Fatalf("held=%d expected=%d", held, tc.held)
			}
			if tc.name == "partial-write" && (len(report.Attempts) != 1 || !report.Attempts[0].PartialWrite) {
				t.Fatalf("partial evidence=%+v", report.Attempts)
			}
			if tc.name == "blocked" || tc.name == "dial-failed" {
				if len(report.Attempts) != 0 {
					t.Fatalf("release=%+v", report.Attempts)
				}
			}
			if isRevoke && (report.Status != "not_comparable" || peak.Load() != 2 || report.InputUsageResponses > 1 || (report.InputUsageResponses == 1) != (tc.name == "revoke-partial")) {
				t.Fatalf("revoked report=%+v peak=%d", report, peak.Load())
			}
			if tc.name == "request-only" && peak.Load() != 2 {
				t.Fatalf("expected two in flight, got %d", peak.Load())
			}
			repeated := call(path, body)
			if repeated.Code < 400 || int(target.Load()) != tc.target {
				t.Fatalf("repeated: %d target=%d", repeated.Code, target.Load())
			}
			again, err := evaluations.Get(ctx, persisted.ID)
			if err != nil || again.After.Replay.Budget.RemainingRequests != report.Budget.RemainingRequests {
				t.Fatalf("repeated mutated balance: %+v %v", again, err)
			}
			if tc.cap > 0 && (again.After.Replay.Budget.RemainingAmountMicros == nil || *again.After.Replay.Budget.RemainingAmountMicros != tc.wantAmount) {
				t.Fatalf("repeated mutated the amount balance: %+v", again.After.Replay.Budget)
			}
			var savedCount int
			if err := pool.QueryRow(ctx, `SELECT count(*) FROM intent_replay_evaluations WHERE decision_id=$1`, id).Scan(&savedCount); err != nil || savedCount != 1 {
				t.Fatalf("duplicate confirmation: saved evaluations=%d err=%v", savedCount, err)
			}
			t.Logf("target=%d confirmed=%d unresolved=%d held=%d remaining_requests=%d remaining_amount=%d duplicate_http=%d", tc.target, report.ExternalRequests, report.UnresolvedReservations, held, report.Budget.RemainingRequests, tc.wantAmount, repeated.Code)
		})
	}
}

func TestIntentAdaptersKeepQuotedTopicTermsBeforeLimit(t *testing.T) {
	pool := dbtest.Open(t)
	repo, err := memoryinfra.NewRepo(pool)
	if err != nil {
		t.Fatal(err)
	}
	conv, err := conversationinfra.NewRepo(pool)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	scopeRepo, err := scopeinfra.NewRepo(pool)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO bot_accounts(bot_id,nickname) VALUES(1,'intent-quoted-fixture') ON CONFLICT DO NOTHING`); err != nil {
		t.Fatal(err)
	}
	policy := scopedomain.Default(scopedomain.ScopeGroup, 22)
	policy.Enabled, policy.StoreMessages, policy.AllowMemoryRead, policy.AllowMemoryWrite = true, true, true, true
	policy.ReplyMode = "intent_based"
	if err := scopeRepo.Upsert(ctx, 1, policy); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := pool.Exec(ctx, `DELETE FROM scope_policies WHERE bot_id=1 AND scope_type='group' AND scope_id=22`); err != nil {
			t.Error(err)
		}
	})
	event, allowed, err := scopeRepo.ResolveMemoryAuthorization(ctx, 1, scopedomain.ScopeGroup, 22)
	if err != nil || !allowed || event == 0 {
		t.Fatalf("source authorization: %d %v %v", event, allowed, err)
	}
	ingested, err := conv.Ingest(ctx, conversationdomain.IngestInput{BotID: 1, Kind: conversationdomain.KindGroup, TargetID: 22, SenderID: 123, PlatformMessageID: time.Now().UnixNano(), Text: "我喜欢topicneedle SYNTH_TOPIC_FACT"})
	if err != nil {
		t.Fatal(err)
	}
	var sourceID int64
	if err := pool.QueryRow(ctx, `SELECT incoming_message_id FROM message_jobs WHERE id=$1`, ingested.JobID).Scan(&sourceID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE messages SET memory_authorization_event_id=$2,segments='[{"type":"text","data":{"text":"我喜欢topicneedle SYNTH_TOPIC_FACT"}}]'::jsonb WHERE id=$1`, sourceID, event); err != nil {
		t.Fatal(err)
	}
	base := memorydomain.Memory{BotID: 1, OwnerKey: "22", SubjectType: memorydomain.SubjectUser, SubjectID: "123", Visibility: memorydomain.VisibilityGroup, Status: memorydomain.StatusActive, ApprovedBy: "fixture", SourceMessageID: sourceID, Content: "本人自述：我喜欢topicneedle SYNTH_TOPIC_FACT"}
	t.Cleanup(func() {
		if _, err := pool.Exec(ctx, `DELETE FROM memories WHERE bot_id=1 AND fact_key IS NOT NULL AND source_message_id IN (SELECT id FROM messages WHERE conversation_id=$1 AND memory_authorization_event_id=$2)`, ingested.ConversationID, event); err != nil {
			t.Error(err)
		}
	})
	for i := 0; i < 10; i++ {
		row := base
		if i > 0 {
			text := fmt.Sprintf("我喜欢unrelated fixture %d", i)
			row.Confidence, row.Content = .9, "本人自述："+text
			if err := pool.QueryRow(ctx, `INSERT INTO messages(conversation_id,bot_id,direction,sender_id,text_content,segments,memory_authorization_event_id) VALUES($1,1,'incoming',123,$2,jsonb_build_array(jsonb_build_object('type','text','data',jsonb_build_object('text',$2::text))),$3) RETURNING id`, ingested.ConversationID, text, event).Scan(&row.SourceMessageID); err != nil {
				t.Fatal(err)
			}
		}
		fact, ok := memorydomain.FactFromContent(row.Content)
		if !ok {
			t.Fatalf("invalid fixture fact %q", row.Content)
		}
		row.FactKey = fact.Key(row, memorydomain.ExtractionScope{ConversationID: ingested.ConversationID, ScopeType: "group", ScopeID: 22, AuthorizationEventID: event})
		id, err := repo.Insert(ctx, row)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := pool.Exec(ctx, `UPDATE memories SET fact_key=$2 WHERE memory_id=$1`, id, row.FactKey); err != nil {
			t.Fatal(err)
		}
		if _, err := pool.Exec(ctx, `INSERT INTO memory_fact_sources(id,memory_id,message_id,authorization_event_id) VALUES(gen_random_uuid(),$1,$2,$3)`, id, row.SourceMessageID, event); err != nil {
			t.Fatal(err)
		}
	}
	for _, protocol := range []string{"jev", "openai"} {
		t.Run(protocol, func(t *testing.T) {
			var requests, hit atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				wire, _ := io.ReadAll(r.Body)
				requests.Add(1)
				if strings.Contains(string(wire), "SYNTH_TOPIC_FACT") {
					hit.Add(1)
				}
				if protocol == "jev" {
					_, _ = io.WriteString(w, `{"answers":{}}`)
					return
				}
				_, _ = io.WriteString(w, `{"choices":[{"logprobs":{"content":[{"token":"B","top_logprobs":[{"token":"A","logprob":-3},{"token":"B","logprob":0},{"token":"C","logprob":-3},{"token":"D","logprob":-3},{"token":"E","logprob":-3}]}]}}]}`)
			}))
			defer server.Close()
			router, probe, in := newBackgroundFixture(protocol, server.URL)
			router.background.Facts = repo
			in.Task.JobID = ingested.JobID
			in.Task.ConversationID = ingested.ConversationID
			in.Task.Message.SenderID = 123
			in.Task.Segments = []ob.Segment{{Type: ob.SegAt, Data: map[string]string{"qq": "123"}}}
			in.Task.Message.Text = "alpha beta gamma delta epsilon zeta theta kappa"
			probe.policy.AllowHistoryRead = true
			in.Policy = probe.policy
			in.Topics = &intentdomain.TopicContext{QuotedID: 3, Reference: &intentdomain.Message{Text: "topicneedle"}, Candidates: []intentdomain.TopicCandidate{{ID: 3}}}
			answer, _ := router.Decide(ctx, in)
			want := int32(1)
			if protocol == "openai" {
				want = 6
			}
			if requests.Load() != want || hit.Load() != want || answer.BackgroundEvidence == nil || len(answer.BackgroundEvidence.Memories) != 1 {
				t.Fatalf("requests=%d topic_hits=%d answer=%+v", requests.Load(), hit.Load(), answer)
			}
			t.Logf("current_terms=8 requests=%d older_topic_fact_in_wire=%d", requests.Load(), hit.Load())
		})
	}
}
