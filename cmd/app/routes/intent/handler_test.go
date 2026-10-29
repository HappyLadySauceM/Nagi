// Tests for the decision log endpoints.
// 决策记录接口的测试。
package intent

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/HappyLadySauceM/Nagi/cmd/app/svc"
	intentcontracts "github.com/HappyLadySauceM/Nagi/internal/intent/contracts"
	intentinfra "github.com/HappyLadySauceM/Nagi/internal/intent/infrastructure"
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
