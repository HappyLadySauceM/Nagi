//go:build integration

package application

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	agentcontracts "github.com/HappyLadySauceM/Nagi/internal/agent/contracts"
	agentruntime "github.com/HappyLadySauceM/Nagi/internal/agent/infrastructure/eino"
	audit "github.com/HappyLadySauceM/Nagi/internal/audit/contracts"
	auditdomain "github.com/HappyLadySauceM/Nagi/internal/audit/domain"
	auditpg "github.com/HappyLadySauceM/Nagi/internal/audit/infrastructure"
	intentdomain "github.com/HappyLadySauceM/Nagi/internal/intent/domain"
	memoryapp "github.com/HappyLadySauceM/Nagi/internal/memory/application"
	"github.com/HappyLadySauceM/Nagi/internal/memory/contracts"
	"github.com/HappyLadySauceM/Nagi/internal/memory/domain"
	memoryinfra "github.com/HappyLadySauceM/Nagi/internal/memory/infrastructure"
	pluginsapp "github.com/HappyLadySauceM/Nagi/internal/plugins/application"
	plugincontracts "github.com/HappyLadySauceM/Nagi/internal/plugins/contracts"
	"github.com/HappyLadySauceM/Nagi/internal/plugins/infrastructure/builtin"
	plugineino "github.com/HappyLadySauceM/Nagi/internal/plugins/infrastructure/eino"
	scopeapp "github.com/HappyLadySauceM/Nagi/internal/scope/application"
	scopedomain "github.com/HappyLadySauceM/Nagi/internal/scope/domain"
	scopeinfra "github.com/HappyLadySauceM/Nagi/internal/scope/infrastructure"
	"github.com/HappyLadySauceM/Nagi/pkg/db/dbtest"
	"github.com/cloudwego/eino/components/tool"
)

func TestHAP52MemoryVisibilityAtGeneration(t *testing.T) {
	ctx := context.Background()
	pool := dbtest.Open(t)
	repo, err := memoryinfra.NewRepo(pool)
	if err != nil {
		t.Fatal(err)
	}
	service, err := memoryapp.NewService(repo)
	if err != nil {
		t.Fatal(err)
	}
	policyRepo, err := scopeinfra.NewRepo(pool)
	if err != nil {
		t.Fatal(err)
	}
	scopes, err := scopeapp.NewService(policyRepo)
	if err != nil {
		t.Fatal(err)
	}
	service.SetReadPolicies(scopes)
	const bot, otherBot int64 = 982502, 982503
	const groupA, groupB, groupC int64 = 982511, 982512, 982513
	if _, err := pool.Exec(ctx, `INSERT INTO bot_accounts(bot_id,nickname) VALUES ($1,'gold-a'),($2,'gold-b') ON CONFLICT DO NOTHING`, bot, otherBot); err != nil {
		t.Fatal(err)
	}
	type scopeKey struct {
		bot, target int64
		kind        string
	}
	conversations := map[scopeKey]string{}
	setPolicy := func(k scopeKey, read bool) {
		t.Helper()
		p := scopedomain.Default(scopedomain.ScopeType(k.kind), k.target)
		p.Enabled, p.AllowMemoryRead, p.StoreMessages = true, read, true
		if err := policyRepo.Upsert(ctx, k.bot, p); err != nil {
			t.Fatal(err)
		}
	}
	for _, k := range []scopeKey{{bot, groupA, "group"}, {bot, groupB, "group"}, {bot, groupC, "group"}, {otherBot, groupA, "group"}, {bot, 10101, "private"}} {
		var id string
		if err := pool.QueryRow(ctx, `INSERT INTO conversations(id,bot_id,kind,target_id) VALUES(gen_random_uuid(),$1,$2,$3) RETURNING id::text`, k.bot, k.kind, k.target).Scan(&id); err != nil {
			t.Fatal(err)
		}
		conversations[k] = id
		setPolicy(k, true)
	}
	type fact struct {
		key                  string
		visibility           domain.Visibility
		owner, subject, text string
	}
	facts := []fact{
		{"opted", domain.VisibilityGroup, "982511", "10101", "霁蓝称呼，本人明确允许在乙群使用"},
		{"local", domain.VisibilityGroup, "982511", "10101", "茜红称呼，本人仅允许在甲群使用"},
		{"namesake", domain.VisibilityGroup, "982511", "10102", "霁蓝称呼，甲群另一位同名成员"},
		{"reported", domain.VisibilityGroup, "982511", "10103", "银狐称呼，第三人转述"},
		{"private", domain.VisibilityPrivateUser, "10101", "10101", "墨绿称呼，私聊事实"},
		{"group", domain.VisibilityGroup, "982511", "982511", "金橙约定，甲群共同事实"},
		{"legacy_shared", domain.VisibilityBotShared, "", "10101", "苍紫称呼，管理员批准但无本人授权"},
		{"correction", domain.VisibilityGroup, "982511", "10101", "湖青称呼，更正原称呼"},
	}
	ids := map[string]string{}
	texts := map[string]string{}
	for _, f := range facts {
		subject := domain.SubjectUser
		if f.key == "group" {
			subject = domain.SubjectGroup
		}
		var id string
		if f.visibility == domain.VisibilityBotShared {
			id, err = repo.Insert(ctx, domain.Memory{BotID: bot, Visibility: f.visibility, SubjectType: subject, SubjectID: f.subject, Content: f.text, Status: domain.StatusActive, ApprovedBy: "fixture-admin", Confidence: 0.9})
		} else {
			id, _, err = service.Propose(ctx, memoryapp.CandidateInput{BotID: bot, Visibility: f.visibility, OwnerKey: f.owner, SubjectType: subject, SubjectID: f.subject, Content: f.text, Confidence: 0.9})
			if err == nil && f.key != "correction" {
				err = service.Approve(ctx, id, "fixture-admin")
			}
		}
		if err != nil {
			t.Fatalf("%s: %v", f.key, err)
		}
		ids[f.key], texts[f.key] = id, f.text
	}
	if _, _, found, err := service.Management(ctx, ids["legacy_shared"], bot); err != nil || !found {
		t.Fatalf("admin legacy found=%v err=%v", found, err)
	}
	toolset, err := builtin.NewMemoryProvider(service, nil).Tools(ctx)
	if err != nil {
		t.Fatal(err)
	}
	detail := toolset[0]
	var wireMu sync.Mutex
	var wire []string
	var onFirst func(http.ResponseWriter, *http.Request)
	var afterTool func(context.Context) error
	var beforeModel func(context.Context)
	var invoked atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Error(err)
			return
		}
		wireMu.Lock()
		wire = append(wire, string(body))
		first := len(wire) == 1
		callback := onFirst
		wireMu.Unlock()
		if first && callback != nil {
			callback(w, r)
			return
		}
		var request struct{ Stream bool }
		if err := json.Unmarshal(body, &request); err != nil {
			t.Error(err)
			return
		}
		if request.Stream {
			w.Header().Set("Content-Type", "text/event-stream")
			fmt.Fprint(w, "data: {\"id\":\"reply\",\"model\":\"fixture\",\"object\":\"chat.completion.chunk\",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"content\":\"fixture reply\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
		} else {
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"id":"reply","model":"fixture","object":"chat.completion","choices":[{"index":0,"message":{"role":"assistant","content":"fixture reply"},"finish_reason":"stop"}]}`)
		}

	}))
	defer server.Close()
	readWire := func() []string { wireMu.Lock(); defer wireMu.Unlock(); return append([]string(nil), wire...) }
	reset := func(callback func(http.ResponseWriter, *http.Request)) {
		wireMu.Lock()
		defer wireMu.Unlock()
		wire = nil
		onFirst = callback
	}
	runtime, err := agentruntime.NewRuntime(ctx, agentruntime.Config{BaseURL: server.URL, APIKey: "fixture", Model: "fixture", Timeout: 5 * time.Second, Tools: func(call context.Context, req agentcontracts.Request) []tool.BaseTool {
		if beforeModel != nil {
			beforeModel(call)
		}
		if !req.AllowMemoryRead {
			return nil
		}
		scope := pluginsapp.Scope{BotID: req.BotID, Kind: req.Kind, TargetID: req.TargetID, ConversationID: req.ConversationID, SenderID: req.SenderID, AllowMemoryRead: true, Reauthorize: func(call context.Context, _ plugincontracts.ToolDescriptor) error {
			p, err := scopes.Resolve(call, req.BotID, scopedomain.ScopeType(req.Kind), req.TargetID)
			if err != nil || !p.AllowMemoryRead {
				return errors.New("fixture memory permission denied")
			}
			return nil
		}}
		guarded := detail
		invoke := guarded.Invoke
		guarded.Invoke = func(call context.Context, args string) (string, error) {
			result, err := invoke(pluginsapp.WithScope(call, scope), args)
			if err == nil && (strings.Contains(result, texts["local"]) || strings.Contains(result, texts["legacy_shared"])) {
				invoked.Add(1)
				if afterTool != nil {
					err = afterTool(call)
				}
			}
			return result, err
		}
		return plugineino.ToBaseTools([]plugincontracts.RegisteredTool{guarded})
	}})
	if err != nil {
		t.Fatal(err)
	}
	sender := &fakeSender{msgID: 1}
	reply, err := NewReplier(Options{Runtime: runtime, Memory: service, Scopes: scopes, Senders: sender, Recorder: &fakeRecorder{}, Outbound: &fakeLedger{}, Logger: replierLogger()})
	if err != nil {
		t.Fatal(err)
	}
	var job int64
	run := func(k scopeKey, senderID int64, query string) error {
		job++
		return reply.ExecuteReply(ctx, intentdomain.Task{JobID: job, BotID: k.bot, Kind: k.kind, TargetID: k.target, ConversationID: conversations[k], Message: intentdomain.Message{MessageID: job, SenderID: senderID, Text: query}}, nil)
	}
	type probe struct {
		name                           string
		account, target                int64
		kind, typ, subject, query, key string
		gold, observed                 bool
	}
	probes := []probe{
		{"opted_other_group", bot, groupB, "group", "user", "10101", "霁蓝", "opted", true, false},
		{"same_opted_fact_in_unapproved_group", bot, groupC, "group", "user", "10101", "霁蓝", "opted", false, false},
		{"opted_origin_group", bot, groupA, "group", "user", "10101", "霁蓝", "opted", true, true},
		{"no_cross_group_permission", bot, groupB, "group", "user", "10101", "茜红", "local", false, false},
		{"different_person_same_name", bot, groupA, "group", "user", "10102", "霁蓝", "namesake", true, true},
		{"third_person_report", bot, groupB, "group", "user", "10103", "银狐", "reported", false, false},
		{"group_fact_stays_local", bot, groupB, "group", "group", "982512", "金橙", "group", false, false},
		{"private_stays_private", bot, groupB, "group", "user", "10101", "墨绿", "private", false, false},
		{"private_origin", bot, 10101, "private", "user", "10101", "墨绿", "private", true, true},
		{"cross_account", otherBot, groupA, "group", "user", "10101", "霁蓝", "opted", false, false},
		{"legacy_shared_without_consent", bot, groupB, "group", "user", "10101", "苍紫", "legacy_shared", false, true},
		{"literal_paraphrase_without_rewriter", bot, groupA, "group", "user", "10101", "天青称谓", "opted", true, false},
	}
	var negative, correct int
	for _, q := range probes {
		k := scopeKey{q.account, q.target, q.kind}
		senderID, _ := strconv.ParseInt(q.subject, 10, 64)
		recall, err := service.Recall(ctx, contracts.RecallRequest{BotID: q.account, Kind: q.kind, TargetID: q.target, ConversationID: conversations[k], SubjectType: q.typ, SubjectID: q.subject, Query: q.query})
		if err != nil {
			t.Fatal(err)
		}
		detailResult, err := detail.Invoke(pluginsapp.WithScope(ctx, pluginsapp.Scope{BotID: q.account, Kind: q.kind, TargetID: q.target, ConversationID: conversations[k], AllowMemoryRead: true}), fmt.Sprintf(`{"memory_id":%q}`, ids[q.key]))
		if err != nil {
			t.Fatal(err)
		}
		reset(nil)
		query := q.query
		if q.typ == "user" {
			query = "我叫" + query
		}
		if err := run(k, senderID, query); err != nil {
			t.Fatalf("%s: %v (received requests=%d)", q.name, err, len(readWire()))
		}
		requests := readWire()
		if len(requests) != 1 {
			t.Fatalf("%s requests=%d", q.name, len(requests))
		}
		recalled := len(recall.Items) == 1 && recall.Items[0].MemoryID == ids[q.key]
		disclosed := strings.Contains(detailResult, texts[q.key])
		transmitted := strings.Contains(requests[0], texts[q.key])
		if !q.gold {
			negative++
			if len(recall.Items) != 0 || disclosed || transmitted {
				t.Errorf("%s recall=%t detail=%t generation=%t", q.name, recalled, disclosed, transmitted)
			}
		} else if q.observed {
			if !recalled || !disclosed || !transmitted {
				t.Errorf("%s original positive recall=%t detail=%t generation=%t", q.name, recalled, disclosed, transmitted)
			} else {
				correct++
			}
		}
		t.Logf("%s recall=%t detail=%t generation=%t", q.name, recalled, disclosed, transmitted)
	}
	if negative != 7 || correct != 3 {
		t.Fatalf("negatives=%d original positives=%d", negative, correct)
	}
	t.Log("negative recall=0/7 detail=0/7 generation=0/7; original positives=3/3")
	origin := scopeKey{bot, groupA, "group"}
	var sourceID int64
	if err := pool.QueryRow(ctx, `INSERT INTO messages(conversation_id,bot_id,direction,sender_id,text_content) VALUES($1,$2,'incoming',10101,'synthetic source') RETURNING id`, conversations[origin], bot).Scan(&sourceID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE memories SET source_message_id=$1,source_sender_id=10101 WHERE memory_id=$2`, sourceID, ids["legacy_shared"]); err != nil {
		t.Fatal(err)
	}
	for _, k := range []scopeKey{origin, {bot, groupB, "group"}, {otherBot, groupA, "group"}} {
		reset(nil)
		if err := run(k, 10101, "我叫苍紫"); err != nil {
			t.Fatal(err)
		}
		if wire := readWire(); len(wire) != 1 || strings.Contains(wire[0], texts["legacy_shared"]) != (k == origin) {
			t.Fatalf("legacy original source %v capture count=%d", k, len(wire))
		}
	}
	t.Log("verified legacy source visible only in exact origin")
	setPolicy(origin, false)
	reset(nil)
	if err := run(origin, 10101, "我叫苍紫"); err != nil {
		t.Fatal(err)
	}
	if wire := readWire(); len(wire) != 1 || strings.Contains(wire[0], texts["legacy_shared"]) {
		t.Fatal("revoked scope leaked on fresh generation")
	}
	setPolicy(origin, true)
	// The first response requests detail. A later tool invocation changes the approved row
	// before a second model submission, so the retained detail is never transmitted.
	// 首个响应读取详情。下一次模型提交前更正或撤回已经读取的记忆，旧详情不会被发送。
	toolResponse := func(w http.ResponseWriter, req *http.Request) {
		args := fmt.Sprintf(`{"memory_id":%q}`, ids["local"])
		calls := fmt.Sprintf(`[{"index":0,"id":"call-1","type":"function","function":{"name":"memory_detail","arguments":%q}}]`, args)
		if strings.Contains(req.Header.Get("Accept"), "text/event-stream") {
			w.Header().Set("Content-Type", "text/event-stream")
			fmt.Fprintf(w, "data: {\"id\":\"reply\",\"model\":\"fixture\",\"object\":\"chat.completion.chunk\",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"tool_calls\":%s},\"finish_reason\":\"tool_calls\"}]}\n\ndata: [DONE]\n\n", calls)
		} else {
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprintf(w, `{"id":"reply","model":"fixture","object":"chat.completion","choices":[{"index":0,"message":{"role":"assistant","tool_calls":%s},"finish_reason":"tool_calls"}]}`, calls)
		}
	}
	for _, mode := range []string{"healthy", "revoked", "storage_disabled", "revised", "deleted"} {
		invoked.Store(0)
		switch mode {
		case "revoked":
			afterTool = func(call context.Context) error {
				policy := scopedomain.Default(scopedomain.ScopeGroup, groupA)
				policy.Enabled, policy.StoreMessages = true, true
				return policyRepo.Upsert(call, bot, policy)
			}
		case "storage_disabled":
			afterTool = func(call context.Context) error {
				policy := scopedomain.Default(scopedomain.ScopeGroup, groupA)
				policy.Enabled, policy.StoreMessages, policy.AllowMemoryRead, policy.AllowHistoryRead = true, false, true, true
				return policyRepo.Upsert(call, bot, policy)
			}
		case "revised":
			afterTool = func(call context.Context) error {
				return service.Revise(call, ids["local"], "茜红称呼，新的合成版本", "", nil)
			}
		case "deleted":
			afterTool = func(call context.Context) error { return service.Delete(call, ids["local"]) }
		}
		reset(toolResponse)
		err := run(origin, 10101, "请调用工具查询")
		requests := readWire()
		if invoked.Load() != 1 {
			t.Fatalf("%s tool result did not contain the original fact, err=%v, calls=%d", mode, err, len(requests))
		}
		if mode == "healthy" {
			if err != nil || len(requests) != 2 || strings.Contains(requests[0], texts["local"]) || !strings.Contains(requests[1], texts["local"]) {
				t.Fatalf("healthy tool pass: err=%v requests=%d", err, len(requests))
			}
		} else if !errors.Is(err, contracts.ErrReadChanged) || len(requests) != 1 || strings.Contains(requests[0], texts["local"]) {
			t.Fatalf("%s stale tool context: err=%v requests=%d", mode, err, len(requests))
		}
		afterTool = nil
		if mode == "revoked" || mode == "storage_disabled" {
			setPolicy(origin, true)
		}
		if mode == "revised" {
			if err := service.Revise(ctx, ids["local"], texts["local"], "", nil); err != nil {
				t.Fatal(err)
			}
		}
		t.Logf("retained tool context %s model_requests=%d", mode, len(requests))
	}
	reset(nil)
	beforeModel = func(call context.Context) {
		p := scopedomain.Default(scopedomain.ScopeGroup, groupA)
		p.Enabled, p.StoreMessages, p.AllowMemoryRead, p.AllowHistoryRead = true, false, true, true
		if err := policyRepo.Upsert(call, bot, p); err != nil {
			t.Error(err)
		}
	}
	if err := run(origin, 10101, "我叫苍紫"); !errors.Is(err, contracts.ErrReadChanged) || len(readWire()) != 0 {
		t.Fatalf("storage disabled after recall: err=%v requests=%d", err, len(readWire()))
	}
	beforeModel = nil
	setPolicy(origin, true)
	t.Log("storage disabled after recall: model_requests=0")
	const historyMarker = "synthetic previous message 52"
	for _, allowHistory := range []bool{false, true} {
		policy := scopedomain.Default(scopedomain.ScopeGroup, groupA)
		policy.Enabled, policy.StoreMessages, policy.AllowHistoryRead = true, true, allowHistory
		if err := policyRepo.Upsert(ctx, bot, policy); err != nil {
			t.Fatal(err)
		}
		reset(nil)
		job++
		err := reply.ExecuteReply(ctx, intentdomain.Task{JobID: job, BotID: bot, Kind: "group", TargetID: groupA,
			ConversationID: conversations[origin], Message: intentdomain.Message{MessageID: job, SenderID: 10101, Text: "我叫苍紫"}},
			[]intentdomain.Message{{SenderID: 10101, Text: historyMarker}})
		requests := readWire()
		if err != nil || len(requests) != 1 || strings.Contains(requests[0], historyMarker) != allowHistory || strings.Contains(requests[0], texts["legacy_shared"]) {
			t.Fatalf("history independence allow=%v: err=%v, requests=%d", allowHistory, err, len(requests))
		}
	}
	setPolicy(origin, true)
	t.Log("history toggles independently with memory read disabled")
	legacyID := ids["legacy_shared"]
	assertLegacyReads := func(want bool) {
		t.Helper()
		recall, err := service.Recall(ctx, contracts.RecallRequest{BotID: bot, Kind: "group", TargetID: groupA,
			ConversationID: conversations[origin], Query: "苍紫", SubjectType: "user", SubjectID: "10101"})
		if err != nil {
			t.Fatal(err)
		}
		found := false
		for _, item := range recall.Items {
			found = found || item.MemoryID == legacyID
		}
		if found != want {
			t.Fatalf("legacy recall want=%v, items=%d", want, len(recall.Items))
		}
		toolScope := pluginsapp.Scope{BotID: bot, Kind: "group", TargetID: groupA, ConversationID: conversations[origin], AllowMemoryRead: true}
		result, err := detail.Invoke(pluginsapp.WithScope(ctx, toolScope), fmt.Sprintf(`{"memory_id":%q}`, legacyID))
		if err != nil || strings.Contains(result, texts["legacy_shared"]) != want {
			t.Fatalf("legacy detail want=%v err=%v", want, err)
		}
	}
	assertLegacyReads(true)
	for _, clear := range []bool{false, true} {
		var messageID, jobID int64
		if err := pool.QueryRow(ctx, `INSERT INTO messages(conversation_id,bot_id,direction,sender_id,text_content) VALUES($1,$2,'incoming',10101,'隔离提交闸门') RETURNING id`, conversations[origin], bot).Scan(&messageID); err != nil {
			t.Fatal(err)
		}
		if err := pool.QueryRow(ctx, `INSERT INTO message_jobs(incoming_message_id,status) VALUES($1,'done') RETURNING id`, messageID).Scan(&jobID); err != nil {
			t.Fatal(err)
		}
		recorder, err := auditpg.NewAIRecorder(pool)
		if err != nil {
			t.Fatal(err)
		}
		auditRun, err := recorder.StartRun(ctx, auditdomain.AIRun{OperationID: fmt.Sprintf("hap55-guard-%d", jobID), JobID: jobID, BotID: bot, ConversationID: conversations[origin]})
		if err != nil {
			t.Fatal(err)
		}
		arrived, release := make(chan struct{}), make(chan struct{})
		gate := &auditStartGate{AIRecorder: recorder, arrived: arrived, release: release}
		call, cancel := context.WithTimeout(audit.WithRun(ctx, gate, auditRun.ID), 5*time.Second)
		result := make(chan error, 1)
		reset(nil)
		go func() {
			result <- reply.ExecuteReply(call, intentdomain.Task{JobID: jobID, BotID: bot, Kind: "group", TargetID: groupA, ConversationID: conversations[origin], Message: intentdomain.Message{MessageID: messageID, SenderID: 10101, Text: "我叫苍紫"}}, nil)
		}()
		select {
		case <-arrived:
		case <-call.Done():
			cancel()
			t.Fatal("media audit start barrier was not reached")
		}
		var clearErr error
		if clear {
			_, clearErr = pool.Exec(ctx, `DELETE FROM messages WHERE id=$1`, sourceID)
		}
		close(release)
		err = <-result
		cancel()
		if clearErr != nil {
			t.Fatal(clearErr)
		}
		requests := readWire()
		if clear {
			if !errors.Is(err, contracts.ErrReadChanged) || len(requests) != 0 {
				t.Fatalf("audit-wait source cleanup err=%v requests=%d", err, len(requests))
			}
		} else if err != nil || len(requests) != 1 || !strings.Contains(requests[0], texts["legacy_shared"]) {
			t.Fatalf("authorized original scope err=%v requests=%d", err, len(requests))
		}
		t.Logf("audit start barrier source_cleared=%t actual_model_requests=%d", clear, len(requests))
	}
	if err := pool.QueryRow(ctx, `INSERT INTO messages(conversation_id,bot_id,direction,sender_id,text_content) VALUES($1,$2,'incoming',10101,'synthetic source restored') RETURNING id`, conversations[origin], bot).Scan(&sourceID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE memories SET source_message_id=$1 WHERE memory_id=$2`, sourceID, legacyID); err != nil {
		t.Fatal(err)
	}
	localID := ids["local"]
	ids["local"] = legacyID
	invoked.Store(0)
	afterTool = func(call context.Context) error {
		_, err := pool.Exec(call, `DELETE FROM messages WHERE id=$1`, sourceID)
		return err
	}
	reset(toolResponse)
	if err := run(origin, 10101, "请调用工具查询"); !errors.Is(err, contracts.ErrReadChanged) || invoked.Load() != 1 || len(readWire()) != 1 {
		t.Fatalf("cleaned source in tool context: err=%v tool_reads=%d requests=%d", err, invoked.Load(), len(readWire()))
	}
	afterTool = nil
	ids["local"] = localID
	var sourceCleared bool
	var visibility string
	if err := pool.QueryRow(ctx, `SELECT source_message_id IS NULL, visibility FROM memories WHERE memory_id=$1::uuid`, legacyID).Scan(&sourceCleared, &visibility); err != nil || !sourceCleared || visibility != string(domain.VisibilityBotShared) {
		t.Fatalf("cleaned source did not preserve legacy row: source_cleared=%v visibility=%q err=%v", sourceCleared, visibility, err)
	}
	assertLegacyReads(false)
	reset(nil)
	if err := run(origin, 10101, "我叫苍紫"); err != nil {
		t.Fatal(err)
	}
	if requests := readWire(); len(requests) != 1 || strings.Contains(requests[0], texts["legacy_shared"]) {
		t.Fatalf("cleaned legacy source reached fresh generation, requests=%d", len(requests))
	}
	t.Log("cleaned original source: foreign_key_null=true bot_shared_retained=true recall=0 detail=0 generation=0 retained_tool_second_request=0")
	reset(nil)
	if _, err := service.Supersede(ctx, domain.SupersedeInput{NewMemoryID: ids["correction"], OldMemoryID: ids["opted"], Actor: "fixture-admin", IdempotencyKey: "gold-correction"}); err != nil {
		t.Fatal(err)
	}
	if err := run(origin, 10101, "我叫霁蓝或湖青"); err != nil {
		t.Fatal(err)
	}
	if wire := readWire(); len(wire) != 1 || strings.Contains(wire[0], texts["opted"]) || !strings.Contains(wire[0], texts["correction"]) {
		t.Fatal("replacement not reflected in generation")
	}
	if err := service.Delete(ctx, ids["correction"]); err != nil {
		t.Fatal(err)
	}
	reset(nil)
	if err := run(origin, 10101, "我叫湖青"); err != nil {
		t.Fatal(err)
	}
	if wire := readWire(); len(wire) != 1 || strings.Contains(wire[0], texts["correction"]) {
		t.Fatal("deleted memory leaked on retry")
	}
}

type auditStartGate struct {
	audit.AIRecorder
	arrived chan struct{}
	release chan struct{}
}

func (g *auditStartGate) RecordEvent(ctx context.Context, event auditdomain.AIEvent) (auditdomain.AIEvent, error) {
	if event.Stage == "generation.media_request" {
		close(g.arrived)
		select {
		case <-g.release:
		case <-ctx.Done():
			return auditdomain.AIEvent{}, ctx.Err()
		}
	}
	return g.AIRecorder.RecordEvent(ctx, event)
}
