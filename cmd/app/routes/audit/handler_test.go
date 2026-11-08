package audit

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/HappyLadySauceM/Nagi/cmd/app/svc"
	auditcontracts "github.com/HappyLadySauceM/Nagi/internal/audit/contracts"
	auditdomain "github.com/HappyLadySauceM/Nagi/internal/audit/domain"
	"github.com/google/uuid"
)

type fakeReader struct {
	runs     []auditdomain.AIRun
	run      auditdomain.AIRun
	artifact auditdomain.AIArtifact
	err      error
	query    auditdomain.AIRunQuery
}

type fakeSigner struct{ url string }

func (f fakeSigner) Presign(context.Context, auditdomain.AIArtifact) (string, error) {
	return f.url, nil
}

func (f *fakeReader) ListRuns(_ context.Context, query auditdomain.AIRunQuery) ([]auditdomain.AIRun, error) {
	f.query = query
	return f.runs, f.err
}

func (f *fakeReader) GetRun(_ context.Context, id string) (auditdomain.AIRun, error) {
	if f.err != nil {
		return auditdomain.AIRun{}, f.err
	}
	if f.run.ID != id {
		return auditdomain.AIRun{}, auditdomain.ErrNotFound
	}
	return f.run, nil
}

func (f *fakeReader) GetArtifact(_ context.Context, id string) (auditdomain.AIArtifact, error) {
	if f.err != nil {
		return auditdomain.AIArtifact{}, f.err
	}
	if f.artifact.ID != id {
		return auditdomain.AIArtifact{}, auditdomain.ErrNotFound
	}
	return f.artifact, nil
}

var _ auditcontracts.AIReader = (*fakeReader)(nil)

func newServer(reader auditcontracts.AIReader) *http.ServeMux {
	mux := http.NewServeMux()
	NewHandler(&svc.ServiceContext{AIAudit: reader}).Register(mux)
	return mux
}

func TestListRunsPassesFilters(t *testing.T) {
	conversationID := uuid.NewString()
	reader := &fakeReader{runs: []auditdomain.AIRun{{ID: uuid.NewString(), ConversationID: conversationID}}}
	rec := httptest.NewRecorder()
	newServer(reader).ServeHTTP(rec, httptest.NewRequest(http.MethodGet,
		"/api/v1/audit/runs?bot_id=12&conversation_id="+conversationID+"&job_id=4&limit=5", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	if reader.query.BotID != 12 || reader.query.ConversationID != conversationID || reader.query.JobID != 4 || reader.query.Limit != 5 {
		t.Fatalf("query = %+v", reader.query)
	}
	var envelope struct {
		Data struct {
			Items []auditdomain.AIRun `json:"items"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	if len(envelope.Data.Items) != 1 {
		t.Fatalf("items = %+v", envelope.Data.Items)
	}
}

func TestListRunsRejectsMalformedFilters(t *testing.T) {
	for _, target := range []string{
		"/api/v1/audit/runs?bot_id=1&conversation_id=bad",
		"/api/v1/audit/runs?bot_id=1&job_id=0",
		"/api/v1/audit/runs?bot_id=1&limit=101",
	} {
		rec := httptest.NewRecorder()
		newServer(&fakeReader{}).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, target, nil))
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("%s: status = %d", target, rec.Code)
		}
	}
}

func TestGetRunAndArtifactReturnNotFound(t *testing.T) {
	reader := &fakeReader{err: auditdomain.ErrNotFound}
	mux := newServer(reader)
	for _, target := range []string{"/api/v1/audit/runs/" + uuid.NewString(), "/api/v1/audit/artifacts/" + uuid.NewString()} {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, target, nil))
		if rec.Code != http.StatusNotFound {
			t.Fatalf("%s: status = %d: %s", target, rec.Code, rec.Body.String())
		}
	}
}

func TestGetRunSurfacesStorageFailure(t *testing.T) {
	reader := &fakeReader{err: errors.New("database unavailable")}
	rec := httptest.NewRecorder()
	newServer(reader).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/audit/runs/"+uuid.NewString(), nil))
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
}

func TestGetArtifactIncludesFreshReplayURL(t *testing.T) {
	artifact := auditdomain.AIArtifact{ID: uuid.NewString(), Status: "uploaded", Kind: "audio.outbound.synthesized"}
	mux := http.NewServeMux()
	NewHandler(&svc.ServiceContext{
		AIAudit:      &fakeReader{artifact: artifact},
		MediaArchive: fakeSigner{url: "https://media.example/nagi/audio.mp3?temporary=1"},
	}).Register(mux)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/audit/artifacts/"+artifact.ID, nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	var envelope struct {
		Data struct {
			ID  string `json:"ID"`
			URL string `json:"url"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	if envelope.Data.ID != artifact.ID {
		t.Fatalf("id = %q", envelope.Data.ID)
	}
	if envelope.Data.URL != "https://media.example/nagi/audio.mp3?temporary=1" {
		t.Fatalf("url = %q", envelope.Data.URL)
	}
}
