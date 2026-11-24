package infrastructure

import (
	"context"
	"errors"
	"log/slog"
	"testing"

	intentapp "github.com/HappyLadySauceM/Nagi/internal/intent/application"
	"github.com/HappyLadySauceM/Nagi/internal/intent/domain"
	"github.com/HappyLadySauceM/Nagi/pkg/db/dbtest"
)

func newPromptDeleteRepo(t *testing.T) *PromptsRepo {
	t.Helper()
	pool := dbtest.Open(t)
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM intent_prompt_versions WHERE bot_id IN (990201,990202,990203,990204)`)
	})
	repo, err := NewPromptsRepo(pool)
	if err != nil {
		t.Fatalf("new prompt repo: %v", err)
	}
	return repo
}

func createPromptDeleteDraft(t *testing.T, repo *PromptsRepo, promptID, kind, provider string, botID int64) domain.PromptRecord {
	t.Helper()
	content := domain.BuiltinPromptFor(kind, provider)
	rec, err := repo.CreateDraftVersion(context.Background(), domain.PromptDraftInput{
		PromptID: promptID, Kind: kind, Name: "delete fixture", Provider: provider,
		BotID: botID, ScopeType: domain.PromptScopeAccount, Content: content, Note: "draft",
		Source: domain.PromptSourceLocal,
	})
	if err != nil {
		t.Fatalf("create draft: %v", err)
	}
	return rec
}

func TestPromptVersionSoftDeleteDraftAndSequence(t *testing.T) {
	repo := newPromptDeleteRepo(t)
	ctx := context.Background()
	const botID = int64(990201)
	key := domain.PromptKey{PromptID: domain.PromptIDChatReply, Kind: domain.PromptKindChatReply, Provider: domain.PromptProviderOpenAI, BotID: botID, ScopeType: domain.PromptScopeAccount}

	v1 := createPromptDeleteDraft(t, repo, key.PromptID, key.Kind, key.Provider, botID)
	v2 := createPromptDeleteDraft(t, repo, key.PromptID, key.Kind, key.Provider, botID)
	if v1.Version != 1 || v2.Version != 2 {
		t.Fatalf("unexpected versions: v1=%d v2=%d", v1.Version, v2.Version)
	}
	if err := repo.DeleteVersion(ctx, v2.ID, botID); err != nil {
		t.Fatalf("delete draft: %v", err)
	}
	if _, err := repo.Get(ctx, v2.ID); !errors.Is(err, ErrPromptNotFound) {
		t.Fatalf("get deleted draft err = %v, want ErrPromptNotFound", err)
	}
	if rec, err := repo.GetIncludingDeleted(ctx, v2.ID); err != nil || rec.ID != v2.ID {
		t.Fatalf("get including deleted = %+v err=%v", rec, err)
	}
	versions, err := repo.VersionsLayer(ctx, key)
	if err != nil {
		t.Fatalf("versions layer: %v", err)
	}
	for _, rec := range versions {
		if rec.ID == v2.ID {
			t.Fatalf("deleted draft still listed: %+v", rec)
		}
	}
	all, err := repo.ListAll(ctx)
	if err != nil {
		t.Fatalf("list all: %v", err)
	}
	for _, rec := range all {
		if rec.ID == v2.ID {
			t.Fatalf("deleted draft still in ListAll: %+v", rec)
		}
	}
	if err := repo.DeleteVersion(ctx, v2.ID, botID); !errors.Is(err, ErrPromptNotFound) {
		t.Fatalf("repeat delete err = %v, want ErrPromptNotFound", err)
	}

	v3 := createPromptDeleteDraft(t, repo, key.PromptID, key.Kind, key.Provider, botID)
	if v3.Version != 3 {
		t.Fatalf("version after soft delete = %d, want 3 (historical max + 1)", v3.Version)
	}
	if err := repo.DeleteVersion(ctx, v3.ID, botID+1); !errors.Is(err, ErrPromptNotFound) {
		t.Fatalf("wrong bot delete err = %v, want ErrPromptNotFound", err)
	}
	if _, err := repo.Get(ctx, v3.ID); err != nil {
		t.Fatalf("wrong bot delete removed version: %v", err)
	}
}

func TestPromptVersionSoftDeleteArchivedKeepsHistorySnapshot(t *testing.T) {
	repo := newPromptDeleteRepo(t)
	ctx := context.Background()
	const botID = int64(990202)
	kind := domain.PromptKindMemoryExtraction
	provider := domain.PromptProviderOpenAI
	promptID := domain.PromptIDMemoryExtraction

	first := createPromptDeleteDraft(t, repo, promptID, kind, provider, botID)
	if _, _, err := repo.PublishVersion(ctx, first.ID); err != nil {
		t.Fatalf("publish first: %v", err)
	}
	second := createPromptDeleteDraft(t, repo, promptID, kind, provider, botID)
	if _, _, err := repo.PublishVersion(ctx, second.ID); err != nil {
		t.Fatalf("publish second: %v", err)
	}
	if err := repo.DeleteVersion(ctx, first.ID, botID); err != nil {
		t.Fatalf("delete archived: %v", err)
	}
	if _, err := repo.Get(ctx, first.ID); !errors.Is(err, ErrPromptNotFound) {
		t.Fatalf("get deleted archived err = %v, want ErrPromptNotFound", err)
	}

	svc := intentapp.NewPromptService(repo, slog.Default(), PromptCodec{})
	snapshot, err := svc.VersionPrompt(ctx, first.ID, kind, provider)
	if err != nil {
		t.Fatalf("rebuild historical prompt: %v", err)
	}
	if snapshot.Hash != first.Hash || snapshot.Version != first.Version || snapshot.OpenAI == nil {
		t.Fatalf("historical snapshot = %+v, want hash=%s version=%d", snapshot, first.Hash, first.Version)
	}
}

func TestPromptVersionSoftDeleteRejectsPublishedAndBuiltin(t *testing.T) {
	repo := newPromptDeleteRepo(t)
	ctx := context.Background()
	const botID = int64(990203)

	published := createPromptDeleteDraft(t, repo, domain.PromptIDChatReply, domain.PromptKindChatReply, domain.PromptProviderOpenAI, botID)
	if _, _, err := repo.PublishVersion(ctx, published.ID); err != nil {
		t.Fatalf("publish: %v", err)
	}
	if err := repo.DeleteVersion(ctx, published.ID, botID); !errors.Is(err, ErrPromptPublished) {
		t.Fatalf("delete published err = %v, want ErrPromptPublished", err)
	}
	if _, err := repo.Get(ctx, published.ID); err != nil {
		t.Fatalf("published version disappeared: %v", err)
	}

	const builtinBotID = int64(990204)
	content := domain.BuiltinPromptFor(domain.PromptKindMemoryExtraction, domain.PromptProviderOpenAI)
	builtin, err := repo.pool.Client.IntentPromptVersion.Create().
		SetPromptID(domain.PromptIDMemoryExtraction).SetPromptKind(domain.PromptKindMemoryExtraction).
		SetName("builtin fixture").SetProvider(domain.PromptProviderOpenAI).
		SetBotID(builtinBotID).SetScopeType(domain.PromptScopeAccount).SetScopeID(0).SetVersion(1).
		SetStatus(domain.PromptStatusArchived).SetSource(domain.PromptSourceBuiltin).
		SetSourceRef("builtin").SetHash(content.Hash).SetNote("builtin").SetContent(domain.PromptContentJSON(content)).
		Save(ctx)
	if err != nil {
		t.Fatalf("create builtin fixture: %v", err)
	}
	if err := repo.DeleteVersion(ctx, builtin.ID, builtinBotID); !errors.Is(err, ErrPromptBuiltin) {
		t.Fatalf("delete builtin err = %v, want ErrPromptBuiltin", err)
	}
}
