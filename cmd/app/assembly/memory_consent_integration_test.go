package assembly

import (
	"context"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/http/httptrace"
	"os"
	execpkg "os/exec"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/HappyLadySauceM/Nagi/cmd/app/router"
	"github.com/HappyLadySauceM/Nagi/cmd/app/svc"
	agentapp "github.com/HappyLadySauceM/Nagi/internal/agent/application"
	agentcontracts "github.com/HappyLadySauceM/Nagi/internal/agent/contracts"
	agentinfra "github.com/HappyLadySauceM/Nagi/internal/agent/infrastructure/eino"
	replytool "github.com/HappyLadySauceM/Nagi/internal/agent/infrastructure/replytool"
	conversationapp "github.com/HappyLadySauceM/Nagi/internal/conversation/application"
	conversationdomain "github.com/HappyLadySauceM/Nagi/internal/conversation/domain"
	conversationinfra "github.com/HappyLadySauceM/Nagi/internal/conversation/infrastructure"
	intentapp "github.com/HappyLadySauceM/Nagi/internal/intent/application"
	intentdomain "github.com/HappyLadySauceM/Nagi/internal/intent/domain"
	intentinfra "github.com/HappyLadySauceM/Nagi/internal/intent/infrastructure"
	mediainfra "github.com/HappyLadySauceM/Nagi/internal/media/infrastructure"
	memoryapp "github.com/HappyLadySauceM/Nagi/internal/memory/application"
	memorycontracts "github.com/HappyLadySauceM/Nagi/internal/memory/contracts"
	memorydomain "github.com/HappyLadySauceM/Nagi/internal/memory/domain"
	memoryinfra "github.com/HappyLadySauceM/Nagi/internal/memory/infrastructure"
	modelcontracts "github.com/HappyLadySauceM/Nagi/internal/models/contracts"
	obapp "github.com/HappyLadySauceM/Nagi/internal/onebot/application"
	onebotcontracts "github.com/HappyLadySauceM/Nagi/internal/onebot/contracts"
	obdomain "github.com/HappyLadySauceM/Nagi/internal/onebot/domain"
	outboundapp "github.com/HappyLadySauceM/Nagi/internal/outbound/application"
	outbounddomain "github.com/HappyLadySauceM/Nagi/internal/outbound/domain"
	outboundinfra "github.com/HappyLadySauceM/Nagi/internal/outbound/infrastructure"
	pluginsapp "github.com/HappyLadySauceM/Nagi/internal/plugins/application"
	plugincontracts "github.com/HappyLadySauceM/Nagi/internal/plugins/contracts"
	"github.com/HappyLadySauceM/Nagi/internal/plugins/infrastructure/builtin"
	plugineino "github.com/HappyLadySauceM/Nagi/internal/plugins/infrastructure/eino"
	scopeapp "github.com/HappyLadySauceM/Nagi/internal/scope/application"
	scopedomain "github.com/HappyLadySauceM/Nagi/internal/scope/domain"
	scopeinfra "github.com/HappyLadySauceM/Nagi/internal/scope/infrastructure"
	settingsapp "github.com/HappyLadySauceM/Nagi/internal/settings/application"
	settingsdomain "github.com/HappyLadySauceM/Nagi/internal/settings/domain"
	"github.com/HappyLadySauceM/Nagi/pkg/config"
	"github.com/HappyLadySauceM/Nagi/pkg/db"
	"github.com/HappyLadySauceM/Nagi/pkg/db/dbtest"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/schema"
	"go.opentelemetry.io/otel"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

type consentWire struct {
	client  *obapp.ClientService
	actions []obdomain.Action
	async   bool
}

func (w *consentWire) Connected() bool { return true }
func (w *consentWire) Close() error    { return nil }
func (w *consentWire) Send(_ context.Context, action obdomain.Action, echo string) error {
	w.actions = append(w.actions, action)
	status, code := "ok", 0
	if w.async {
		status, code = "async", 1
	}
	wire, _ := json.Marshal(map[string]any{"status": status, "retcode": code, "echo": echo, "data": map[string]any{"message_id": -100 - len(w.actions)}})
	w.client.HandleResponse(echo, wire)
	return nil
}

type consentFixtureRuntime struct{ agentcontracts.Runtime }

func (r consentFixtureRuntime) Run(ctx context.Context, req agentcontracts.Request) (agentcontracts.Result, error) {
	return r.Runtime.Run(memorycontracts.WithConsentDestination(ctx, func(ctx context.Context, _ memorycontracts.GenerationDispatch) error {
		return memorycontracts.ValidateRunReads(ctx)
	}), req)
}

type consentWaitingLedger struct {
	*outboundapp.Coordinator
	reserved chan outbounddomain.Record
}

func (l consentWaitingLedger) Reserve(ctx context.Context, req outboundapp.Request) (outbounddomain.Record, error) {
	record, err := l.Coordinator.Reserve(ctx, req)
	if err == nil {
		l.reserved <- record
	}
	return record, err
}

type consentInterruptLedger struct {
	*outboundapp.Coordinator
	interrupt error
	record    *outbounddomain.Record
}

func (l consentInterruptLedger) Reserve(ctx context.Context, req outboundapp.Request) (outbounddomain.Record, error) {
	record, err := l.Coordinator.Reserve(ctx, req)
	if err != nil {
		return record, err
	}
	*l.record = record
	return outbounddomain.Record{}, l.interrupt
}

func TestHAP52BConsentEntryAndDisclosure(t *testing.T) {
	ctx := context.Background()
	pool := dbtest.Open(t)
	dsn := os.Getenv("NAGI_TEST_DSN")
	if !strings.Contains(dsn, "_test?") {
		t.Skip("requires explicit isolated test DSN for second pool")
	}
	second, err := db.Connect(ctx, dsn, 4)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	bot := time.Now().UnixMicro() // Keep the fixture account ID exact in the browser.
	const person, other int64 = 972521, 972522
	const groupA, groupB, groupC, groupD int64 = 972523, 972524, 972525, 972526
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	memoryRepo, _ := memoryinfra.NewRepo(pool)
	memory2, _ := memoryinfra.NewRepo(second)
	members, _ := scopeinfra.NewRepo(pool)
	members2, _ := scopeinfra.NewRepo(second)
	scopes, _ := scopeapp.NewService(members)
	scopes2, _ := scopeapp.NewService(members2)
	mem, _ := memoryapp.NewService(memoryRepo)
	mem2, _ := memoryapp.NewService(memory2)
	mem.SetReadPolicies(scopes)
	mem2.SetReadPolicies(scopes2)
	mem.SetCrossGroupConsentRead(true)
	mem2.SetCrossGroupConsentRead(true)
	convRepo, _ := conversationinfra.NewRepo(pool, conversationinfra.WithConsentIngress(memoryRepo, true))
	conv2, _ := conversationinfra.NewRepo(second, conversationinfra.WithConsentIngress(memory2, true))
	convDisabled, _ := conversationinfra.NewRepo(second, conversationinfra.WithConsentIngress(memory2, false))
	conversation, _ := conversationapp.NewService(convRepo, logger)
	conversation2, _ := conversationapp.NewService(conv2, logger)
	conversationDisabled, _ := conversationapp.NewService(convDisabled, logger)
	firstIngress, _ := conversationinfra.NewIngress(conversation, scopes, nil, logger)
	secondIngress, _ := conversationinfra.NewIngress(conversation2, scopes2, nil, logger)
	disabledIngress, _ := conversationinfra.NewIngress(conversationDisabled, scopes2, nil, logger)
	outbound, _ := outboundinfra.NewRepo(pool)
	outbound2, _ := outboundinfra.NewRepo(second)
	gate := memoryinfra.ConsentSendGate{Enabled: true, ReadEnabled: true}
	outbound.SetConsentGate(gate)
	outbound2.SetConsentGate(gate)
	ledger, _ := outboundapp.NewCoordinator(outbound)
	ledger2, _ := outboundapp.NewCoordinator(outbound2)
	policies, _ := outboundinfra.NewPoliciesRepo(pool)
	accounts := svc.NewAccounts()
	wire := &consentWire{}
	wire.client = obapp.NewClientService(wire, 15*time.Second, logger)
	if err = wire.client.BindIdentity(obdomain.BotID(bot)); err != nil {
		t.Fatal(err)
	}
	if err = accounts.Add(svc.NewAccountConnection("fixture", bot, wire.client)); err != nil {
		t.Fatal(err)
	}
	settings := settingsapp.NewProvider(settingsapp.Snapshot{Settings: settingsdomain.Settings{}})
	processor := consentPromptProcessor(true, memoryRepo, accounts, scopes, ledger, policies, settings)
	cfg := &config.Config{}
	cfg.Server.AdminToken = "isolated-fixture"
	cfg.Memory.CrossGroupConsentWrite = true
	web := httptest.NewServer(router.New(&svc.ServiceContext{Config: cfg, Accounts: accounts, Memory: mem, SettingsProvider: settings}, logger))
	defer web.Close()
	api := func(method, path, body string, auth bool, want int) memorycontracts.ConsentProposal {
		t.Helper()
		req, err := http.NewRequestWithContext(ctx, method, web.URL+path+"?bot_id="+strconv.FormatInt(bot, 10), strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		if auth {
			req.Header.Set("Authorization", "Bearer isolated-fixture")
		}
		response, err := web.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		raw, err := io.ReadAll(response.Body)
		if err != nil || response.StatusCode != want {
			t.Fatalf("%s %s: %d %s %v", method, path, response.StatusCode, raw, err)
		}
		var out struct {
			Data memorycontracts.ConsentProposal
		}
		if want == http.StatusOK && json.Unmarshal(raw, &out) != nil {
			t.Fatalf("bad envelope: %s", raw)
		}
		return out.Data
	}
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := pool.Exec(ctx, q, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`INSERT INTO bot_accounts(bot_id) VALUES($1) ON CONFLICT DO NOTHING`, bot)
	for _, group := range []int64{groupA, groupB, groupC, groupD} {
		p := scopedomain.Default(scopedomain.ScopeGroup, group)
		p.Enabled, p.StoreMessages, p.AllowMemoryRead = true, true, true
		p.ReplyMode = scopedomain.ModeAlwaysReply
		p.MinReplyIntervalMS = 1
		p.AllowHistoryRead = true
		p.MessageIntervalMS = 5
		if err = members.Upsert(ctx, bot, p); err != nil {
			t.Fatal(err)
		}
		exec(`INSERT INTO qq_groups(bot_id,group_id) VALUES($1,$2)`, bot, group)
	}
	const fact = "霁蓝称呼是这位成员明确给出的称呼"
	original, err := conversation.Ingest(ctx, conversationdomain.IngestInput{BotID: bot, Kind: conversationdomain.KindGroup, TargetID: groupA, PlatformMessageID: -77, SenderID: person, Text: fact, ArchiveOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	id, err := memoryRepo.Insert(ctx, memorydomain.Memory{BotID: bot, Visibility: memorydomain.VisibilityGroup, OwnerKey: fmt.Sprint(groupA), SourceMessageID: original.MessageID, SourceSenderID: person, SubjectType: memorydomain.SubjectUser, SubjectID: fmt.Sprint(person), Content: fact, Source: "original-only", Status: memorydomain.StatusActive, ApprovedBy: "fixture", Confidence: 0.9})
	if err != nil {
		t.Fatal(err)
	}
	createPath := "/api/v1/memory-consents"
	body := func(key string, version, b, c int64) string {
		return fmt.Sprintf("{\"memory_id\":%q,\"expected_fact_version\":%d,\"target_group_ids\":[%d,%d],\"idempotency_key\":%q}", id, version, b, c, key)
	}
	create := func(key string, version, b, c int64) memorycontracts.ConsentProposal {
		return api(http.MethodPost, createPath, body(key, version, b, c), true, 200)
	}
	read := func(p memorycontracts.ConsentProposal) memorycontracts.ConsentProposal {
		return api(http.MethodGet, createPath+"/"+p.ID, "", true, 200)
	}
	api(http.MethodPost, createPath, body("auth-missing", 1, groupB, groupD), false, 401)
	api(http.MethodPost, createPath, body("wrong-version", 2, groupB, groupD), true, 409)
	api(http.MethodPost, createPath, body("unknown-group", 1, groupB, 42), true, 400)
	api(http.MethodPost, createPath, body("source-as-target", 1, groupA, groupB), true, 400)
	exec(`UPDATE scope_policies SET max_chars_per_message=25 WHERE bot_id=$1 AND scope_id=$2`, bot, groupA)
	api(http.MethodPost, createPath, body("too-long", 1, groupB, groupD), true, 400)
	exec(`UPDATE scope_policies SET max_chars_per_message=500 WHERE bot_id=$1 AND scope_id=$2`, bot, groupA)
	first := create("first", 1, groupD, groupB)
	if got := create("first", 1, groupB, groupD); got.ID != first.ID {
		t.Fatalf("idempotency produced %s, want %s", got.ID, first.ID)
	}
	api(http.MethodPost, createPath, body("first", 1, groupB, groupC), true, 409)
	var count int
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM message_jobs WHERE consent_proposal_id=$1 AND incoming_message_id IS NULL`, first.ID).Scan(&count); err != nil || count != 1 {
		t.Fatalf("jobs=%d, %v", count, err)
	}
	claimOne := func(repo *conversationinfra.Repo, proposal string) conversationdomain.Job {
		t.Helper()
		jobs, err := repo.ClaimReadyJobs(ctx, 30)
		if err != nil {
			t.Fatal(err)
		}
		for _, job := range jobs {
			if job.BotID == bot && job.ConsentProposalID == proposal {
				return job
			}
		}
		t.Fatalf("missing proposal job %s", proposal)
		return conversationdomain.Job{}
	}
	finish := func(job conversationdomain.Job) {
		t.Helper()
		if err := convRepo.CompleteJob(ctx, job.ID, job.ClaimToken); err != nil {
			t.Fatal(err)
		}
	}
	var jobs []conversationdomain.Job
	var mu sync.Mutex
	var wg sync.WaitGroup
	for _, repo := range []*conversationinfra.Repo{convRepo, conv2} {
		wg.Add(1)
		go func(repo *conversationinfra.Repo) {
			defer wg.Done()
			got, err := repo.ClaimReadyJobs(ctx, 30)
			if err != nil {
				t.Error(err)
			}
			mu.Lock()
			defer mu.Unlock()
			for _, job := range got {
				if job.ConsentProposalID == first.ID {
					jobs = append(jobs, job)
				}
			}
		}(repo)
	}
	wg.Wait()
	if len(jobs) != 1 || jobs[0].MessageID != 0 {
		t.Fatalf("claimed control jobs=%+v", jobs)
	}
	firstJob := jobs[0]
	deliver := func(job conversationdomain.Job) {
		t.Helper()
		if err := processor(ctx, job); err != nil {
			t.Fatal(err)
		}
		finish(job)
	}
	deliver(firstJob)
	first = read(first)
	if len(wire.actions) != 1 || first.DeliveryStatus != "sent" || first.PlatformMessageID != -101 || len(first.ActiveGroups) != 0 {
		t.Fatalf("delivery=%+v calls=%d", first, len(wire.actions))
	}
	action := wire.actions[0]
	prompt := obdomain.PlainText(action.Params["message"].([]obdomain.Segment))
	if action.Name != obdomain.ActionSendGroup || action.Params["group_id"] != groupA || !strings.Contains(prompt, fact) || !strings.Contains(prompt, fmt.Sprint(groupB)) || !strings.Contains(prompt, fmt.Sprint(groupD)) || !strings.Contains(prompt, "群友可以看到回复") {
		t.Fatalf("wrong outbound %v", action)
	}
	incoming := func(h *conversationinfra.Ingress, number, sender, group int64, segments ...obdomain.Segment) {
		t.Helper()
		err := h.Handle(ctx, obdomain.BotID(bot), obdomain.Event{Post: obdomain.PostMessage, Type: obdomain.TypeGroup, GroupID: obdomain.GroupID(group), UserID: obdomain.UserID(sender), Sender: obdomain.Sender{ID: obdomain.UserID(sender), Nickname: "same fixture name"}, MessageID: obdomain.MessageID(number), Segments: segments})
		if err != nil {
			t.Fatal(err)
		}
	}
	confirm := func(h *conversationinfra.Ingress, p memorycontracts.ConsentProposal, number int64) {
		incoming(h, number, person, groupA, obdomain.Reply(obdomain.MessageID(p.PlatformMessageID)), obdomain.At(obdomain.UserID(bot)), obdomain.Text("  同意跨群使用  "))
	}
	incoming(firstIngress, -200, other, groupA, obdomain.Reply(-101), obdomain.Text("同意跨群使用"))
	incoming(firstIngress, -201, person, groupA, obdomain.Reply(-555), obdomain.Text("同意跨群使用"))
	incoming(firstIngress, -202, person, groupD, obdomain.Reply(-101), obdomain.Text("同意跨群使用"))
	incoming(firstIngress, -203, person, groupA, obdomain.Reply(-101), obdomain.Reply(-101), obdomain.Text("同意跨群使用"))
	incoming(firstIngress, -204, person, groupA, obdomain.Reply(-101), obdomain.Text("同意跨群使用，还请发到别处"))
	if got := read(first); got.Status != "pending" || len(got.ActiveGroups) != 0 {
		t.Fatalf("false confirmation accepted: %+v", got)
	}
	confirm(secondIngress, first, -205)
	confirm(firstIngress, first, -205)
	first = read(first)
	if fmt.Sprint(first.ActiveGroups) != fmt.Sprint([]int64{groupB, groupD}) {
		t.Fatalf("approved groups: %+v", first)
	}
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM memory_consent_events WHERE proposal_id=$1`, first.ID).Scan(&count); err != nil || count != 1 {
		t.Fatalf("events=%d %v", count, err)
	}
	var modelCount int
	var lastBody string
	var modelMu sync.Mutex
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		modelMu.Lock()
		lastBody = string(raw)
		modelCount++
		modelMu.Unlock()
		var body struct{ Stream bool }
		_ = json.Unmarshal(raw, &body)
		if body.Stream {
			w.Header().Set("Content-Type", "text/event-stream")
			fmt.Fprint(w, "data: {\"id\":\"r\",\"model\":\"fixture\",\"object\":\"chat.completion.chunk\",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"content\":\"fixture\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
		} else {
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"id":"r","model":"fixture","object":"chat.completion","choices":[{"index":0,"message":{"role":"assistant","content":"fixture"},"finish_reason":"stop"}]}`)
		}
	}))
	defer model.Close()
	runtime, err := agentinfra.NewRuntime(ctx, agentinfra.Config{BaseURL: model.URL, APIKey: "fixture", Model: "fixture", Timeout: 45 * time.Second, HTTPTimeout: 30 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	replier, err := agentapp.NewReplier(agentapp.Options{Runtime: consentFixtureRuntime{runtime}, Scopes: scopes, Memory: mem, Senders: accounts, Recorder: conversation, Outbound: ledger, Logger: logger, CanSend: func(map[string]bool) bool { return false }})
	if err != nil {
		t.Fatal(err)
	}
	detailTools, err := builtin.NewMemoryProvider(mem2, nil).Tools(ctx)
	if err != nil {
		t.Fatal(err)
	}
	jobFor := func(group, sender, number int64) conversationdomain.Job {
		t.Helper()
		incoming(firstIngress, number, sender, group, obdomain.Text("我叫霁蓝吗"))
		all, err := conv2.ClaimReadyJobs(ctx, 30)
		if err != nil {
			t.Fatal(err)
		}
		for _, job := range all {
			if job.BotID == bot && job.Message.PlatformMessageID == number {
				return job
			}
		}
		t.Fatal("missing reply job")
		return conversationdomain.Job{}
	}
	expectedContent := fact
	readJob := func(job conversationdomain.Job, want bool) {
		t.Helper()
		run := memorycontracts.WithReplyJob(ctx, job.ID, job.ClaimToken)
		request := memorycontracts.RecallRequest{BotID: bot, Kind: "group", TargetID: job.TargetID, ConversationID: job.ConversationID, Query: "霁蓝称呼", Limit: 1, SubjectType: "user", SubjectID: fmt.Sprint(job.Message.SenderID)}
		hits, err := mem.Recall(run, request)
		if err != nil || (!want && len(hits.Items) != 0) || (want && len(hits.Items) != 1) || want && hits.Items[0].Memory.ID != id {
			t.Fatalf("recall group=%d sender=%d hits=%+v err=%v want=%v", job.TargetID, job.Message.SenderID, hits, err, want)
		}
		if job.TargetID != groupA {
			preview, err := mem.Recall(ctx, request)
			if err != nil || len(preview.Items) != 0 {
				t.Fatalf("unbound recall: %+v %v", preview, err)
			}
		}
		scope := pluginsapp.Scope{BotID: bot, Kind: "group", TargetID: job.TargetID, ConversationID: job.ConversationID, SenderID: job.Message.SenderID, AllowMemoryRead: true}
		result, err := detailTools[0].Invoke(pluginsapp.WithScope(run, scope), fmt.Sprintf("{\"memory_id\":%q}", id))
		if err != nil || strings.Contains(result, expectedContent) != want || job.TargetID != groupA && strings.Contains(result, "original-only") {
			t.Fatalf("detail: %s %v", result, err)
		}
		modelMu.Lock()
		before := modelCount
		modelMu.Unlock()
		err = replier.ExecuteReply(ctx, intentdomain.Task{BotID: bot, JobID: job.ID, ClaimToken: job.ClaimToken, Kind: "group", ConversationID: job.ConversationID, TargetID: job.TargetID, Message: intentdomain.Message{SenderID: job.Message.SenderID, MessageID: job.Message.PlatformMessageID, Text: job.Message.Text}}, nil)
		modelMu.Lock()
		defer modelMu.Unlock()
		if err != nil || modelCount != before+1 || strings.Contains(lastBody, expectedContent) != want || job.TargetID != groupA && strings.Contains(lastBody, "original-only") {
			t.Fatalf("generation group=%d sender=%d calls=%d err=%v body=%s", job.TargetID, job.Message.SenderID, modelCount-before, err, lastBody)
		}
		t.Logf("platform_id=%d group=%d subject=%v recall=%v detail=%v generation=%v model_requests=1", job.Message.PlatformMessageID, job.TargetID, job.Message.SenderID == person, len(hits.Items) == 1, strings.Contains(result, expectedContent), strings.Contains(lastBody, expectedContent))
		if err := convRepo.FailJob(ctx, job.ID, "fixture_no_platform_reply", 0, 1, job.ClaimToken); err != nil {
			t.Fatal(err)
		}
	}
	readJob(jobFor(groupB, person, -210), true)
	readJob(jobFor(groupB, other, -211), false)
	readJob(jobFor(groupC, person, -212), false)
	readJob(jobFor(groupA, person, -213), true)
	queued := create("old-pending", 1, groupB, groupD)
	revoke := func(h *conversationinfra.Ingress, p memorycontracts.ConsentProposal, number, group int64) {
		incoming(h, number, person, groupA, obdomain.Text(fmt.Sprintf("撤回跨群 %s %d", p.ID, group)))
	}
	revoke(disabledIngress, first, -220, groupB)
	if got := read(queued); got.Status != "invalidated" {
		t.Fatalf("old overlapping proposal = %+v", got)
	}
	deliver(claimOne(convRepo, queued.ID))
	confirm(firstIngress, first, -205)
	confirm(firstIngress, first, -221)
	if got := read(first); fmt.Sprint(got.ActiveGroups) != fmt.Sprint([]int64{groupD}) {
		t.Fatalf("old confirmation replay revived grant: %+v", got)
	}
	readJob(jobFor(groupB, person, -222), false)
	readJob(jobFor(groupD, person, -223), true)
	fresh := create("fresh", 1, groupB, groupD)
	deliver(claimOne(conv2, fresh.ID))
	fresh = read(fresh)
	confirm(firstIngress, fresh, -224)
	readJob(jobFor(groupB, person, -225), true)
	trackedJob := jobFor(groupB, person, -226)
	run := memorycontracts.WithRunReads(memorycontracts.WithReplyJob(ctx, trackedJob.ID, trackedJob.ClaimToken))
	request := memorycontracts.DetailRequest{BotID: bot, Kind: "group", TargetID: groupB, ConversationID: trackedJob.ConversationID, MemoryID: id}
	toolScope := pluginsapp.Scope{BotID: bot, Kind: "group", TargetID: groupB, ConversationID: trackedJob.ConversationID, SenderID: person, AllowMemoryRead: true, Reauthorize: func(context.Context, plugincontracts.ToolDescriptor) error { return nil }}
	var retained atomic.Int32
	var physical atomic.Int32
	toolModel := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		physical.Add(1)
		raw, _ := io.ReadAll(r.Body)
		if strings.Contains(string(raw), fact) {
			t.Error("revoked fact was sent to model")
		}
		args := fmt.Sprintf("{\"memory_id\":%q}", id)
		calls := fmt.Sprintf(`[{"index":0,"id":"tool-1","type":"function","function":{"name":"memory_detail","arguments":%q}}]`, args)
		var body struct{ Stream bool }
		_ = json.Unmarshal(raw, &body)
		if body.Stream {
			w.Header().Set("Content-Type", "text/event-stream")
			fmt.Fprintf(w, "data: {\"id\":\"r\",\"model\":\"fixture\",\"object\":\"chat.completion.chunk\",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"tool_calls\":%s},\"finish_reason\":\"tool_calls\"}]}\n\ndata: [DONE]\n\n", calls)
		} else {
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprintf(w, `{"id":"r","model":"fixture","object":"chat.completion","choices":[{"index":0,"message":{"role":"assistant","tool_calls":%s},"finish_reason":"tool_calls"}]}`, calls)
		}
	}))
	defer toolModel.Close()
	toolRuntime, err := agentinfra.NewRuntime(ctx, agentinfra.Config{BaseURL: toolModel.URL, APIKey: "fixture", Model: "fixture", Timeout: 45 * time.Second, HTTPTimeout: 30 * time.Second, Tools: func(context.Context, agentcontracts.Request) []tool.BaseTool {
		wrapped := detailTools[0]
		call := wrapped.Invoke
		wrapped.Invoke = func(toolCtx context.Context, args string) (string, error) {
			result, err := call(pluginsapp.WithScope(toolCtx, toolScope), args)
			if err == nil && strings.Contains(result, fact) {
				retained.Add(1)
				text := fmt.Sprintf("撤回跨群 %s %d", fresh.ID, groupB)
				err = secondIngress.Handle(ctx, obdomain.BotID(bot), obdomain.Event{Post: obdomain.PostMessage, Type: obdomain.TypeGroup, GroupID: obdomain.GroupID(groupA), UserID: obdomain.UserID(person), Sender: obdomain.Sender{ID: obdomain.UserID(person)}, MessageID: -227, Segments: []obdomain.Segment{obdomain.Text(text)}})
			}
			return result, err
		}
		return plugineino.ToBaseTools([]plugincontracts.RegisteredTool{wrapped})
	}})
	if err != nil {
		t.Fatal(err)
	}
	_, err = (consentFixtureRuntime{toolRuntime}).Run(run, agentcontracts.Request{AllowMemoryRead: true, Messages: []agentcontracts.Message{{Role: "user", Content: "请调用记忆详情工具"}}})
	if !errors.Is(err, memorycontracts.ErrReadChanged) || physical.Load() != 1 || retained.Load() != 1 {
		t.Fatalf("cached tool: retained=%d requests=%d err=%v", retained.Load(), physical.Load(), err)
	}
	t.Log("cross-group detail returned once, other replica revoked grant, second model request blocked")

	replacement := create("new-after-revoke", 1, groupB, groupD)
	deliver(claimOne(convRepo, replacement.ID))
	replacement = read(replacement)
	confirm(firstIngress, replacement, -229)
	if err := memorycontracts.ValidateRunReads(run); !errors.Is(err, memorycontracts.ErrReadChanged) {
		t.Fatalf("old run resumed after fresh grant: %v", err)
	}
	finish(trackedJob)
	exec(`DELETE FROM messages WHERE bot_id=$1 AND platform_message_id=-229`, bot)
	confirmationProbe := jobFor(groupB, person, -230)
	request.ConversationID = confirmationProbe.ConversationID
	if _, found, err := mem.RecallDetail(memorycontracts.WithReplyJob(ctx, confirmationProbe.ID, confirmationProbe.ClaimToken), request); found || err != nil {
		t.Fatalf("deleted confirmation: found=%v err=%v", found, err)
	}
	finish(confirmationProbe)
	beforeChange := create("before-edit", 1, groupB, groupD)
	row, _, err := memoryRepo.Get(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	row.Content = "原事实已更正"
	if err = memoryRepo.UpdateMemoryLocked(ctx, row); err != nil {
		t.Fatal(err)
	}
	row.Content = fact
	if err = memoryRepo.UpdateMemoryLocked(ctx, row); err != nil {
		t.Fatal(err)
	}
	current, _, err := memoryRepo.Get(ctx, id)
	if err != nil || current.AuthorizationVersion != 3 || read(beforeChange).Status != "invalidated" {
		t.Fatalf("fact version=%d err=%v", current.AuthorizationVersion, err)
	}
	deliver(claimOne(convRepo, beforeChange.ID))
	last := create("unknown", 3, groupB, groupD)
	wire.async = true
	job := claimOne(convRepo, last.ID)
	if err = processor(ctx, job); err == nil {
		t.Fatal("expected unknown OneBot outcome")
	}
	if err = processor(ctx, job); err != nil {
		t.Fatal(err)
	}
	if err := convRepo.FailJob(ctx, job.ID, "unknown_delivery", 0, 1, job.ClaimToken); err != nil {
		t.Fatal(err)
	}
	last = read(last)
	if last.DeliveryStatus != "unknown" || last.PlatformMessageID != 0 {
		t.Fatalf("unknown delivery %+v", last)
	}
	reserve := func(job conversationdomain.Job) outbounddomain.Record {
		t.Helper()
		_, text, err := memoryRepo.ConsentPrompt(ctx, bot, job.ConsentProposalID, job.ID, job.ClaimToken)
		if err != nil {
			t.Fatal(err)
		}
		record, err := ledger.Reserve(ctx, outboundapp.Request{JobID: job.ID, BotID: bot, ConversationID: job.ConversationID, ClaimToken: job.ClaimToken, ToolCallID: memorydomain.ConsentPromptKind, Text: text})
		if err != nil {
			t.Fatal(err)
		}
		return record
	}
	usage := func(jobID int64, want int) {
		t.Helper()
		var used int
		if err := pool.QueryRow(ctx, `SELECT reserved_messages FROM agent_run_budgets WHERE job_id=$1`, jobID).Scan(&used); err != nil || used != want {
			t.Fatalf("reserved=%d want=%d err=%v", used, want, err)
		}
	}
	cancelProposal := func(p memorycontracts.ConsentProposal) {
		t.Helper()
		got := api(http.MethodPost, createPath+"/"+p.ID+"/revoke", fmt.Sprintf("{\"expected_state_version\":%d}", p.StateVersion), true, 200)
		if got.Status != "cancelled" {
			t.Fatalf("cancelled state %+v", got)
		}
	}
	gateCase := create("cancel-at-gate", 3, groupB, groupD)
	gateJob := claimOne(convRepo, gateCase.ID)
	gateRecord := reserve(gateJob)
	cancelProposal(gateCase)
	if err := outbound2.MarkSending(ctx, gateRecord.ID, gateJob.ClaimToken); !errors.Is(err, outbounddomain.ErrConsentSendDenied) {
		t.Fatalf("cancelled gate: %v", err)
	}
	if err := ledger.Cancel(ctx, gateRecord.ID, gateJob.ClaimToken); err != nil {
		t.Fatal(err)
	}
	usage(gateJob.ID, 0)
	finish(gateJob)
	check := func(record outbounddomain.Record, job conversationdomain.Job, want string) {
		t.Helper()
		var status string
		if err := pool.QueryRow(ctx, `SELECT status FROM agent_outbound_messages WHERE id=$1 AND job_id=$2`, record.ID, job.ID).Scan(&status); err != nil || status != want {
			t.Fatalf("outbound status=%s want=%s err=%v", status, want, err)
		}
	}
	recoverJob := func(job conversationdomain.Job) {
		t.Helper()
		exec(`UPDATE message_jobs SET lease_expires_at=clock_timestamp()-interval '1 second' WHERE id=$1`, job.ID)
		if err := conv2.RecoverStaleJobs(ctx, conversationdomain.RecoveryPolicy{MaxAttempts: 5}); err != nil {
			t.Fatal(err)
		}
	}
	recoveryEvidence := func(record outbounddomain.Record) {
		t.Helper()
		var correct bool
		if err := second.QueryRow(ctx, `SELECT COALESCE(jsonb_array_length(o.payload->'lease_recoveries')=1
  AND e.value->>'reason'='lease_expired_prepared_refunded'
  AND (e.value->>'messages')::int=1 AND (e.value->>'chars')::int=o.charge_chars
  AND (e.value->>'new_attempt')::int=1
  AND (e.value->>'refunded_at')::timestamptz<=(e.value->>'reserved_at')::timestamptz
  AND (e.value->>'reserved_at')::timestamptz=o.created_at
  AND (SELECT count(*) FROM jsonb_object_keys(e.value))=6, false)
 FROM agent_outbound_messages o
 LEFT JOIN LATERAL jsonb_array_elements(o.payload->'lease_recoveries') WITH ORDINALITY e(value,n) ON e.n=1
 WHERE o.id=$1`, record.ID).Scan(&correct); err != nil || !correct {
			t.Fatalf("recovery evidence in delivery %s valid=%v err=%v", record.ID, correct, err)
		}
	}
	prepared := create("recover-prepared", 3, groupB, groupD)
	preparedJob := claimOne(convRepo, prepared.ID)
	preparedRecord := reserve(preparedJob)
	recoverJob(preparedJob)
	check(preparedRecord, preparedJob, "cancelled")
	usage(preparedJob.ID, 0)
	cancelProposal(prepared)
	reclaimed := claimOne(conv2, prepared.ID)
	if reclaimed.ClaimToken == preparedJob.ClaimToken {
		t.Fatal("claim reused")
	}
	if err := outbound.MarkSending(ctx, preparedRecord.ID, preparedJob.ClaimToken); !errors.Is(err, outboundinfra.ErrLeaseLost) {
		t.Fatalf("expired writer: %v", err)
	}
	deliver(reclaimed)
	wire.async = false
	promptRecovery := create("recover-prompt-same-slot", 3, groupB, groupD)
	beforeClaim := claimOne(convRepo, promptRecovery.ID)
	beforePrompt := reserve(beforeClaim)
	recoverJob(beforeClaim)
	usage(beforeClaim.ID, 0)
	afterClaim := claimOne(conv2, promptRecovery.ID)
	afterPrompt := reserve(afterClaim)
	if afterPrompt.ID != beforePrompt.ID || afterPrompt.Sequence != beforePrompt.Sequence {
		t.Fatal("prompt recovery changed its slot")
	}
	usage(beforeClaim.ID, 1)
	beforeSend := len(wire.actions)
	deliver(afterClaim)
	if len(wire.actions) != beforeSend+1 {
		t.Fatal("prompt recovery did not send exactly once")
	}
	check(afterPrompt, afterClaim, "sent")
	recoveryEvidence(afterPrompt)
	t.Log("control prompt recovery retained its slot, reserved once, and reached OneBot once")
	wire.async = true
	sending := create("recover-sending", 3, groupB, groupD)
	sendingJob := claimOne(convRepo, sending.ID)
	sendingRecord := reserve(sendingJob)
	if err := outbound.MarkSending(ctx, sendingRecord.ID, sendingJob.ClaimToken); err != nil {
		t.Fatal(err)
	}
	recoverJob(sendingJob)
	check(sendingRecord, sendingJob, "unknown")
	usage(sendingJob.ID, 1)
	if err := outbound.Settle(ctx, sendingRecord.ID, outboundapp.Outcome{Status: outbounddomain.StatusSent, PlatformMessageID: -999}, sendingJob.ClaimToken); !errors.Is(err, outboundinfra.ErrLeaseLost) {
		t.Fatalf("stale settlement: %v", err)
	}
	if err := processor(ctx, sendingJob); err != nil {
		t.Fatal(err)
	}
	if got := read(sending); got.DeliveryStatus != "unknown" || len(got.ActiveGroups) != 0 {
		t.Fatalf("recovered sending: %+v", got)
	}
	t.Log("cancelled-before-send=0, prepared-refund=0, sending-unknown-reservation=1, stale writes denied, business-cancelled or unknown recovery sends=0")
	// Source and control traffic spend the same last hourly slot.
	exec(`UPDATE scope_policies SET max_replies_per_hour=1 WHERE bot_id=$1 AND scope_id=$2`, bot, groupA)
	exec(`UPDATE agent_outbound_messages SET created_at=clock_timestamp()-interval '2 hours' WHERE bot_id=$1`, bot)
	pending := create("shared-hourly", 3, groupB, groupD)
	regular, err := conversation.Ingest(ctx, conversationdomain.IngestInput{BotID: bot, Kind: conversationdomain.KindGroup, TargetID: groupA, PlatformMessageID: -235, SenderID: person, Text: "ordinary fixture reply"})
	if err != nil {
		t.Fatal(err)
	}
	exec(`UPDATE message_jobs SET next_run_at=clock_timestamp()-interval '1 day' WHERE id=$1`, regular.JobID)
	ordinary := claimOne(convRepo, "")
	if ordinary.ID != regular.JobID {
		t.Fatalf("wrong ordinary job %d", ordinary.ID)
	}
	normalRecord, err := ledger.Reserve(ctx, outboundapp.Request{JobID: ordinary.ID, BotID: bot, ConversationID: ordinary.ConversationID, ClaimToken: ordinary.ClaimToken, Text: "counted ordinary reply", Limits: outbounddomain.Limits{MaxPerHour: 1}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = outboundapp.Deliver(ctx, ledger, normalRecord.ID, func(context.Context) (int64, error) { return -900, nil }, ordinary.ClaimToken); err != nil {
		t.Fatal(err)
	}
	finish(ordinary)
	quotaJob := claimOne(conv2, pending.ID)
	var budget *outbounddomain.BudgetError
	if err := processor(ctx, quotaJob); !errors.As(err, &budget) || budget.Dimension != "hourly_messages" {
		t.Fatalf("shared last slot not respected: %v", err)
	}
	finish(quotaJob)
	exec(`UPDATE scope_policies SET max_replies_per_hour=0 WHERE bot_id=$1 AND scope_id=$2`, bot, groupA)
	t.Log("ordinary reply used the last hourly slot, consent prompt sends=0")
	expires := create("expires", 3, groupB, groupD)
	expiredJob := claimOne(convRepo, expires.ID)
	wire.async = false
	deliver(expiredJob)
	expires = read(expires)
	exec(`UPDATE memory_consent_proposals SET expires_at=clock_timestamp()-interval '1 second' WHERE id=$1`, expires.ID)
	confirm(secondIngress, expires, -236)
	if found := read(expires); len(found.ActiveGroups) != 0 {
		t.Fatalf("expired confirmation accepted %+v", found)
	}
	// Give both actions a valid prompt and race them through independent pools.
	concurrent := create("confirm-revoke-race", 3, groupB, groupD)
	deliver(claimOne(convRepo, concurrent.ID))
	concurrent = read(concurrent)
	outcomes := make(chan error, 2)
	for _, entry := range []struct {
		h        *conversationinfra.Ingress
		id       obdomain.MessageID
		segments []obdomain.Segment
	}{
		{firstIngress, -237, []obdomain.Segment{obdomain.Reply(obdomain.MessageID(concurrent.PlatformMessageID)), obdomain.Text("同意跨群使用")}},
		{secondIngress, -238, []obdomain.Segment{obdomain.Text(fmt.Sprintf("撤回跨群 %s 全部", concurrent.ID))}},
	} {
		go func() {
			outcomes <- entry.h.Handle(ctx, obdomain.BotID(bot), obdomain.Event{Post: obdomain.PostMessage, Type: obdomain.TypeGroup, GroupID: obdomain.GroupID(groupA), UserID: obdomain.UserID(person), Sender: obdomain.Sender{ID: obdomain.UserID(person)}, MessageID: entry.id, Segments: entry.segments})
		}()
	}
	for range 2 {
		if err := <-outcomes; err != nil {
			t.Fatal(err)
		}
	}
	if got := read(concurrent); len(got.ActiveGroups) != 0 {
		t.Fatalf("concurrent revoke did not close grants: %+v", got)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM memory_consent_events WHERE proposal_id=$1 AND outcome='revoked'`, concurrent.ID).Scan(&count); err != nil || count != 1 {
		t.Fatalf("revoke events=%d %v", count, err)
	}
	sourceClear := create("source-clear", 3, groupB, groupD)
	deliver(claimOne(convRepo, sourceClear.ID))
	sourceClear = read(sourceClear)
	confirm(firstIngress, sourceClear, -239)
	if got := read(sourceClear); len(got.ActiveGroups) != 2 {
		t.Fatalf("expected source-clear grant %+v", got)
	}
	exec(`UPDATE messages SET created_at=clock_timestamp()-interval '90 days' WHERE id=$1`, original.MessageID)
	if n, err := convRepo.PurgeBefore(ctx, time.Now().Add(-60*24*time.Hour), 1); err != nil || n != 1 {
		t.Fatalf("purged=%d %v", n, err)
	}
	if got := read(sourceClear); len(got.ActiveGroups) != 0 || got.Status != "invalidated" {
		t.Fatalf("source removal: %+v", got)
	}
	readJob(jobFor(groupB, person, -240), false)
	t.Log("expired confirmation rejected, concurrent revoke converged, source deletion invalidated grant")
	if len(wire.actions) != 8 {
		t.Fatalf("OneBot sends=%d, want 8", len(wire.actions))
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM message_jobs j JOIN messages m ON m.id=j.incoming_message_id WHERE m.bot_id=$1 AND m.control_kind<>''`, bot).Scan(&count); err != nil || count != 0 {
		t.Fatalf("commands reached normal jobs=%d err=%v", count, err)
	}
	browser := func(mode, current, oldID, before, after string) bool {
		t.Helper()
		path := os.Getenv("HAP52B_BROWSER")
		if path == "" {
			return false
		}
		bctx, cancel := context.WithTimeout(ctx, 35*time.Second)
		defer cancel()
		output, err := execpkg.CommandContext(bctx, "node", path, web.URL, mode, current, oldID, before, after).CombinedOutput()
		t.Logf("%s", output)
		if err != nil {
			t.Fatalf("browser %s: %v", mode, err)
		}
		return true
	}
	const originalContent = "霁蓝称呼是这位成员本人明确提供的原称呼"
	const corrected = "霁蓝称呼已经通过本人更正为雾青"
	const successor = "霁蓝称呼已经通过本人更正为银灰"
	seedFact := func(text string, number int64, status memorydomain.Status) string {
		t.Helper()
		src, err := conversation.Ingest(ctx, conversationdomain.IngestInput{BotID: bot, Kind: conversationdomain.KindGroup, TargetID: groupA, PlatformMessageID: number, SenderID: person, Text: text, ArchiveOnly: true})
		if err != nil {
			t.Fatal(err)
		}
		m := memorydomain.Memory{BotID: bot, Visibility: memorydomain.VisibilityGroup, OwnerKey: fmt.Sprint(groupA), SourceMessageID: src.MessageID, SourceSenderID: person, SubjectType: memorydomain.SubjectUser, SubjectID: fmt.Sprint(person), Content: text, Source: "original-only", Status: status, Confidence: 0.9}
		if status == memorydomain.StatusActive {
			m.ApprovedBy = "fixture"
		}
		key, err := memoryRepo.Insert(ctx, m)
		if err != nil {
			t.Fatal(err)
		}
		return key
	}
	grant := func(key string, version, message int64) memorycontracts.ConsentProposal {
		t.Helper()
		p := create(key, version, groupB, groupD)
		deliver(claimOne(conv2, p.ID))
		p = read(p)
		if p.DeliveryStatus != "sent" || p.PlatformMessageID == 0 {
			t.Fatalf("delivery %+v", p)
		}
		confirm(secondIngress, p, message)
		p = read(p)
		if p.Status != "confirmed" || fmt.Sprint(p.ActiveGroups) != fmt.Sprint([]int64{groupB, groupD}) {
			t.Fatalf("grant %+v", p)
		}
		return p
	}
	assertVersion := func(key string, version int64, status memorydomain.Status) {
		t.Helper()
		m, found, err := memory2.Get(ctx, key)
		if err != nil || !found || m.AuthorizationVersion != version || m.Status != status {
			t.Fatalf("memory version/status: found=%v version=%d status=%v err=%v", found, m.AuthorizationVersion, m.Status, err)
		}
	}
	assertInactive := func(p memorycontracts.ConsentProposal, wantStatus string) {
		t.Helper()
		p = read(p)
		if len(p.ActiveGroups) != 0 || p.Status != wantStatus {
			t.Fatalf("consent should be inactive: %+v", p)
		}
	}
	id = seedFact(originalContent, -301, memorydomain.StatusActive)
	expectedContent = originalContent
	p1 := grant("lifecycle-original", 1, -302)
	readJob(jobFor(groupB, person, -303), true)
	readJob(jobFor(groupA, person, -304), true)
	if !browser("revise", id, "", originalContent, corrected) {
		api(http.MethodPatch, "/api/v1/memories/"+id, fmt.Sprintf("{\"content\":%q}", corrected), true, 200)
	}
	assertVersion(id, 2, memorydomain.StatusActive)
	assertInactive(p1, "invalidated")
	readJob(jobFor(groupB, person, -305), false)
	expectedContent = corrected
	readJob(jobFor(groupB, person, -306), false)
	readJob(jobFor(groupA, person, -307), true)
	api(http.MethodPatch, "/api/v1/memories/"+id, fmt.Sprintf("{\"content\":%q}", originalContent), true, 200)
	assertVersion(id, 3, memorydomain.StatusActive)
	confirm(firstIngress, p1, -302)
	confirm(firstIngress, p1, -308)
	assertInactive(p1, "invalidated")
	expectedContent = originalContent
	readJob(jobFor(groupB, person, -309), false)
	readJob(jobFor(groupA, person, -310), true)
	p3 := grant("lifecycle-updated", 3, -311)
	readJob(jobFor(groupB, person, -312), true)
	exec(`UPDATE messages SET created_at=clock_timestamp()-interval '90 days' WHERE bot_id=$1 AND platform_message_id=-311`, bot)
	if n, err := conv2.PurgeBefore(ctx, time.Now().Add(-60*24*time.Hour), 1); err != nil || n != 1 {
		t.Fatalf("purged confirmation=%d err=%v", n, err)
	}
	var nullProof int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM memory_consent_grants WHERE proposal_id=$1 AND confirmation_message_id IS NULL`, p3.ID).Scan(&nullProof); err != nil || nullProof != 2 {
		t.Fatalf("null confirmation references=%d err=%v", nullProof, err)
	}
	assertInactive(p3, "confirmed")
	confirm(firstIngress, p3, -311)
	assertInactive(p3, "confirmed")
	readJob(jobFor(groupB, person, -313), false)
	readJob(jobFor(groupA, person, -314), true)
	p4 := grant("lifecycle-before-replace", 3, -315)
	readJob(jobFor(groupB, person, -316), true)
	oldID := id
	newID := seedFact(successor, -317, memorydomain.StatusCandidate)
	if !browser("supersede", newID, oldID, originalContent, successor) {
		api(http.MethodPost, "/api/v1/memories/"+newID+"/supersede", fmt.Sprintf("{\"old_memory_id\":%q,\"reason\":\"synthetic correction\",\"idempotency_key\":\"lifecycle-replace\"}", oldID), true, 200)
	}
	assertVersion(oldID, 4, memorydomain.StatusSuperseded)
	assertVersion(newID, 2, memorydomain.StatusActive)
	assertInactive(p4, "invalidated")
	confirm(firstIngress, p4, -318)
	assertInactive(p4, "invalidated")
	readJob(jobFor(groupB, person, -319), false)
	id, expectedContent = newID, successor
	readJob(jobFor(groupB, person, -320), false)
	readJob(jobFor(groupA, person, -321), true)
	p5 := grant("lifecycle-successor", 2, -322)
	readJob(jobFor(groupB, person, -323), true)
	api(http.MethodPost, createPath+"/"+p5.ID+"/revoke", fmt.Sprintf("{\"expected_state_version\":%d,\"target_group_id\":%d}", p5.StateVersion, groupB), true, 200)
	if got := read(p5); fmt.Sprint(got.ActiveGroups) != fmt.Sprint([]int64{groupD}) {
		t.Fatalf("HTTP revoke: %+v", got)
	}
	readJob(jobFor(groupB, person, -324), false)
	readJob(jobFor(groupD, person, -325), true)
	t.Logf("lifecycle positive and negative per-id cases=17, added model requests=%d, added OneBot prompts=%d", modelCount-8, len(wire.actions)-7)

	setSource := func(enabled, store, canSend bool) {
		t.Helper()
		p := scopedomain.Default(scopedomain.ScopeGroup, groupA)
		p.Enabled, p.StoreMessages, p.AllowMemoryRead, p.AllowHistoryRead = enabled, store, true, true
		p.ReplyMode = scopedomain.ModeAlwaysReply
		p.MinReplyIntervalMS = 1
		p.ToolPermissions = map[string]bool{"qq.reply.current": canSend}
		if err := scopes2.Upsert(ctx, bot, p); err != nil {
			t.Fatal(err)
		}
	}
	setSource(true, true, false)
	api(http.MethodPost, createPath, body("creation-denied", 2, groupB, groupD), true, 409)
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM memory_consent_proposals WHERE bot_id=$1 AND idempotency_key='creation-denied'`, bot).Scan(&count); err != nil || count != 0 {
		t.Fatalf("creation denied left proposal=%d err=%v", count, err)
	}
	setSource(true, true, true)
	beforeDisable := create("disable-confirm", 2, groupB, groupD)
	deliver(claimOne(convRepo, beforeDisable.ID))
	beforeDisable = read(beforeDisable)
	disabledConfirm := func(p memorycontracts.ConsentProposal, number int64) {
		t.Helper()
		_, err := conversation2.Ingest(ctx, conversationdomain.IngestInput{BotID: bot, Kind: conversationdomain.KindGroup, TargetID: groupA, SenderID: person, PlatformMessageID: number, ConsentControl: true, Segments: []obdomain.Segment{obdomain.Reply(obdomain.MessageID(p.PlatformMessageID)), obdomain.Text("同意跨群使用")}})
		if err != nil {
			t.Fatal(err)
		}
		var outcome string
		if err = pool.QueryRow(ctx, `SELECT outcome FROM memory_consent_events WHERE proposal_id=$1 AND platform_message_id=$2`, p.ID, number).Scan(&outcome); err != nil || outcome != "rejected" {
			t.Fatalf("disabled confirmation %s %v", outcome, err)
		}
	}
	setSource(false, true, true)
	disabledConfirm(beforeDisable, -326)
	setSource(true, true, true)
	exec(`UPDATE scope_policies SET store_messages=false WHERE bot_id=$1 AND scope_id=$2`, bot, groupA)
	disabledConfirm(beforeDisable, -327)
	setSource(true, true, false)
	confirm(firstIngress, beforeDisable, -328)
	beforeDisable = read(beforeDisable)
	if len(beforeDisable.ActiveGroups) != 2 {
		t.Fatalf("delivered prompt became unconfirmable after send permission revoked: %+v", beforeDisable)
	}
	setSource(true, true, true)
	t.Log("create with sending revoked=409 and zero proposal; disabled/storage-off confirmations rejected; delivered prompt confirmed with sending revoked")

	publisher := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		if !strings.Contains(string(raw), successor) {
			t.Error("authorized generation did not get granted fact")
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"id": "dispatch", "model": "fixture", "choices": []map[string]any{{"index": 0, "finish_reason": "stop", "message": map[string]string{"role": "assistant", "content": "派生回复标记 [CQ:at,qq=972522]"}}}})
	}))
	defer publisher.Close()
	liveRuntime, err := agentinfra.NewRuntime(ctx, agentinfra.Config{BaseURL: publisher.URL, APIKey: "fixture", Model: "fixture", Timeout: 5 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	liveReply, err := agentapp.NewReplier(agentapp.Options{Runtime: consentFixtureRuntime{liveRuntime}, Scopes: scopes, Memory: mem, Senders: accounts, Recorder: conversation, Outbound: ledger, Logger: logger})
	if err != nil {
		t.Fatal(err)
	}
	makeTask := func(j conversationdomain.Job) intentdomain.Task {
		return intentdomain.Task{BotID: bot, JobID: j.ID, ClaimToken: j.ClaimToken, Kind: "group", TargetID: j.TargetID, ConversationID: j.ConversationID, Message: intentdomain.Message{SenderID: j.Message.SenderID, MessageID: j.Message.PlatformMessageID, Text: j.Message.Text, CreatedAt: j.Message.CreatedAt, Segments: j.Message.Segments}, Segments: j.Message.Segments}
	}
	j := jobFor(groupB, person, -329)
	decisionRepo, _ := intentinfra.NewDecisionsRepo(pool)
	if err := decisionRepo.Record(ctx, j.ID, intentdomain.FinalDecision{Decision: intentdomain.DecisionReply, ReasonCode: "direct_name_call", Snapshot: &intentdomain.DecisionSnapshot{Input: intentdomain.SnapshotInput{TopicChange: &intentdomain.TopicChange{ID: j.ID, Action: "opened", ExpiresAt: time.Now().Add(5 * time.Minute)}}}}, j.ClaimToken); err != nil {
		t.Fatal(err)
	}
	spans := tracetest.NewSpanRecorder()
	oldTP := otel.GetTracerProvider()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(spans))
	otel.SetTracerProvider(tp)
	t.Cleanup(func() { otel.SetTracerProvider(oldTP); _ = tp.Shutdown(ctx) })
	if err := liveReply.ExecuteReply(ctx, makeTask(j), nil); err != nil {
		t.Fatal(err)
	}
	finish(j)
	lastAction := wire.actions[len(wire.actions)-1]
	segments := lastAction.Params["message"].([]obdomain.Segment)
	if lastAction.Params["group_id"] != groupB || len(segments) != 1 || segments[0].Type != obdomain.SegText || !strings.Contains(segments[0].Data["text"], "派生回复标记") {
		t.Fatalf("automatic OneBot action not literal: %+v", lastAction)
	}
	for _, span := range spans.Ended() {
		for _, field := range span.Attributes() {
			if strings.Contains(field.Value.Emit(), "派生回复标记") || strings.Contains(field.Value.Emit(), successor) {
				t.Fatalf("raw span content escaped: %s %s", span.Name(), field.Key)
			}
		}
	}
	var derivedID int64
	if err := pool.QueryRow(ctx, `SELECT platform_message_id FROM messages WHERE bot_id=$1 AND conversation_id=$2 AND control_kind='memory_consent_reply' AND text_content LIKE '派生回复标记%'`, bot, j.ConversationID).Scan(&derivedID); err != nil {
		t.Fatal(err)
	}
	quoted := func(h *conversationinfra.Ingress, number, sender, group, reference int64) conversationdomain.Job {
		t.Helper()
		incoming(h, number, sender, group, obdomain.Reply(obdomain.MessageID(reference)), obdomain.Text("请继续刚才的普通话题"))
		jobs, err := conv2.ClaimReadyJobs(ctx, 30)
		if err != nil {
			t.Fatal(err)
		}
		for _, job := range jobs {
			if job.BotID == bot && job.Message.PlatformMessageID == number {
				return job
			}
		}
		t.Fatal("missing quoted job")
		return conversationdomain.Job{}
	}
	counts := map[string]int{}
	var semMu sync.Mutex
	capture := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		if strings.Contains(string(raw), successor) || strings.Contains(string(raw), "派生回复标记") || strings.Contains(string(raw), "同意跨群使用") {
			t.Error("consent content entered later model request")
		}
		semMu.Lock()
		counts[r.URL.Path]++
		semMu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/jev" {
			fmt.Fprint(w, `{"answers":{}}`)
			return
		}
		if r.URL.Path == "/chat/completions" {
			var req struct{ Logprobs bool }
			_ = json.Unmarshal(raw, &req)
			if req.Logprobs {
				fmt.Fprint(w, `{"choices":[{"logprobs":{"content":[{"token":"A","top_logprobs":[{"token":"A","logprob":-0.1},{"token":"B","logprob":-3}]}]}}]}`)
				return
			}
			fmt.Fprint(w, `{"id":"probe","model":"fixture","choices":[{"index":0,"message":{"role":"assistant","content":"普通回复"},"finish_reason":"stop"}]}`)
		}
	}))
	defer capture.Close()
	jev, _ := intentinfra.NewJevClient(intentinfra.JevConfig{Endpoint: capture.URL + "/jev", Model: "fixture", Timeout: 5 * time.Second}, logger)
	local := &intentinfra.LocalClient{Config: intentinfra.JevConfig{Endpoint: capture.URL + "/chat/completions", Model: "fixture", Timeout: 5 * time.Second}}
	cleanRuntime, err := agentinfra.NewRuntime(ctx, agentinfra.Config{BaseURL: capture.URL, APIKey: "fixture", Model: "fixture", Timeout: 5 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	cleanReply, err := agentapp.NewReplier(agentapp.Options{Runtime: cleanRuntime, Scopes: scopes, Memory: mem, Senders: accounts, Recorder: conversation, Outbound: ledger, Logger: logger, CanSend: func(map[string]bool) bool { return false }})
	if err != nil {
		t.Fatal(err)
	}
	for _, group := range []int64{groupA, groupB} {
		p := scopedomain.Default(scopedomain.ScopeGroup, group)
		p.Enabled, p.StoreMessages, p.AllowMemoryRead, p.AllowHistoryRead = true, true, true, true
		p.ReplyMode, p.MinReplyIntervalMS, p.MaxRepliesPerHour = scopedomain.ModeIntentBased, 1, 0
		if err := scopes.Upsert(ctx, bot, p); err != nil {
			t.Fatal(err)
		}
	}
	checkContext := func(j conversationdomain.Job, reference int64, adapter string, semantic intentapp.SemanticDecider) {
		t.Helper()
		task := makeTask(j)
		topics, err := decisionRepo.Topics(ctx, task, reference, 300)
		if err != nil || topics.Reference != nil || topics.QuotedID != 0 {
			t.Fatalf("hidden quote resolved: %+v %v", topics, err)
		}
		for _, c := range topics.Candidates {
			for _, m := range c.History {
				if strings.Contains(m.Text, "派生回复标记") {
					t.Fatal("derived history was aggregated")
				}
			}
		}
		if adapter != "" {
			engine, err := intentapp.NewDecider(intentapp.Options{Semantic: semantic, History: historyReader{svc: conversation}, Scopes: scopes, Recorder: decisionRepo, Logger: logger, Dynamic: func() intentapp.RuntimeConfig {
				return intentapp.RuntimeConfig{Enabled: true, AIEnabled: true, HistoryLimit: 15, TopicIdleSeconds: 300, Provider: adapter, Model: "fixture"}
			}})
			if err != nil {
				t.Fatal(err)
			}
			if err := engine.ProcessTask(ctx, task); err != nil {
				t.Fatal(err)
			}
			history, err := (historyReader{svc: conversation}).RecentIntentMessages(ctx, j.ConversationID, 15)
			if err != nil {
				t.Fatal(err)
			}
			if err := cleanReply.ExecuteReply(ctx, task, history); err != nil {
				t.Fatal(err)
			}
		}
		if adapter == "" {
			finish(j)
		} else if err := convRepo.FailJob(ctx, j.ID, "fixture_no_platform_reply", 0, 1, j.ClaimToken); err != nil {
			t.Fatal(err)
		}
	}
	checkContext(quoted(firstIngress, -330, other, groupB, derivedID), derivedID, "jev", jev)
	revoke(secondIngress, beforeDisable, -331, groupB)
	checkContext(quoted(firstIngress, -332, person, groupB, derivedID), derivedID, "openai", local)
	checkContext(quoted(firstIngress, -333, other, groupA, beforeDisable.PlatformMessageID), beforeDisable.PlatformMessageID, "", nil)
	if counts["/jev"] < 1 || counts["/chat/completions"] < 7 {
		t.Fatalf("intent and agent physical counts=%v", counts)
	}
	t.Logf("automatic reply received once and stored as derived, raw spans absent; hidden content absent from quoted and aggregated context, actual request counts=%v", counts)

	arm := func(key string, event int64) memorycontracts.ConsentProposal {
		t.Helper()
		p := create(key, 2, groupB, groupD)
		deliver(claimOne(convRepo, p.ID))
		p = read(p)
		confirm(secondIngress, p, event)
		return read(p)
	}
	tracked := func(j conversationdomain.Job) context.Context {
		t.Helper()
		run := memorycontracts.WithRunReads(memorycontracts.WithReplyJob(ctx, j.ID, j.ClaimToken))
		req := memorycontracts.DetailRequest{BotID: bot, Kind: "group", TargetID: groupB, ConversationID: j.ConversationID, MemoryID: id}
		item, ok, err := mem.RecallDetail(run, req)
		if err != nil || !ok || item.ConsentIdentity == "" {
			t.Fatalf("dispatch requires live grant: %v %v", ok, err)
		}
		memorycontracts.TrackVersionedRead(run, mem, req, item, nil)
		return run
	}
	p := arm("dispatch-refund", -334)
	j = jobFor(groupB, person, -33401)
	run = tracked(j)
	registry := pluginsapp.NewRegistry()
	if err := registry.Register(ctx, builtin.NewPublicProvider(builtin.PublicOptions{
		Account: func(int64) (onebotcontracts.PublicAPI, bool) { return wire.client, true },
		Send: func(call context.Context, command obdomain.PublicCommand) (plugincontracts.SendReceipt, error) {
			return replytool.SendPublic(call, replytool.Options{Ledger: ledger}, command, wire.client.ExecutePublic)
		},
	})); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = registry.Close(ctx) })
	toolSettings := settingsForTest(&config.Config{Agent: config.Agent{MaxToolCalls: 2}})
	resolver := buildToolsResolver(toolSettings, registry, scopes)
	tools := resolver(run, agentcontracts.Request{
		BotID: bot, JobID: j.ID, ClaimToken: j.ClaimToken, ConversationID: j.ConversationID,
		Kind: "group", TargetID: groupB, SenderID: person, MessageID: j.Message.PlatformMessageID,
		PermissionOverrides: map[string]bool{"qq.reply.current": true},
	})
	node, err := compose.NewToolNode(run, &compose.ToolsNodeConfig{Tools: tools})
	if err != nil {
		t.Fatal(err)
	}
	invocation := &schema.Message{Role: schema.Assistant, ToolCalls: []schema.ToolCall{{
		ID: "fixture-framework-id", Type: "function", Function: schema.FunctionCall{Name: "qq_send_msg", Arguments: `{"message":"普通业务回执对账"}`},
	}}}
	beforeWrapped := len(wire.actions)
	var originalReceipt string
	for i := 0; i < 2; i++ {
		results, err := node.Invoke(run, invocation)
		if err != nil || len(results) != 1 || results[0].ToolCallID != invocation.ToolCalls[0].ID || !strings.Contains(results[0].Content, `"sent"`) {
			t.Fatalf("framework invocation %d: results=%v err=%v", i, results, err)
		}
		if i == 0 {
			originalReceipt = results[0].Content
		} else if results[0].Content != originalReceipt {
			t.Fatalf("same framework call returned a different receipt: %s / %s", originalReceipt, results[0].Content)
		}
	}
	result, err := node.Invoke(run, invocation)
	if err != nil || len(result) != 1 || !strings.Contains(result[0].Content, "上限") {
		t.Fatalf("tool call budget was not applied independently: %v %v", result, err)
	}
	rows, err := outbound.ListByJob(ctx, j.ID)
	if err != nil || len(rows) != 1 || rows[0].Sequence != 1 || rows[0].PlatformMessageID == 0 || len(wire.actions) != beforeWrapped+1 {
		t.Fatalf("wrapped call slots=%v platform=%d err=%v", rows, len(wire.actions)-beforeWrapped, err)
	}
	usage(j.ID, 1)
	finish(j)
	t.Log("production tool wrapper: two invocations of the same framework call, one slot, one reservation, one OneBot action; third invocation limited by tool budget")
	j = jobFor(groupB, person, -335)
	run = tracked(j)
	b := len(wire.actions)
	pluginScope := pluginsapp.Scope{BotID: bot, JobID: j.ID, ClaimToken: j.ClaimToken, ConversationID: j.ConversationID, Kind: "group", TargetID: groupB, SenderID: other, MessageID: -999, Permissions: map[string]bool{"qq.reply.current": true, "qq.content.at_all": true, "qq.content.media": true}}
	run = pluginsapp.WithToolCallID(pluginsapp.WithScope(run, pluginScope), "verified-recipient")
	for i, seg := range []obdomain.Segment{obdomain.At(obdomain.UserID(other)), obdomain.Reply(-999), {Type: obdomain.SegAt, Data: map[string]string{"qq": "all"}}, obdomain.Image("fixture"), obdomain.Segment{Type: obdomain.SegForward, Data: map[string]string{"id": "fixture"}}} {
		_, err := replytool.SendPublic(pluginsapp.WithToolCallID(run, fmt.Sprintf("forbidden-%d", i)), replytool.Options{Ledger: ledger}, obdomain.PublicCommand{Name: obdomain.ActionSendGroup, GroupID: obdomain.GroupID(groupB), Segments: []obdomain.Segment{seg, obdomain.Text("派生回复标记")}}, wire.client.ExecutePublic)
		if err == nil || len(wire.actions) != b {
			t.Fatalf("forbidden segment %s reached platform: %v", seg.Type, err)
		}
	}
	_, err = replytool.SendPublic(run, replytool.Options{Ledger: ledger}, obdomain.PublicCommand{Name: obdomain.ActionSendGroup, GroupID: obdomain.GroupID(groupB), Segments: []obdomain.Segment{obdomain.Reply(-335), obdomain.At(obdomain.UserID(person)), obdomain.Text("允许的公开派生回复")}}, wire.client.ExecutePublic)
	if err != nil || len(wire.actions) != b+1 {
		t.Fatalf("verified trigger and person were denied: %v", err)
	}
	literal := "[CQ:at,qq=" + strconv.FormatInt(person, 10) + "] 引用标记"
	_, err = replytool.SendPublic(pluginsapp.WithToolCallID(run, "literal"), replytool.Options{Ledger: ledger}, obdomain.PublicCommand{Name: obdomain.ActionSendGroup, GroupID: obdomain.GroupID(groupB), Text: &literal}, wire.client.ExecutePublic)
	if err != nil || len(wire.actions) != b+2 {
		t.Fatalf("literal consent reply counts: actions=%d err=%v", len(wire.actions)-b, err)
	}
	literalSegments, ok := wire.actions[b+1].Params["message"].([]obdomain.Segment)
	if !ok || len(literalSegments) != 1 || literalSegments[0].Type != obdomain.SegText || literalSegments[0].Data["text"] != literal {
		t.Fatal("literal consent reply changed shape or content")
	}
	finish(j)
	j = jobFor(groupB, person, -336)
	run = tracked(j)
	reserveDerived := func(key string) outbounddomain.Record {
		t.Helper()
		r, err := ledger.Reserve(run, outboundapp.Request{JobID: j.ID, BotID: bot, ConversationID: j.ConversationID, ClaimToken: j.ClaimToken, ToolCallID: key, Text: "孤立派发正文"})
		if err != nil {
			t.Fatal(err)
		}
		return r
	}
	r := reserveDerived("before-revoke")
	n := len(wire.actions)
	revoke(secondIngress, p, -337, groupB)
	if _, err := outboundapp.Deliver(run, ledger, r.ID, func(call context.Context) (int64, error) {
		return wire.client.SendGroupMessageReceipt(call, obdomain.GroupID(groupB), obdomain.Text("孤立派发正文"))
	}, j.ClaimToken); !errors.Is(err, outbounddomain.ErrConsentSendDenied) || len(wire.actions) != n {
		t.Fatalf("revoked prepared reply was sent: %v", err)
	}
	if err := ledger.Cancel(ctx, r.ID, j.ClaimToken); err != nil {
		t.Fatal(err)
	}
	usage(j.ID, 0)
	p = arm("dispatch-regrant", -338)
	if err := memorycontracts.ValidateRunReads(run); !errors.Is(err, memorycontracts.ErrReadChanged) {
		t.Fatalf("old context survived regrant: %v", err)
	}
	finish(j)
	j = jobFor(groupB, person, -339)
	run = tracked(j)
	r = reserveDerived("dispatch-first")
	waitingRecord := reserveDerived("dispatch-second")
	if _, err := outboundapp.Deliver(run, ledger, r.ID, func(call context.Context) (int64, error) {
		revoke(secondIngress, p, -340, groupB)
		return wire.client.SendGroupMessageReceipt(call, obdomain.GroupID(groupB), obdomain.Text("孤立派发正文"))
	}, j.ClaimToken); err != nil || len(wire.actions) != n+2 {
		t.Fatalf("committed dispatch did not settle exactly once: actions=%d base=%d err=%v", len(wire.actions), n, err)
	}
	if _, err := outboundapp.Deliver(run, ledger, waitingRecord.ID, func(call context.Context) (int64, error) {
		return wire.client.SendGroupMessageReceipt(call, obdomain.GroupID(groupB), obdomain.Text("孤立派发正文"))
	}, j.ClaimToken); !errors.Is(err, outbounddomain.ErrConsentSendDenied) || len(wire.actions) != n+2 {
		t.Fatalf("later dispatch was not blocked: %v", err)
	}
	if err := ledger.Cancel(ctx, waitingRecord.ID, j.ClaimToken); err != nil {
		t.Fatal(err)
	}
	usage(j.ID, 1)
	finish(j)
	t.Log("public payload uses persisted sender/trigger; five forbidden recipient/media forms sent zero; pre-dispatch revoke refunded; regrant denied stale run; post-commit revoke allowed one settled delivery and stopped the next")

	settled := r.ID
	p = arm("revoke-during-interval", -341)
	j = jobFor(groupB, person, -342)
	// Anchor the interval to an actual settled row just before the test send.
	// 用已实结的投递在测试发送前建立限流窗口。
	exec(`UPDATE agent_outbound_messages SET updated_at=clock_timestamp() WHERE id=$1 AND status='sent'`, settled)
	run = pluginsapp.WithScope(tracked(j), pluginsapp.Scope{BotID: bot, JobID: j.ID, ClaimToken: j.ClaimToken, ConversationID: j.ConversationID, Kind: "group", TargetID: groupB, Permissions: map[string]bool{"qq.reply.current": true}})
	waiting := consentWaitingLedger{Coordinator: ledger, reserved: make(chan outbounddomain.Record, 1)}
	intervalResult := make(chan error, 1)
	go func() {
		_, sendErr := replytool.SendPublic(pluginsapp.WithToolCallID(run, "interval"), replytool.Options{Ledger: waiting, Limits: outbounddomain.Limits{MinIntervalMS: 6000}}, obdomain.PublicCommand{Name: obdomain.ActionSendGroup, GroupID: obdomain.GroupID(groupB), Segments: []obdomain.Segment{obdomain.Text("合成限流等待派生回复")}}, wire.client.ExecutePublic)
		intervalResult <- sendErr
	}()
	select {
	case prepared := <-waiting.reserved:
		if !prepared.NotBefore.After(time.Now()) {
			t.Fatalf("derived reply did not enter interval wait: %s", prepared.NotBefore)
		}
	case sendErr := <-intervalResult:
		t.Fatalf("derived reply finished before interval wait: %v", sendErr)
	case <-time.After(5 * time.Second):
		t.Fatal("derived reply was not reserved")
	}
	n = len(wire.actions)
	revoke(secondIngress, p, -343, groupB)
	if sendErr := <-intervalResult; !errors.Is(sendErr, memorycontracts.ErrReadChanged) || len(wire.actions) != n {
		t.Fatalf("revocation during interval: %v actions=%d baseline=%d", sendErr, len(wire.actions), n)
	}
	usage(j.ID, 0)
	finish(j)
	t.Log("persistent revoke after reservation during actual reply interval: OneBot=0, usage=0")

	p = arm("revoke-after-dial", -344)
	j = jobFor(groupB, person, -345)
	run = tracked(j)
	var connected atomic.Int32
	var dialHits atomic.Int32
	dialServer := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { dialHits.Add(1) }))
	defer dialServer.Close()
	dialRuntime, err := agentinfra.NewRuntime(ctx, agentinfra.Config{BaseURL: dialServer.URL, APIKey: "fixture", Model: "fixture", Timeout: 10 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	revokeResult := make(chan error, 1)
	run = httptrace.WithClientTrace(run, &httptrace.ClientTrace{ConnectDone: func(_, _ string, connErr error) {
		if connErr == nil && connected.Add(1) == 1 {
			text := fmt.Sprintf("撤回跨群 %s %d", p.ID, groupB)
			revokeResult <- secondIngress.Handle(ctx, obdomain.BotID(bot), obdomain.Event{Post: obdomain.PostMessage, Type: obdomain.TypeGroup, GroupID: obdomain.GroupID(groupA), UserID: obdomain.UserID(person), Sender: obdomain.Sender{ID: obdomain.UserID(person)}, MessageID: -346, Segments: []obdomain.Segment{obdomain.Text(text)}})
		}
	}})
	_, err = (consentFixtureRuntime{dialRuntime}).Run(run, agentcontracts.Request{Messages: []agentcontracts.Message{{Role: "user", Content: successor}}})
	var revokeErr error
	select {
	case revokeErr = <-revokeResult:
	default:
		revokeErr = errors.New("revocation did not complete before the model returned")
	}
	if !errors.Is(err, memorycontracts.ErrReadChanged) || connected.Load() != 1 || dialHits.Load() != 0 || revokeErr != nil {
		t.Fatalf("persistent revoke after dial: connects=%d requests=%d revoke=%v run=%v", connected.Load(), dialHits.Load(), revokeErr, err)
	}
	finish(j)
	t.Log("actual loopback TCP connected, second pool committed revoke before first model write: HTTP requests=0")

	publicTools, err := builtin.NewPublicProvider(builtin.PublicOptions{
		StoredMessage: convRepo.StoredPublicMessage,
		Account:       func(int64) (onebotcontracts.PublicAPI, bool) { return wire.client, true },
	}).Tools(ctx)
	if err != nil {
		t.Fatal(err)
	}
	publicByID := map[string]plugincontracts.RegisteredTool{}
	for _, tool := range publicTools {
		publicByID[tool.Descriptor.ID] = tool
	}
	invokePublic := func(call context.Context, name, args string) string {
		t.Helper()
		tool := publicByID["qq."+name]
		scope := pluginsapp.Scope{BotID: bot, Kind: "group", TargetID: groupB, ConversationID: j.ConversationID, Permissions: map[string]bool{tool.Descriptor.Permission: true, "qq.cross_conversation": true}}
		value, err := pluginsapp.Guard(scope, tool.Descriptor, tool.Invoke)(call, args)
		if err != nil {
			t.Fatal(err)
		}
		return value
	}
	const normalPlatformID = -379
	incoming(firstIngress, normalPlatformID, person, groupB, obdomain.Text("合成普通历史正文"))
	for _, candidate := range func() []conversationdomain.Job {
		jobs, err := conv2.ClaimReadyJobs(ctx, 30)
		if err != nil {
			t.Fatal(err)
		}
		return jobs
	}() {
		if candidate.BotID == bot && candidate.Message.PlatformMessageID == normalPlatformID {
			finish(candidate)
		}
	}
	beforeRead := len(wire.actions)
	if read := invokePublic(ctx, "get_msg", `{"message_id":-379}`); !strings.Contains(read, "合成普通历史正文") {
		t.Fatalf("ordinary stored message was not returned: %s", read)
	}
	for name, args := range map[string]string{"get_msg": fmt.Sprintf(`{"message_id":%d}`, derivedID), "get_forward_msg": `{"id":"fixture-unknown"}`, "get_record": `{"file":"fixture-unknown","out_format":"mp3"}`, "get_image": `{"file":"fixture-unknown"}`} {
		if read := invokePublic(ctx, name, args); !strings.Contains(read, "无法核验") {
			t.Fatalf("restricted read decision: %s", read)
		}
	}
	if read := invokePublic(run, "get_group_info", `{}`); !strings.Contains(read, "仅允许当前群文字") {
		t.Fatalf("derived platform action decision: %s", read)
	}
	if len(wire.actions) != beforeRead {
		t.Fatal("local and blocked reads reached platform")
	}
	t.Log("ordinary source read=1, restricted or unavailable source reads=0, derived non-send actions=0")

	p = arm("authoritative-first-write", -350)
	j = jobFor(groupB, person, -351)
	run = tracked(j)
	var actual atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		actual.Add(1)
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"id":"fixture","choices":[{"index":0,"message":{"role":"assistant","content":"通过"},"finish_reason":"stop"}]}`)
	}))
	defer upstream.Close()
	profile := modelcontracts.Profile{ID: "hap52b-wire", Usage: "generation", Protocol: "openai", Model: "fixture", BaseURL: upstream.URL, Version: 1, VerifiedVersion: 1, VerifiedAt: time.Now()}
	modelSettings := settingsdomain.Settings{Models: modelcontracts.Catalog{Profiles: []modelcontracts.Profile{profile}, Defaults: map[string]string{"generation": profile.ID}}}
	modelSettings.AI.Enabled = true
	var priorSettings []byte
	if err := pool.QueryRow(ctx, `SELECT COALESCE((SELECT to_jsonb(s) FROM system_settings s WHERE id=1),'null'::jsonb)`).Scan(&priorSettings); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if string(priorSettings) == "null" {
			if _, err := pool.Exec(ctx, `DELETE FROM system_settings WHERE id=1`); err != nil {
				t.Errorf("clear fixture settings: %v", err)
			}
			return
		}
		if _, err := pool.Exec(ctx, `UPDATE system_settings s SET payload=old.payload,version=old.version,updated_at=old.updated_at,updated_by=old.updated_by
 FROM jsonb_populate_record(NULL::system_settings,$1::jsonb) old WHERE s.id=old.id`, priorSettings); err != nil {
			t.Errorf("restore fixture settings: %v", err)
		}
	})
	payload, _ := json.Marshal(modelSettings)
	exec(`INSERT INTO system_settings(id,payload,version,updated_by) VALUES(1,$1,1,'fixture') ON CONFLICT(id) DO UPDATE SET payload=excluded.payload,version=system_settings.version+1`, payload)
	actualRuntime, err := agentinfra.NewRuntime(ctx, agentinfra.Config{BaseURL: upstream.URL, APIKey: "fixture", Model: profile.Model})
	if err != nil {
		t.Fatal(err)
	}
	run = memorycontracts.WithConsentDestination(run, func(call context.Context, d memorycontracts.GenerationDispatch) error {
		return memoryRepo.ValidateConsentGeneration(call, profile, d)
	})
	wireInput := agentcontracts.Request{Messages: []agentcontracts.Message{{Role: "user", Content: successor}}}
	if _, err := actualRuntime.Run(run, wireInput); err != nil || actual.Load() != 1 {
		t.Fatalf("authority positive requests=%d err=%v", actual.Load(), err)
	}
	var changed atomic.Bool
	var settingErr error
	changeRun := httptrace.WithClientTrace(run, &httptrace.ClientTrace{ConnectDone: func(_, _ string, e error) {
		if e == nil && changed.CompareAndSwap(false, true) {
			_, settingErr = second.Exec(ctx, `UPDATE system_settings SET payload=jsonb_set(payload,'{Models,Profiles,0,BaseURL}','"http://127.0.0.1:1"'),version=version+1 WHERE id=1`)
		}
	}})
	if _, err := actualRuntime.Run(changeRun, wireInput); !errors.Is(err, memorycontracts.ErrReadChanged) || actual.Load() != 1 || !changed.Load() || settingErr != nil {
		t.Fatalf("authority after route change requests=%d err=%v store=%v", actual.Load(), err, settingErr)
	}
	finish(j)
	t.Log("PG17 authoritative HTTP first write: approved=1; persisted route change during dial=0")
	// Bind a stored local fact and a cross-group fact to one ordinary model request.
	// 将持久化的原域事实和跨群事实绑定到同一个普通模型请求。
	exec(`UPDATE system_settings SET payload=$1,version=version+1 WHERE id=1`, payload)
	const localMid = -35501
	incoming(firstIngress, localMid, person, groupB, obdomain.Text("本群隔离事实来源"))
	localJob := claimOne(conv2, "")
	finish(localJob)
	var localID string
	if err := pool.QueryRow(ctx, `INSERT INTO memories(memory_id,bot_id,visibility,owner_key,content,origin,status,approved_by,source_message_id,source_sender_id)
 SELECT gen_random_uuid(),bot_id,'group',$2::bigint::text,'本群隔离事实','extraction','active','fixture',id,sender_id FROM messages WHERE bot_id=$1 AND platform_message_id=$3
 RETURNING memory_id::text`, bot, groupB, localMid).Scan(&localID); err != nil {
		t.Fatal(err)
	}
	j = jobFor(groupB, person, -35502)
	run = tracked(j)
	localReq := memorycontracts.DetailRequest{BotID: bot, Kind: "group", TargetID: groupB, ConversationID: j.ConversationID, SenderID: person, MemoryID: localID}
	localItem, ok, err := mem.RecallDetail(run, localReq)
	if err != nil || !ok || localItem.ConsentIdentity != "" || localItem.SourceMessageID <= 0 {
		t.Fatalf("local fixture: visible=%t err=%v", ok, err)
	}
	memorycontracts.TrackVersionedRead(run, mem, localReq, localItem, nil)
	run = memorycontracts.WithConsentDestination(run, func(call context.Context, d memorycontracts.GenerationDispatch) error {
		return memoryRepo.ValidateConsentGeneration(call, profile, d)
	})
	wireInput.Messages[0].Content = successor + "\n" + localItem.Content
	beforeMixed := actual.Load()
	if _, err := actualRuntime.Run(run, wireInput); err != nil || actual.Load() != beforeMixed+1 {
		t.Fatalf("mixed memory request: requests=%d err=%v", actual.Load()-beforeMixed, err)
	}
	exec(`DELETE FROM message_jobs WHERE incoming_message_id=$1`, localItem.SourceMessageID)
	if _, err := second.Exec(ctx, `DELETE FROM messages WHERE id=$1`, localItem.SourceMessageID); err != nil {
		t.Fatal(err)
	}
	if _, err := actualRuntime.Run(run, wireInput); !errors.Is(err, memorycontracts.ErrReadChanged) || actual.Load() != beforeMixed+1 {
		t.Fatalf("mixed memory request after ordinary source removal: requests=%d err=%v", actual.Load()-beforeMixed, err)
	}
	finish(j)
	t.Log("mixed A/B request: authorized source produced one loopback request; after ordinary retention removal the request was closed")

	p = arm("ordinary-recovery", -352)
	interrupted := errors.New("fixture worker stopped after committed reservation")
	var old outbounddomain.Record
	firstLedger := consentInterruptLedger{Coordinator: ledger, interrupt: interrupted, record: &old}
	firstReply, err := agentapp.NewReplier(agentapp.Options{Runtime: consentFixtureRuntime{liveRuntime}, Scopes: scopes, Memory: mem, Senders: accounts, Recorder: conversation, Outbound: firstLedger, Logger: logger})
	if err != nil {
		t.Fatal(err)
	}
	secondReply, err := agentapp.NewReplier(agentapp.Options{Runtime: consentFixtureRuntime{liveRuntime}, Scopes: scopes2, Memory: mem2, Senders: accounts, Recorder: conversation2, Outbound: ledger2, Logger: logger})
	if err != nil {
		t.Fatal(err)
	}
	const explicitText = "租约接管合成文字"
	const explicitSlot = "explicit-recovery"
	explicit := func(run context.Context, j conversationdomain.Job, l replytool.Ledger, slot string, text string, limits outbounddomain.Limits) error {
		scope := pluginsapp.Scope{BotID: bot, JobID: j.ID, ClaimToken: j.ClaimToken, ConversationID: j.ConversationID, Kind: "group", TargetID: groupB, Permissions: map[string]bool{"qq.reply.current": true}}
		_, err := replytool.SendPublic(pluginsapp.WithToolCallID(pluginsapp.WithScope(run, scope), slot), replytool.Options{Ledger: l, Limits: limits}, obdomain.PublicCommand{Name: obdomain.ActionSendGroup, GroupID: obdomain.GroupID(groupB), Text: &text}, wire.client.ExecutePublic)
		return err
	}
	for i, slot := range []string{"final", explicitSlot} {
		j = jobFor(groupB, person, int64(-353-i))
		before := len(wire.actions)
		if i == 0 {
			err = firstReply.ExecuteReply(ctx, makeTask(j), nil)
		} else {
			err = explicit(tracked(j), j, firstLedger, slot, explicitText, outbounddomain.Limits{MaxMessages: 1})
		}
		if !errors.Is(err, interrupted) || (i == 0 && old.ToolCallID != slot || i != 0 && !strings.HasPrefix(old.ToolCallID, "explicit:v1:")) || len(wire.actions) != before {
			t.Fatalf("entry interruption after reserve: slot=%s err=%v actions=%d", slot, err, len(wire.actions)-before)
		}
		usage(j.ID, 1)
		recoverJob(j)
		usage(j.ID, 0)
		next := claimOne(conv2, "")
		if next.ID != j.ID || next.ClaimToken == j.ClaimToken {
			t.Fatal("unexpected recovered claim")
		}
		if err := outbound.MarkSending(tracked(next), old.ID, j.ClaimToken); !errors.Is(err, outboundinfra.ErrLeaseLost) {
			t.Fatalf("late claim: %v", err)
		}
		if i != 0 {
			for _, newCall := range []string{slot, "new-framework-call"} {
				err = explicit(tracked(next), next, ledger2, newCall, explicitText, outbounddomain.Limits{MaxMessages: 1})
				if !errors.Is(err, outbounddomain.ErrConsentSendDenied) || len(wire.actions) != before {
					t.Fatalf("independent execution of explicit call: actions=%d err=%v", len(wire.actions)-before, err)
				}
			}
			rows, err := outbound2.ListByJob(ctx, next.ID)
			if err != nil || len(rows) != 1 || rows[0].ID != old.ID || rows[0].Sequence != old.Sequence {
				t.Fatalf("unverified execution changed the original slot: %v %v", rows, err)
			}
			usage(j.ID, 0)
			check(old, next, "cancelled")
			finish(next)
			continue
		}
		err = secondReply.ExecuteReply(ctx, makeTask(next), nil)
		if err != nil || len(wire.actions) != before+1 {
			t.Fatalf("recovered slot receipt: slot=%s err=%v actions=%d", slot, err, len(wire.actions)-before)
		}
		var gotID string
		var seq int
		if err = second.QueryRow(ctx, `SELECT id::text,sequence FROM agent_outbound_messages WHERE job_id=$1 AND tool_call_id=$2 AND claim_token=$3::uuid`, j.ID, old.ToolCallID, next.ClaimToken).Scan(&gotID, &seq); err != nil || gotID != old.ID || seq != old.Sequence {
			t.Fatalf("slot changed: slot=%s id=%s seq=%d err=%v", slot, gotID, seq, err)
		}
		if i == 0 {
			err = secondReply.ExecuteReply(ctx, makeTask(next), nil)
		} else {
			err = explicit(tracked(next), next, ledger, slot, explicitText, outbounddomain.Limits{MaxMessages: 1})
		}
		if err != nil || len(wire.actions) != before+1 {
			t.Fatalf("same claim repeated a settled action: slot=%s err=%v actions=%d", slot, err, len(wire.actions)-before)
		}
		usage(j.ID, 1)
		check(old, next, "sent")
		recoveryEvidence(old)
		finish(next)
	}
	t.Log("automatic reply reused the original slot across claims; independent explicit invocations kept the original slot cancelled and refunded")
	for i, slot := range []string{"final", explicitSlot} {
		j = jobFor(groupB, person, int64(-390-i*4))
		before := len(wire.actions)
		if i == 0 {
			err = firstReply.ExecuteReply(ctx, makeTask(j), nil)
		} else {
			err = explicit(tracked(j), j, firstLedger, slot, explicitText, outbounddomain.Limits{MaxMessages: 1})
		}
		if !errors.Is(err, interrupted) || (i == 0 && old.ToolCallID != slot || i != 0 && !strings.HasPrefix(old.ToolCallID, "explicit:v1:")) || len(wire.actions) != before {
			t.Fatalf("initial preparation for regrant: slot=%s err=%v", slot, err)
		}
		recoverJob(j)
		usage(j.ID, 0)
		next := claimOne(conv2, "")
		if next.ID != j.ID || next.ClaimToken == j.ClaimToken {
			t.Fatal("unexpected claim after regrant")
		}
		revoke(secondIngress, p, int64(-391-i*4), groupB)
		p = arm(fmt.Sprintf("regrant-after-recovery-%d", i), int64(-392-i*4))
		before = len(wire.actions)
		if i == 0 {
			err = secondReply.ExecuteReply(ctx, makeTask(next), nil)
		} else {
			err = explicit(tracked(next), next, ledger2, slot, explicitText, outbounddomain.Limits{MaxMessages: 1})
		}
		if err == nil || len(wire.actions) != before {
			t.Fatalf("regranted permit reused old slot: slot=%s err=%v actions=%d", slot, err, len(wire.actions)-before)
		}
		usage(j.ID, 0)
		check(old, next, "cancelled")
		finish(next)
	}
	t.Log("both recovered reply entries rejected the new permit instance after revoke and regrant with identical content: OneBot=0, usage=0")

	j = jobFor(groupB, person, -399)
	if err = explicit(tracked(j), j, firstLedger, explicitSlot, explicitText, outbounddomain.Limits{MaxMessages: 1}); !errors.Is(err, interrupted) {
		t.Fatalf("initial preparation for interval recovery: %v", err)
	}
	next := j
	waiting2 := consentWaitingLedger{Coordinator: ledger2, reserved: make(chan outbounddomain.Record, 1)}
	exec(`UPDATE agent_outbound_messages SET updated_at=clock_timestamp() WHERE id=$1 AND status='sent'`, settled)
	intervalDone := make(chan error, 1)
	newRun := tracked(next)
	before := len(wire.actions)
	go func() {
		intervalDone <- explicit(newRun, next, waiting2, explicitSlot, explicitText, outbounddomain.Limits{MaxMessages: 1, MinIntervalMS: 6000})
	}()
	select {
	case prepared := <-waiting2.reserved:
		if prepared.ID != old.ID || prepared.Sequence != old.Sequence || !prepared.NotBefore.After(time.Now()) {
			t.Fatal("original slot did not enter the interval wait")
		}
	case err := <-intervalDone:
		t.Fatalf("same invocation finished before interval wait: %v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("same invocation did not reach the interval")
	}
	revoke(secondIngress, p, -400, groupB)
	if err = <-intervalDone; !errors.Is(err, memorycontracts.ErrReadChanged) || len(wire.actions) != before {
		t.Fatalf("revocation during interval: err=%v actions=%d", err, len(wire.actions)-before)
	}
	usage(j.ID, 0)
	check(old, next, "cancelled")
	finish(next)
	t.Log("same explicit invocation re-entered the original slot; revocation cancelled it without sending")
	// The two separately granted facts must survive one coherent, post-lock read.
	// 两条各自获准的事实必须同时通过加锁后的同一次读取。
	mainID := id
	p = arm("first-write-dependency-a", -401)
	const secondFact = "第二条隔离授权事实"
	id = seedFact(secondFact, -402, memorydomain.StatusActive)
	p2 := grant("first-write-dependency-b", 1, -403)
	var secondSource int64
	if err := pool.QueryRow(ctx, `SELECT source_message_id FROM memory_consent_proposals WHERE id=$1`, p2.ID).Scan(&secondSource); err != nil {
		t.Fatal(err)
	}
	secondID := id
	id = mainID
	j = jobFor(groupB, person, -404)
	run = tracked(j)
	secondRequest := memorycontracts.DetailRequest{BotID: bot, Kind: "group", TargetID: groupB, ConversationID: j.ConversationID, MemoryID: secondID}
	item, ok, err := mem2.RecallDetail(run, secondRequest)
	if err != nil || !ok || item.ConsentIdentity == "" {
		t.Fatalf("second fact not available: %v %v", ok, err)
	}
	memorycontracts.TrackVersionedRead(run, mem2, secondRequest, item, nil)
	if got := len(memorycontracts.ConsentReads(run)); got != 2 {
		t.Fatalf("got %d tracked dependencies, want two", got)
	}
	exec(`UPDATE system_settings SET payload=$1,version=version+1 WHERE id=1`, payload)
	validate := func(call context.Context, d memorycontracts.GenerationDispatch) error {
		return memoryRepo.ValidateConsentGeneration(call, profile, d)
	}
	run = memorycontracts.WithConsentDestination(run, validate)
	wireInput.Messages[0].Content = successor + "\n" + secondFact
	beforeModel := actual.Load()
	if _, err := actualRuntime.Run(run, wireInput); err != nil || actual.Load() != beforeModel+1 {
		t.Fatalf("two-dependency dispatch positive: requests=%d err=%v", actual.Load()-beforeModel, err)
	}
	var removed atomic.Bool
	var removeErr error
	removeRun := httptrace.WithClientTrace(run, &httptrace.ClientTrace{ConnectDone: func(_, _ string, e error) {
		if e == nil && removed.CompareAndSwap(false, true) {
			_, removeErr = second.Exec(ctx, `UPDATE messages SET created_at=clock_timestamp()-interval '90 days' WHERE id=$1`, secondSource)
			if removeErr == nil {
				var count int
				count, removeErr = conv2.PurgeBefore(ctx, time.Now().Add(-60*24*time.Hour), 1)
				if removeErr == nil && count != 1 {
					removeErr = fmt.Errorf("expected one expired source, deleted %d", count)
				}
			}
		}
	}})
	if _, err := actualRuntime.Run(removeRun, wireInput); !errors.Is(err, memorycontracts.ErrReadChanged) || actual.Load() != beforeModel+1 || !removed.Load() || removeErr != nil {
		t.Fatalf("source removal before physical first write: requests=%d err=%v cleanup=%v", actual.Load()-beforeModel, err, removeErr)
	}
	if len(read(p).ActiveGroups) != 2 {
		t.Fatal("cleanup changed the other valid dependency")
	}
	finish(j)
	t.Log("two tracked B dependencies under PostgreSQL: valid=1 HTTP request; deleting only the second source before physical first write=0 HTTP requests")
	if certFile := os.Getenv("HAP52B_TLS_CERT"); certFile != "" {
		pair, err := tls.LoadX509KeyPair(certFile, os.Getenv("HAP52B_TLS_KEY"))
		if err != nil {
			t.Fatal(err)
		}
		var tlsCalls atomic.Int32
		tlsServer := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			tlsCalls.Add(1)
			if r.TLS == nil {
				t.Error("missing TLS in loopback generation request")
			}
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"id":"tls-fixture","choices":[{"index":0,"message":{"role":"assistant","content":"通过"},"finish_reason":"stop"}]}`)
		}))
		tlsServer.TLS = &tls.Config{Certificates: []tls.Certificate{pair}, MinVersion: tls.VersionTLS12}
		tlsServer.StartTLS()
		defer tlsServer.Close()
		tlsProfile := profile
		tlsProfile.BaseURL = tlsServer.URL
		modelSettings.Models.Profiles[0] = tlsProfile
		tlsPayload, _ := json.Marshal(modelSettings)
		exec(`UPDATE system_settings SET payload=$1,version=version+1 WHERE id=1`, tlsPayload)
		j = jobFor(groupB, person, -405)
		run = memorycontracts.WithConsentDestination(tracked(j), func(call context.Context, d memorycontracts.GenerationDispatch) error {
			return memoryRepo.ValidateConsentGeneration(call, tlsProfile, d)
		})
		tlsRuntime, err := agentinfra.NewRuntime(ctx, agentinfra.Config{BaseURL: tlsServer.URL, APIKey: "fixture", Model: tlsProfile.Model})
		if err != nil {
			t.Fatal(err)
		}
		wireInput.Messages[0].Content = successor
		if _, err := tlsRuntime.Run(run, wireInput); err != nil || tlsCalls.Load() != 1 {
			t.Fatalf("real PostgreSQL and TLS positive: requests=%d err=%v", tlsCalls.Load(), err)
		}
		var handshake atomic.Bool
		var revokeErr error
		tlsBlocked := httptrace.WithClientTrace(run, &httptrace.ClientTrace{TLSHandshakeDone: func(state tls.ConnectionState, err error) {
			if err == nil && state.HandshakeComplete && handshake.CompareAndSwap(false, true) {
				revokeErr = memory2.RevokeConsent(ctx, bot, p.ID, p.StateVersion, groupB)
			}
		}})
		if _, err := tlsRuntime.Run(tlsBlocked, wireInput); !errors.Is(err, memorycontracts.ErrReadChanged) || tlsCalls.Load() != 1 || !handshake.Load() || revokeErr != nil {
			t.Fatalf("real PostgreSQL revoke after TLS: requests=%d handshake=%v revoke=%v err=%v", tlsCalls.Load(), handshake.Load(), revokeErr, err)
		}
		finish(j)
		t.Log("real PostgreSQL plus loopback HTTPS: authorized=1, alternate pool committed revoke after TLS handshake=0")
	}
	t.Logf("total OneBot actions=%d, actual generation requests=%d", len(wire.actions), modelCount)
}

type pictureFixtureRuntime struct{ agentcontracts.Runtime }

func (r pictureFixtureRuntime) Run(ctx context.Context, req agentcontracts.Request) (agentcontracts.Result, error) {
	req.ModelImages = true
	return r.Runtime.Run(ctx, req)
}

type historyPictureAccount struct {
	reader mediainfra.Reader
}

func (a historyPictureAccount) MediaReader(int64) (mediainfra.Reader, bool) {
	return a.reader, true
}

type historyPictureBridge struct {
	lookup func(context.Context, string) (obdomain.FileResult, error)
}

func (b historyPictureBridge) GetImage(ctx context.Context, file string) (obdomain.FileResult, error) {
	return b.lookup(ctx, file)
}

func (historyPictureBridge) GetRecord(context.Context, string, string) (obdomain.FileResult, error) {
	return obdomain.FileResult{}, errors.New("unexpected voice lookup")
}

func TestReplyReadsEachPersistedHistoryPictureWithCurrentPolicy(t *testing.T) {
	pool := dbtest.Open(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	repo, err := conversationinfra.NewRepo(pool)
	if err != nil {
		t.Fatal(err)
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	conversations, err := conversationapp.NewService(repo, logger)
	if err != nil {
		t.Fatal(err)
	}
	scopeRepo, _ := scopeinfra.NewRepo(pool)
	scopes, _ := scopeapp.NewService(scopeRepo)
	ledgerRepo, _ := outboundinfra.NewRepo(pool)
	ledger, _ := outboundapp.NewCoordinator(ledgerRepo)
	accounts := svc.NewAccounts()
	bot := time.Now().UnixMicro()
	const group, person = 56101, 56102
	if _, err := pool.Exec(ctx, `INSERT INTO bot_accounts(bot_id) VALUES($1)`, bot); err != nil {
		t.Fatal(err)
	}
	wire := &consentWire{}
	wire.client = obapp.NewClientService(wire, 5*time.Second, logger)
	if err := wire.client.BindIdentity(obdomain.BotID(bot)); err != nil {
		t.Fatal(err)
	}
	if err := accounts.Add(svc.NewAccountConnection("fixture", bot, wire.client)); err != nil {
		t.Fatal(err)
	}
	policy := func(ctx context.Context, allowed bool) error {
		_, err := pool.Exec(ctx, `INSERT INTO scope_policies(bot_id,scope_type,scope_id,enabled,store_messages,allow_history_read,message_interval_ms)
 VALUES($1,'group',$2,true,true,$3,0) ON CONFLICT(bot_id,scope_type,scope_id) DO UPDATE SET allow_history_read=excluded.allow_history_read`, bot, group, allowed)
		return err
	}
	const fixturePNG = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+jP5sAAAAASUVORK5CYII="
	picture, err := base64.StdEncoding.DecodeString(fixturePNG)
	if err != nil {
		t.Fatal(err)
	}
	for i, mode := range []string{"all-allowed", "after-first-picture", "after-first-lookup"} {
		t.Run(mode, func(t *testing.T) {
			if err := policy(ctx, true); err != nil {
				t.Fatal(err)
			}
			var currentDownloads, firstDownloads, secondDownloads, picturesInModel atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/current.png":
					currentDownloads.Add(1)
				case "/one.png":
					firstDownloads.Add(1)
					if mode == "after-first-picture" {
						if err := policy(r.Context(), false); err != nil {
							t.Error(err)
						}
					}
				case "/two.png":
					secondDownloads.Add(1)
				case "/chat/completions":
					var payload struct {
						Messages []struct{ Content json.RawMessage }
					}
					if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
						t.Error(err)
					}
					for _, message := range payload.Messages {
						var parts []struct{ Type string }
						if json.Unmarshal(message.Content, &parts) == nil {
							for _, part := range parts {
								if part.Type == "image_url" {
									picturesInModel.Add(1)
								}
							}
						}
					}
					w.Header().Set("Content-Type", "application/json")
					fmt.Fprint(w, `{"id":"fixture","model":"fixture","choices":[{"index":0,"finish_reason":"stop","message":{"role":"assistant","content":"图片收到了"}}]}`)
					return
				default:
					t.Errorf("unexpected loopback path %q", r.URL.Path)
					http.NotFound(w, r)
					return
				}
				w.Header().Set("Content-Type", "image/png")
				_, _ = w.Write(picture)
			}))
			defer server.Close()
			var lookups []string
			media := mediainfra.NewService(nil, historyPictureAccount{reader: historyPictureBridge{lookup: func(call context.Context, file string) (obdomain.FileResult, error) {
				lookups = append(lookups, file)
				if mode == "after-first-lookup" && file == "one" {
					if err := policy(call, false); err != nil {
						return obdomain.FileResult{}, err
					}
				}
				return obdomain.FileResult{URL: server.URL + "/" + file + ".png"}, nil
			}}}, t.TempDir())
			media.HTTP = server.Client()
			runtime, err := agentinfra.NewRuntime(ctx, agentinfra.Config{BaseURL: server.URL, APIKey: "fixture", Model: "fixture"})
			if err != nil {
				t.Fatal(err)
			}
			replier, err := agentapp.NewReplier(agentapp.Options{Runtime: pictureFixtureRuntime{runtime}, Media: media, HistoryImages: repo.HistoryImages, Scopes: scopes, Senders: accounts, Recorder: conversations, Outbound: ledger, Logger: logger})
			if err != nil {
				t.Fatal(err)
			}
			historyID := int64(99170 + i*10)
			stored, err := repo.Ingest(ctx, conversationdomain.IngestInput{BotID: bot, Kind: conversationdomain.KindGroup, TargetID: group, PlatformMessageID: historyID, SenderID: person, Text: "历史两张图", ArchiveOnly: true,
				Segments: []obdomain.Segment{obdomain.Text("历史两张图"), obdomain.Image("one"), obdomain.Image("two")}})
			if err != nil {
				t.Fatal(err)
			}
			current := []obdomain.Segment{obdomain.Text("看看图片"), {Type: obdomain.SegImage, Data: map[string]string{"file": "current", "url": server.URL + "/current.png"}}}
			messageID := historyID + 1
			in, err := repo.Ingest(ctx, conversationdomain.IngestInput{BotID: bot, Kind: conversationdomain.KindGroup, TargetID: group, PlatformMessageID: messageID, SenderID: person, Text: "看看图片", Segments: current})
			if err != nil || in.ConversationID != stored.ConversationID {
				t.Fatalf("ingest current: %+v %v", in, err)
			}
			claim := dbtest.Claim(t, pool, in.JobID)
			task := intentdomain.Task{BotID: bot, JobID: in.JobID, ClaimToken: claim, ConversationID: in.ConversationID, Kind: "group", TargetID: group, Segments: current,
				Message: intentdomain.Message{MessageID: messageID, SenderID: person, Text: "看看图片", Segments: current}}
			history := []intentdomain.Message{{MessageID: historyID, SenderID: person, Text: "历史两张图", Segments: []obdomain.Segment{obdomain.Image("stale-view")}}}
			before := len(wire.actions)
			if err := replier.ExecuteReply(ctx, task, history); err != nil {
				t.Fatal(err)
			}
			wantLookups, wantFirst, wantSecond, wantModel := "one,two", int32(1), int32(1), int32(3)
			switch mode {
			case "after-first-picture":
				wantLookups, wantSecond, wantModel = "one", 0, 2
			case "after-first-lookup":
				wantLookups, wantFirst, wantSecond, wantModel = "one", 0, 0, 1
			}
			if strings.Join(lookups, ",") != wantLookups || firstDownloads.Load() != wantFirst || secondDownloads.Load() != wantSecond || currentDownloads.Load() != 1 || picturesInModel.Load() != wantModel || len(wire.actions) != before+1 {
				t.Fatalf("lookups=%v downloads current/first/second=%d/%d/%d model pictures=%d sends=%d", lookups, currentDownloads.Load(), firstDownloads.Load(), secondDownloads.Load(), picturesInModel.Load(), len(wire.actions)-before)
			}
			t.Logf("lookups=%v downloads current/first/second=%d/%d/%d model pictures=%d sends=%d", lookups, currentDownloads.Load(), firstDownloads.Load(), secondDownloads.Load(), picturesInModel.Load(), len(wire.actions)-before)
		})
	}
}
