// HTTP tests for replay evaluation routes and mandatory confirmation.
// 回放评估路由与强制确认的 HTTP 测试。
package intent

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/HappyLadySauceM/Nagi/cmd/app/svc"
	intentapp "github.com/HappyLadySauceM/Nagi/internal/intent/application"
	intentinfra "github.com/HappyLadySauceM/Nagi/internal/intent/infrastructure"
	"github.com/HappyLadySauceM/Nagi/pkg/db/dbtest"
)

func TestReplayModelHTTPRejectsMissingConfirmation(t *testing.T) {
	mux := http.NewServeMux()
	NewHandler(&svc.ServiceContext{IntentReplayer: intentapp.NewReplayService(nil, nil, nil)}).Register(mux)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/intent/decisions/7/replay-model", bytes.NewReader([]byte(`{}`)))
	req.Header.Set("Content-Type", "application/json")
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusPreconditionRequired {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
}

func TestReplayEvaluationHTTPCreateRequiresMode(t *testing.T) {
	mux := http.NewServeMux()
	NewHandler(&svc.ServiceContext{IntentReplayer: intentapp.NewReplayService(nil, nil, nil)}).Register(mux)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/intent/decisions/7/replay-evaluations", bytes.NewReader([]byte(`{}`)))
	req.Header.Set("Content-Type", "application/json")
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
}

func TestReplayEvaluationHTTPListAndNotFound(t *testing.T) {
	pool := dbtest.Open(t)
	repo, err := intentinfra.NewReplayEvaluationsRepo(pool)
	if err != nil {
		t.Fatalf("new repo: %v", err)
	}
	mux := http.NewServeMux()
	NewHandler(&svc.ServiceContext{IntentReplayer: intentapp.NewReplayService(nil, nil, nil, repo)}).Register(mux)

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/intent/replay-evaluations?bot_id=1", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("list status = %d: %s", rec.Code, rec.Body.String())
	}
	var list struct {
		Data struct {
			Items []json.RawMessage
			Limit int
		}
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	if len(list.Data.Items) != 0 || list.Data.Limit != 20 {
		t.Fatalf("list = %+v", list.Data)
	}

	for _, path := range []string{
		"/api/v1/intent/replay-evaluations/999999",
		"/api/v1/intent/replay-evaluations/999999/comparison",
	} {
		rec = httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if rec.Code != http.StatusNotFound {
			t.Fatalf("%s status = %d: %s", path, rec.Code, rec.Body.String())
		}
	}
}
