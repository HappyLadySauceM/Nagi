// Tests for local prompt sync candidate handling.
// 本地 Prompt 同步候选处理测试。
package application

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/HappyLadySauceM/Nagi/internal/intent/domain"
)

type promptSyncRepo struct {
	mu      sync.Mutex
	records []domain.PromptRecord
	nextID  int64
}

func (f *promptSyncRepo) Active(_ context.Context, promptID, provider string) (domain.PromptRecord, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, rec := range f.records {
		if rec.PromptID == promptID && rec.Provider == provider && rec.Status == domain.PromptStatusPublished {
			return rec, true, nil
		}
	}
	return domain.PromptRecord{}, false, nil
}

func (f *promptSyncRepo) List(context.Context) ([]domain.PromptRecord, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]domain.PromptRecord(nil), f.records...), nil
}

func (f *promptSyncRepo) Versions(_ context.Context, promptID string) ([]domain.PromptRecord, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]domain.PromptRecord, 0)
	for _, rec := range f.records {
		if rec.PromptID == promptID {
			out = append(out, rec)
		}
	}
	return out, nil
}

func (f *promptSyncRepo) Get(_ context.Context, id int64) (domain.PromptRecord, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, rec := range f.records {
		if rec.ID == id {
			return rec, nil
		}
	}
	return domain.PromptRecord{}, errors.New("not found")
}

func (f *promptSyncRepo) CreateDraft(_ context.Context, promptID, name, provider string, content domain.Prompt, note, source, sourceRef string) (domain.PromptRecord, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	version := 1
	for _, rec := range f.records {
		if rec.PromptID == promptID && rec.Version >= version {
			version = rec.Version + 1
		}
	}
	f.nextID++
	raw, _ := json.Marshal(map[string]any{"Provider": content.Provider, "SystemOne": content.SystemOne, "OpenAI": content.OpenAI})
	rec := domain.PromptRecord{
		ID: f.nextID, PromptID: promptID, Name: name, Provider: provider, Version: version,
		Status: domain.PromptStatusDraft, Source: source, SourceRef: sourceRef,
		Hash: domain.PromptHash(content), Note: note, Content: raw, CreatedAt: time.Now().Format(time.RFC3339),
	}
	f.records = append(f.records, rec)
	return rec, nil
}

func (f *promptSyncRepo) UpdateDraft(_ context.Context, id int64, note string, content domain.Prompt) (domain.PromptRecord, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i := range f.records {
		if f.records[i].ID == id && f.records[i].Status == domain.PromptStatusDraft {
			f.records[i].Note = note
			f.records[i].Hash = domain.PromptHash(content)
			return f.records[i], nil
		}
	}
	return domain.PromptRecord{}, errors.New("immutable")
}

func (f *promptSyncRepo) Publish(_ context.Context, id int64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i := range f.records {
		if f.records[i].Status == domain.PromptStatusPublished {
			f.records[i].Status = domain.PromptStatusArchived
		}
	}
	for i := range f.records {
		if f.records[i].ID == id {
			f.records[i].Status = domain.PromptStatusPublished
		}
	}
	return nil
}

func (f *promptSyncRepo) Rollback(_ context.Context, promptID string, version int) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i := range f.records {
		if f.records[i].PromptID == promptID && f.records[i].Status == domain.PromptStatusPublished {
			f.records[i].Status = domain.PromptStatusArchived
		}
	}
	for i := range f.records {
		if f.records[i].PromptID == promptID && f.records[i].Version == version {
			f.records[i].Status = domain.PromptStatusPublished
		}
	}
	return nil
}

type fakeRemotePrompts struct {
	configured bool
	remote     domain.RemotePrompt
	err        error
	fetches    int
}

func (f *fakeRemotePrompts) Configured() bool { return f.configured }

func (f *fakeRemotePrompts) Fetch(context.Context, string, int, string) (domain.RemotePrompt, error) {
	f.fetches++
	return f.remote, f.err
}

func remotePromptForTest(t *testing.T, version int) domain.RemotePrompt {
	t.Helper()
	content := domain.PromptContentJSON(domain.BuiltinPrompt(domain.PromptProviderJev))
	return domain.RemotePrompt{
		Name: domain.BuiltinPromptID, Version: version, Type: "text", Prompt: content,
		UpdatedAt: "2026-11-12T00:00:00Z",
	}
}

func TestPromptSyncStatusWithoutRemoteUsesBuiltin(t *testing.T) {
	svc := NewPromptService(&promptSyncRepo{}, slog.Default())
	status, err := svc.SyncStatus(context.Background(), "", "")
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if status.Configured || status.Status != domain.PromptSyncStatusNotConfigured || status.ReasonCode != domain.PromptSyncReasonNotConfigured {
		t.Fatalf("status = %+v", status)
	}
	if status.Active.Source != domain.PromptSourceBuiltin || status.Active.Version != 1 {
		t.Fatalf("active = %+v", status.Active)
	}
}

func TestPromptSyncRemoteCreatesCandidateWithoutActivating(t *testing.T) {
	repo := &promptSyncRepo{}
	remote := &fakeRemotePrompts{configured: true, remote: remotePromptForTest(t, 3)}
	svc := NewPromptService(repo, slog.Default(), remote)
	result, err := svc.SyncRemote(context.Background(), domain.BuiltinPromptID, domain.PromptProviderJev, 3, "")
	if err != nil {
		t.Fatalf("sync: %v", err)
	}
	if result.Status != "candidate_created" || result.Candidate.Status != domain.PromptStatusDraft || result.Candidate.Source != domain.PromptSourceLangfuse {
		t.Fatalf("result = %+v", result)
	}
	if result.Candidate.SourceRef != "langfuse:intent.participation@3" {
		t.Fatalf("source ref = %q", result.Candidate.SourceRef)
	}
	if active, found, err := repo.Active(context.Background(), domain.BuiltinPromptID, domain.PromptProviderJev); err != nil || found {
		t.Fatalf("candidate must not activate: active=%+v found=%v err=%v", active, found, err)
	}
	status, err := svc.SyncStatus(context.Background(), domain.BuiltinPromptID, domain.PromptProviderJev)
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if !status.Configured || status.Status != domain.PromptSyncStatusOK || len(status.Candidates) != 1 || status.Active.Source != domain.PromptSourceBuiltin {
		t.Fatalf("status = %+v", status)
	}
}

func TestPromptSyncRemoteRejectsInvalidContent(t *testing.T) {
	repo := &promptSyncRepo{}
	remote := &fakeRemotePrompts{configured: true, remote: domain.RemotePrompt{
		Name: domain.BuiltinPromptID, Version: 2, Prompt: json.RawMessage(`"plain text"`),
	}}
	svc := NewPromptService(repo, slog.Default(), remote)
	if _, err := svc.SyncRemote(context.Background(), domain.BuiltinPromptID, domain.PromptProviderJev, 2, ""); !errors.Is(err, domain.ErrPromptRemoteInvalid) {
		t.Fatalf("err = %v, want invalid", err)
	}
	if records, _ := repo.Versions(context.Background(), domain.BuiltinPromptID); len(records) != 0 {
		t.Fatalf("invalid content must not create candidate: %+v", records)
	}
}

func TestPromptSyncRemoteSameVersionIsIdempotent(t *testing.T) {
	repo := &promptSyncRepo{}
	remote := &fakeRemotePrompts{configured: true, remote: remotePromptForTest(t, 3)}
	svc := NewPromptService(repo, slog.Default(), remote)
	if _, err := svc.SyncRemote(context.Background(), domain.BuiltinPromptID, domain.PromptProviderJev, 3, ""); err != nil {
		t.Fatalf("first sync: %v", err)
	}
	result, err := svc.SyncRemote(context.Background(), domain.BuiltinPromptID, domain.PromptProviderJev, 3, "")
	if err != nil {
		t.Fatalf("second sync: %v", err)
	}
	if result.Status != "already_synced" {
		t.Fatalf("status = %q", result.Status)
	}
	if records, _ := repo.Versions(context.Background(), domain.BuiltinPromptID); len(records) != 1 {
		t.Fatalf("duplicate candidates: %+v", records)
	}
}

func TestPromptSyncRemoteVersionConflict(t *testing.T) {
	repo := &promptSyncRepo{}
	remote := &fakeRemotePrompts{configured: true, remote: remotePromptForTest(t, 3)}
	svc := NewPromptService(repo, slog.Default(), remote)
	if _, err := svc.SyncRemote(context.Background(), domain.BuiltinPromptID, domain.PromptProviderJev, 3, ""); err != nil {
		t.Fatalf("first sync: %v", err)
	}
	changed := domain.PromptContentJSON(domain.BuiltinPrompt(domain.PromptProviderJev))
	var parsed map[string]any
	_ = json.Unmarshal(changed, &parsed)
	parsed["Provider"] = "jev"
	parsed["SystemOne"].(map[string]any)["Questions"].(map[string]any)["directed_to_bot"].(map[string]any)["Instructions"] = "changed"
	raw, _ := json.Marshal(parsed)
	remote.remote.Prompt = raw
	if _, err := svc.SyncRemote(context.Background(), domain.BuiltinPromptID, domain.PromptProviderJev, 3, ""); !errors.Is(err, domain.ErrPromptRemoteVersionConflict) {
		t.Fatalf("err = %v, want version conflict", err)
	}
}

func TestPromptSyncRemoteRejectsNameMismatch(t *testing.T) {
	repo := &promptSyncRepo{}
	remote := &fakeRemotePrompts{configured: true, remote: domain.RemotePrompt{
		Name: "other.prompt", Version: 2, Prompt: domain.PromptContentJSON(domain.BuiltinPrompt(domain.PromptProviderJev)),
	}}
	svc := NewPromptService(repo, slog.Default(), remote)
	if _, err := svc.SyncRemote(context.Background(), domain.BuiltinPromptID, domain.PromptProviderJev, 2, ""); !errors.Is(err, domain.ErrPromptRemoteInvalid) {
		t.Fatalf("err = %v, want invalid", err)
	}
}
