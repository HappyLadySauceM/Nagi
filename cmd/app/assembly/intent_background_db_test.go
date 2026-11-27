//go:build integration

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
	memorydomain "github.com/HappyLadySauceM/Nagi/internal/memory/domain"
	memoryinfra "github.com/HappyLadySauceM/Nagi/internal/memory/infrastructure"
	models "github.com/HappyLadySauceM/Nagi/internal/models/contracts"
	ob "github.com/HappyLadySauceM/Nagi/internal/onebot/domain"
	scopedomain "github.com/HappyLadySauceM/Nagi/internal/scope/domain"
	scopeinfra "github.com/HappyLadySauceM/Nagi/internal/scope/infrastructure"
	settingsapp "github.com/HappyLadySauceM/Nagi/internal/settings/application"
	settingsdomain "github.com/HappyLadySauceM/Nagi/internal/settings/domain"
	settingsinfra "github.com/HappyLadySauceM/Nagi/internal/settings/infrastructure"
	"github.com/HappyLadySauceM/Nagi/pkg/db/dbtest"
)

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
				if err == nil || requests.Load() < 1 || requests.Load() > 2 ||
					out.ExternalRequests < 1 || out.ExternalRequests > 2 ||
					len(out.Attempts) != out.ExternalRequests || out.Usage.InputResponses != 0 {
					t.Fatalf("requests=%d result=%+v err=%v", requests.Load(), out, err)
				}
				for i := range out.Attempts {
					if out.Attempts[i].Outcome != "response_unusable" {
						t.Fatalf("attempt[%d]=%+v requests=%d result=%+v err=%v", i, out.Attempts[i], requests.Load(), out, err)
					}
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
