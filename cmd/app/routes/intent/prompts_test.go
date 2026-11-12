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
