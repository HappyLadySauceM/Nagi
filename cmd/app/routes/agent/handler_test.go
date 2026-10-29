// Tests for the outbound ledger endpoint.
// 投递台账接口的测试。
package agent

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/HappyLadySauceM/Nagi/cmd/app/svc"
	outbounddomain "github.com/HappyLadySauceM/Nagi/internal/outbound/domain"
)

type fakeReader struct{ rows []outbounddomain.Record }

func (f fakeReader) ListByJob(context.Context, int64) ([]outbounddomain.Record, error) {
	return f.rows, nil
}

func newServer(rows []outbounddomain.Record) *http.ServeMux {
	mux := http.NewServeMux()
	NewHandler(&svc.ServiceContext{Outbound: fakeReader{rows: rows}}).Register(mux)
	return mux
}

func TestListOutboundRequiresJobID(t *testing.T) {
	rec := httptest.NewRecorder()
	newServer(nil).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/agent/outbound", nil))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

func TestListOutboundReturnsRecords(t *testing.T) {
	mux := newServer([]outbounddomain.Record{
		{ID: "a", JobID: 7, Sequence: 1, Text: "第一条", Status: outbounddomain.StatusSent, PlatformMessageID: 11},
		{ID: "b", JobID: 7, Sequence: 2, Text: "第二条", Status: outbounddomain.StatusUnknown, ErrorCode: "timeout"},
	})
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/agent/outbound?job_id=7", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	var env struct {
		Code int
		Data struct {
			Items []struct {
				Sequence int
				Status   string
			}
		}
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(env.Data.Items) != 2 || env.Data.Items[0].Sequence != 1 || env.Data.Items[1].Status != "unknown" {
		t.Fatalf("items = %+v", env.Data.Items)
	}
}
