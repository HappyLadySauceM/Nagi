package intent

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/HappyLadySauceM/Nagi/cmd/app/svc"
	intentapp "github.com/HappyLadySauceM/Nagi/internal/intent/application"
	"github.com/HappyLadySauceM/Nagi/internal/intent/domain"
	intentinfra "github.com/HappyLadySauceM/Nagi/internal/intent/infrastructure"
	"github.com/HappyLadySauceM/Nagi/pkg/db/dbtest"
)

func promptHTTPFixture(t *testing.T) (*http.ServeMux, *intentinfra.PromptsRepo, string) {
	t.Helper()
	pool := dbtest.Open(t)
	repo, err := intentinfra.NewPromptsRepo(pool)
	if err != nil {
		t.Fatalf("new prompt repo: %v", err)
	}
	promptID := domain.BuiltinPromptID + ".http.rollback." + time.Now().Format("150405.000000000")
	content := domain.BuiltinPrompt(domain.PromptProviderJev)
	v1, err := repo.CreateDraft(context.Background(), promptID, "http fixture", domain.PromptProviderJev, content, "v1", domain.PromptSourceLocal, "")
	if err != nil {
		t.Fatalf("create v1: %v", err)
	}
	if err := repo.Publish(context.Background(), v1.ID); err != nil {
		t.Fatalf("publish v1: %v", err)
	}
	mux := http.NewServeMux()
	NewHandler(&svc.ServiceContext{Prompts: intentapp.NewPromptService(repo, slog.Default())}).Register(mux)
	return mux, repo, promptID
}

func rollbackRequest(mux *http.ServeMux, promptID string, version int) *httptest.ResponseRecorder {
	body, _ := json.Marshal(map[string]any{"version": version})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/v1/intent/prompts/%s/rollback", promptID), bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	mux.ServeHTTP(rec, req)
	return rec
}

func TestPromptRollbackHTTPRejectsDraftAndKeepsActive(t *testing.T) {
	mux, repo, promptID := promptHTTPFixture(t)
	content := domain.BuiltinPrompt(domain.PromptProviderJev)
	v2, err := repo.CreateDraft(context.Background(), promptID, "http fixture", domain.PromptProviderJev, content, "v2", domain.PromptSourceLocal, "")
	if err != nil {
		t.Fatalf("create v2: %v", err)
	}
	rec := rollbackRequest(mux, promptID, v2.Version)
	if rec.Code != http.StatusConflict {
		t.Fatalf("draft rollback status = %d, want 409: %s", rec.Code, rec.Body.String())
	}
	active, found, err := repo.Active(context.Background(), promptID, domain.PromptProviderJev)
	if err != nil || !found || active.Version != 1 {
		t.Fatalf("active after rejected rollback = %+v found=%v err=%v", active, found, err)
	}
}

func TestPromptRollbackHTTPAcceptsArchivedPublishedVersion(t *testing.T) {
	mux, repo, promptID := promptHTTPFixture(t)
	content := domain.BuiltinPrompt(domain.PromptProviderJev)
	v2, err := repo.CreateDraft(context.Background(), promptID, "http fixture", domain.PromptProviderJev, content, "v2", domain.PromptSourceLocal, "")
	if err != nil {
		t.Fatalf("create v2: %v", err)
	}
	if err := repo.Publish(context.Background(), v2.ID); err != nil {
		t.Fatalf("publish v2: %v", err)
	}
	rec := rollbackRequest(mux, promptID, 1)
	if rec.Code != http.StatusOK {
		t.Fatalf("archived rollback status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	active, found, err := repo.Active(context.Background(), promptID, domain.PromptProviderJev)
	if err != nil || !found || active.Version != 1 {
		t.Fatalf("active after rollback = %+v found=%v err=%v", active, found, err)
	}
}

func TestPromptRollbackHTTPRejectsCrossPromptVersion(t *testing.T) {
	mux, repo, promptID := promptHTTPFixture(t)
	content := domain.BuiltinPrompt(domain.PromptProviderJev)
	otherID := domain.BuiltinPromptID + ".http.other." + time.Now().Format("150405.000000000")
	otherV1, err := repo.CreateDraft(context.Background(), otherID, "other", domain.PromptProviderJev, content, "v1", domain.PromptSourceLocal, "")
	if err != nil {
		t.Fatalf("create other v1: %v", err)
	}
	if err := repo.Publish(context.Background(), otherV1.ID); err != nil {
		t.Fatalf("publish other v1: %v", err)
	}
	otherV2, err := repo.CreateDraft(context.Background(), otherID, "other", domain.PromptProviderJev, content, "v2", domain.PromptSourceLocal, "")
	if err != nil {
		t.Fatalf("create other v2: %v", err)
	}
	if err := repo.Publish(context.Background(), otherV2.ID); err != nil {
		t.Fatalf("publish other v2: %v", err)
	}
	rec := rollbackRequest(mux, promptID, otherV2.Version)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("cross-prompt rollback status = %d, want 404: %s", rec.Code, rec.Body.String())
	}
}

type fakePromptRemote struct {
	configured bool
	remote     domain.RemotePrompt
}

func (f *fakePromptRemote) Configured() bool { return f.configured }
func (f *fakePromptRemote) Fetch(context.Context, string, int, string) (domain.RemotePrompt, error) {
	return f.remote, nil
}

func promptMuxWithRemote(t *testing.T, remote *fakePromptRemote) (*http.ServeMux, *intentinfra.PromptsRepo, string) {
	t.Helper()
	pool := dbtest.Open(t)
	repo, err := intentinfra.NewPromptsRepo(pool)
	if err != nil {
		t.Fatalf("new prompt repo: %v", err)
	}
	promptID := domain.BuiltinPromptID + ".http.sync." + time.Now().Format("150405.000000000")
	mux := http.NewServeMux()
	NewHandler(&svc.ServiceContext{Prompts: intentapp.NewPromptService(repo, slog.Default(), remote)}).Register(mux)
	return mux, repo, promptID
}

func TestPromptSyncStatusHTTPWithoutConfiguration(t *testing.T) {
	mux, _, promptID := promptMuxWithRemote(t, &fakePromptRemote{configured: false})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/intent/prompts/sync-status?prompt_id="+promptID+"&provider=jev", nil)
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	var envelope struct {
		Data struct {
			Configured bool
			Status     string
			ReasonCode string
			Active     struct {
				Source string
			}
		}
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if envelope.Data.Configured || envelope.Data.Status != domain.PromptSyncStatusNotConfigured || envelope.Data.Active.Source != domain.PromptSourceBuiltin {
		t.Fatalf("data = %+v", envelope.Data)
	}
}

func TestPromptSyncHTTPCreatesLocalCandidate(t *testing.T) {
	content := domain.PromptContentJSON(domain.BuiltinPrompt(domain.PromptProviderJev))
	remote := &fakePromptRemote{configured: true, remote: domain.RemotePrompt{
		Name: domain.BuiltinPromptID + ".http.sync", Version: 4, Type: "text", Prompt: content,
	}}
	mux, _, promptID := promptMuxWithRemote(t, remote)
	remote.remote.Name = promptID
	body, _ := json.Marshal(map[string]any{"Provider": "jev", "Version": 4})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/intent/prompts/"+promptID+"/sync", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	var envelope struct {
		Data struct {
			Status    string
			Candidate struct {
				Status    string
				Source    string
				SourceRef string
			}
		}
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if envelope.Data.Status != "candidate_created" || envelope.Data.Candidate.Status != domain.PromptStatusDraft || envelope.Data.Candidate.Source != domain.PromptSourceLangfuse {
		t.Fatalf("data = %+v", envelope.Data)
	}

	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/intent/prompts/sync-status?prompt_id="+promptID+"&provider=jev", nil))
	var status struct {
		Data struct {
			Status     string
			Candidates []struct{ ID int64 }
		}
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &status); err != nil {
		t.Fatalf("decode status: %v", err)
	}
	if status.Data.Status != domain.PromptSyncStatusOK || len(status.Data.Candidates) != 1 {
		t.Fatalf("status = %+v", status.Data)
	}
}

func TestPromptSyncHTTPNotConfiguredIsConflict(t *testing.T) {
	mux, _, promptID := promptMuxWithRemote(t, &fakePromptRemote{configured: false})
	body, _ := json.Marshal(map[string]any{"Provider": "jev", "Version": 1})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/intent/prompts/"+promptID+"/sync", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	var envelope struct {
		Data struct{ ReasonCode string }
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if envelope.Data.ReasonCode != domain.PromptSyncReasonNotConfigured {
		t.Fatalf("reason = %q", envelope.Data.ReasonCode)
	}
}
