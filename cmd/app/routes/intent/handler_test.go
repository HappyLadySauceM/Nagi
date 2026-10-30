// Tests for the decision log endpoints.
// 决策记录接口的测试。
package intent

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/HappyLadySauceM/Nagi/cmd/app/svc"
	intentcontracts "github.com/HappyLadySauceM/Nagi/internal/intent/contracts"
	intentdomain "github.com/HappyLadySauceM/Nagi/internal/intent/domain"
	intentinfra "github.com/HappyLadySauceM/Nagi/internal/intent/infrastructure"
	"github.com/HappyLadySauceM/Nagi/pkg/db/dbtest"
)

// fakeReader serves canned decisions and records the query it received.
// fakeReader 返回预设决策并记录收到的查询。
type fakeReader struct {
	rows   []intentcontracts.DecisionRecord
	detail intentcontracts.DecisionRecord
	err    error
	query  intentcontracts.DecisionQuery
}

func (f *fakeReader) List(_ context.Context, q intentcontracts.DecisionQuery) ([]intentcontracts.DecisionRecord, error) {
	f.query = q
	return f.rows, f.err
}

func (f *fakeReader) Get(_ context.Context, id int64) (intentcontracts.DecisionRecord, error) {
	if f.err != nil {
		return intentcontracts.DecisionRecord{}, f.err
	}
	if id != f.detail.ID {
		return intentcontracts.DecisionRecord{}, intentinfra.ErrDecisionNotFound
	}
	return f.detail, nil
}

func newServer(reader intentcontracts.DecisionReader) *http.ServeMux {
	mux := http.NewServeMux()
	NewHandler(&svc.ServiceContext{Decisions: reader}).Register(mux)
	return mux
}

// envelope decodes the shared response envelope.
// envelope 解码统一响应信封。
type envelope struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data"`
}

func TestListRequiresBotID(t *testing.T) {
	mux := newServer(&fakeReader{})
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/intent/decisions", nil))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

func TestListRejectsInvalidLimit(t *testing.T) {
	mux := newServer(&fakeReader{})
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/intent/decisions?bot_id=1&limit=500", nil))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

func TestListPassesFiltersAndReturnsItems(t *testing.T) {
	reader := &fakeReader{rows: []intentcontracts.DecisionRecord{{
		ID: 7, JobID: 9, BotID: 42, ConversationID: "conv-1",
		Decision: "reply", ReasonCode: "always_reply", Provider: "jev", Model: "jev-latest",
		LatencyMS: 12, CreatedAt: time.Unix(0, 0).UTC(),
	}}}
	mux := newServer(reader)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet,
		"/api/v1/intent/decisions?bot_id=42&conversation_id=conv-1&limit=5&before_id=99", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	if reader.query.BotID != 42 || reader.query.ConversationID != "conv-1" ||
		reader.query.Limit != 5 || reader.query.BeforeID != 99 {
		t.Fatalf("query = %+v", reader.query)
	}
	var env envelope
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode envelope: %v", err)
	}
	var data struct {
		Items []struct {
			ID         int64
			ReasonCode string
		}
	}
	if err := json.Unmarshal(env.Data, &data); err != nil {
		t.Fatalf("decode data: %v", err)
	}
	if len(data.Items) != 1 || data.Items[0].ID != 7 || data.Items[0].ReasonCode != "always_reply" {
		t.Fatalf("items = %+v", data.Items)
	}
}

func TestGetReturnsSteps(t *testing.T) {
	reader := &fakeReader{detail: intentcontracts.DecisionRecord{
		ID: 5, Decision: "reply", ReasonCode: "explicit_mention",
		Steps: []intentcontracts.DecisionStep{
			{Index: 0, Phase: "rules", ReasonCode: "explicit_mention", Detail: map[string]any{"explicit": true}},
			{Index: 1, Phase: "final", ReasonCode: "explicit_mention"},
		},
	}}
	mux := newServer(reader)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/intent/decisions/5", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	var env envelope
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode envelope: %v", err)
	}
	var data struct {
		Steps []struct {
			Phase  string
			Detail map[string]any
		}
	}
	if err := json.Unmarshal(env.Data, &data); err != nil {
		t.Fatalf("decode data: %v", err)
	}
	if len(data.Steps) != 2 || data.Steps[0].Detail["explicit"] != true {
		t.Fatalf("steps = %+v", data.Steps)
	}
}

func TestGetMissingDecisionReturns404(t *testing.T) {
	mux := newServer(&fakeReader{detail: intentcontracts.DecisionRecord{ID: 1}})
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/intent/decisions/999", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}

func TestGlobalPreviewPreservesActiveSpecificRules(t *testing.T) {
	pool := dbtest.Open(t)
	repo, err := intentinfra.NewRulesetsRepo(pool)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	const botID = 995051
	defer func() {
		pool.Exec(ctx, "DELETE FROM intent_rule_activations WHERE bot_id=$1", botID)
		pool.Exec(ctx, "DELETE FROM intent_rulesets WHERE bot_id=$1", botID)
	}()
	specific, err := repo.CreateDraft(ctx, botID, "private", 99, "specific", false)
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.ReplaceEntries(ctx, specific.ID, []intentdomain.RuleEntry{{Priority: 0, Action: intentdomain.RuleAction{Type: intentdomain.ActionSilent, ReasonCode: "specific_stop"}}}); err != nil {
		t.Fatal(err)
	}
	if err := repo.Publish(ctx, specific.ID); err != nil {
		t.Fatal(err)
	}
	global, err := repo.CreateDraft(ctx, botID, "group", 0, "global draft", false)
	if err != nil {
		t.Fatal(err)
	}
	entries := []intentdomain.RuleEntry{{Priority: 0, Action: intentdomain.RuleAction{Type: intentdomain.ActionReply, ReasonCode: "global_reply"}}}
	if err := repo.ReplaceEntries(ctx, global.ID, entries); err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	NewHandler(&svc.ServiceContext{Rulesets: repo}).RegisterRules(mux)
	for _, preview := range []map[string]any{{"entries": entries, "preview_global": true}, {"ruleset_id": global.ID}} {
		body := map[string]any{"bot_id": botID, "scope_type": "private", "scope_id": 99, "kind": "private", "reply_mode": "always_reply", "seconds_since_reply": 60}
		for key, value := range preview {
			body[key] = value
		}
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		request := httptest.NewRequest(http.MethodPost, "/api/v1/intent/simulate", bytes.NewReader(raw))
		response := httptest.NewRecorder()
		mux.ServeHTTP(response, request)
		var result struct {
			Data struct {
				Blocked    bool
				ReasonCode string
				ForceReply bool
			}
		}
		if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		if response.Code != 200 || !result.Data.Blocked || result.Data.ForceReply || result.Data.ReasonCode != "specific_stop" {
			t.Fatalf("global preview bypassed specific: %d %s", response.Code, response.Body.String())
		}
	}
}
