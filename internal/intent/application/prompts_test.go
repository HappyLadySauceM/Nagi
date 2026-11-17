// Tests for local prompt sync candidate handling.
// 本地 Prompt 同步候选处理测试。
package application

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
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

func (f *promptSyncRepo) GetIncludingDeleted(ctx context.Context, id int64) (domain.PromptRecord, error) {
	return f.Get(ctx, id)
}

func (f *promptSyncRepo) DeleteVersion(context.Context, int64, int64) error {
	return nil
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

// layeredPromptRepo serves account-layer records for ResolveFor tests and
// delegates the legacy repository methods to the embedded fake.
// layeredPromptRepo 为 ResolveFor 测试提供账号层记录，其余旧版仓储方法委托给内嵌假实现。
type layeredPromptRepo struct {
	*promptSyncRepo
	accounts []domain.PromptRecord
}

func (f *layeredPromptRepo) ActiveLayer(_ context.Context, key domain.PromptKey) (domain.PromptRecord, bool, error) {
	for _, rec := range f.accounts {
		if rec.PromptID == key.PromptID && rec.Kind == key.Kind && rec.Provider == key.Provider &&
			rec.BotID == key.BotID && rec.ScopeType == key.ScopeType {
			return rec, true, nil
		}
	}
	return domain.PromptRecord{}, false, nil
}

func (f *layeredPromptRepo) HasLayer(ctx context.Context, key domain.PromptKey) (bool, error) {
	_, found, err := f.ActiveLayer(ctx, key)
	return found, err
}

func (*layeredPromptRepo) ListAll(context.Context) ([]domain.PromptRecord, error) {
	return nil, nil
}

func (*layeredPromptRepo) VersionsLayer(context.Context, domain.PromptKey) ([]domain.PromptRecord, error) {
	return nil, nil
}

func (*layeredPromptRepo) CreateDraftVersion(context.Context, domain.PromptDraftInput) (domain.PromptRecord, error) {
	return domain.PromptRecord{}, nil
}

func (*layeredPromptRepo) PublishVersion(context.Context, int64) (domain.PromptRecord, int64, error) {
	return domain.PromptRecord{}, 0, nil
}

func (*layeredPromptRepo) RollbackVersion(context.Context, domain.PromptKey, int64) (domain.PromptRecord, int64, error) {
	return domain.PromptRecord{}, 0, nil
}

func (*layeredPromptRepo) RestoreBuiltin(context.Context, domain.PromptKey) (int64, error) {
	return 0, nil
}

func TestWithContentVariablesMergesContentRefs(t *testing.T) {
	// extra 与内容中出现的变量名取并集并去重；空内容原样返回 extra。
	content := json.RawMessage(`{"OpenAI":{"SystemPrompt":"hi {{foo}} {{bar}}"}}`)
	got := withContentVariables([]string{"foo", "baz"}, content)
	seen := make(map[string]bool, len(got))
	for _, name := range got {
		seen[name] = true
	}
	for _, want := range []string{"foo", "baz", "bar"} {
		if !seen[want] {
			t.Fatalf("missing %q in %v", want, got)
		}
	}
	if len(got) != 3 {
		t.Fatalf("merged = %v, want 3 unique names", got)
	}
	if pass := withContentVariables([]string{"x"}, nil); len(pass) != 1 || pass[0] != "x" {
		t.Fatalf("nil content = %v, want [x]", pass)
	}
}

func TestResolveForKeepsStoredPromptAfterCustomVariableRetired(t *testing.T) {
	// 已发布模板引用了当前已删除的自定义变量时，ResolveFor 仍应解析该模板而不是回退内置；
	// 未替换的占位符保持字面量，动态变量照常替换。
	published := domain.BuiltinPromptFor(domain.PromptKindChatReply, domain.PromptProviderOpenAI)
	published.OpenAI.SystemPrompt = "你好 {{user_name}}，我是 {{bot_name}}。"
	repo := &layeredPromptRepo{accounts: []domain.PromptRecord{{
		PromptID: domain.PromptIDChatReply, Kind: domain.PromptKindChatReply, Provider: domain.PromptProviderOpenAI,
		BotID: 1, ScopeType: domain.PromptScopeAccount, Version: 2, Status: domain.PromptStatusPublished,
		Source: domain.PromptSourceLocal, Hash: published.Hash, Content: domain.PromptContentJSON(published),
	}}}
	service := NewPromptService(repo, testLogger())
	// 当前没有自定义变量：user_name 视为已删除。
	service.SetCustomVariables(func() map[string]string { return nil })

	got := service.ResolveFor(context.Background(), domain.PromptIDChatReply, domain.PromptKindChatReply,
		domain.PromptProviderOpenAI, 1, domain.PromptScopeAccount, 0,
		map[string]string{"bot_name": "Nagi", "scope_type": "群聊"})
	if got.FallbackReason == "invalid_local_prompt" {
		t.Fatalf("stored prompt referencing retired variable fell back to builtin: %+v", got)
	}
	if got.Source != domain.PromptSourceLocal {
		t.Fatalf("source = %q, want local", got.Source)
	}
	if !strings.Contains(got.OpenAI.SystemPrompt, "{{user_name}}") {
		t.Fatalf("retired variable should stay literal, got %q", got.OpenAI.SystemPrompt)
	}
	if strings.Contains(got.OpenAI.SystemPrompt, "{{bot_name}}") || !strings.Contains(got.OpenAI.SystemPrompt, "Nagi") {
		t.Fatalf("bot_name should be rendered, got %q", got.OpenAI.SystemPrompt)
	}
}

func TestResolveForStillRejectsUnknownVariableOnSave(t *testing.T) {
	// 保存路径（CreateDraftFor）仍严格：未配置的变量必须被拒。
	repo := &layeredPromptRepo{}
	service := NewPromptService(repo, testLogger())
	service.SetCustomVariables(func() map[string]string { return nil })
	p := domain.BuiltinPromptFor(domain.PromptKindChatReply, domain.PromptProviderOpenAI)
	p.OpenAI.SystemPrompt = "你好 {{user_name}}"
	_, err := service.CreateDraftFor(context.Background(), domain.PromptIDChatReply, domain.PromptKindChatReply,
		domain.PromptProviderOpenAI, 1, domain.PromptScopeAccount, &p, "", 0, "", "", "", "")
	if err == nil {
		t.Fatal("unknown variable accepted on save")
	}
}
