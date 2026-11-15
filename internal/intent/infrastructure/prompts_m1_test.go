package infrastructure

import (
	"context"
	"log/slog"
	"strings"
	"testing"

	intentapp "github.com/HappyLadySauceM/Nagi/internal/intent/application"
	"github.com/HappyLadySauceM/Nagi/internal/intent/domain"
	"github.com/HappyLadySauceM/Nagi/pkg/db/dbtest"
)

func TestPromptLayersPublishRollbackRestore(t *testing.T) {
	pool := dbtest.Open(t)
	repo, err := NewPromptsRepo(pool)
	if err != nil {
		t.Fatalf("new prompt repo: %v", err)
	}
	ctx := context.Background()
	botID := int64(881001)
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM intent_prompt_versions WHERE bot_id=$1`, botID)
	})
	accountContent := domain.BuiltinPromptFor(domain.PromptKindChatReply, domain.PromptProviderOpenAI)
	account, err := repo.CreateDraftVersion(ctx, domain.PromptDraftInput{
		PromptID: domain.PromptIDChatReply, Kind: domain.PromptKindChatReply, Name: "chat account",
		Provider: domain.PromptProviderOpenAI, BotID: botID, ScopeType: domain.PromptScopeAccount,
		Content: accountContent, Note: "v1", Source: domain.PromptSourceLocal,
	})
	if err != nil {
		t.Fatalf("create account draft: %v", err)
	}
	if err := repo.Publish(ctx, account.ID); err != nil {
		t.Fatalf("publish account: %v", err)
	}
	active, found, err := repo.ActiveLayer(ctx, domain.PromptKey{PromptID: domain.PromptIDChatReply, Kind: domain.PromptKindChatReply, Provider: domain.PromptProviderOpenAI, BotID: botID, ScopeType: domain.PromptScopeAccount})
	if err != nil || !found || active.Version != 1 || active.ArchivedAt != "" {
		t.Fatalf("account active = %+v found=%v err=%v", active, found, err)
	}

	supplementContent := domain.EmptyPromptSupplement(domain.PromptKindChatReply, domain.PromptProviderOpenAI)
	supplementContent.OpenAI.SystemPrompt = "补充：优先给出可执行步骤。"
	supplement, err := repo.CreateDraftVersion(ctx, domain.PromptDraftInput{
		PromptID: domain.PromptIDChatReply, Kind: domain.PromptKindChatReply, Name: "chat group",
		Provider: domain.PromptProviderOpenAI, BotID: botID, ScopeType: domain.PromptScopeGroup,
		Content: supplementContent, Note: "group supplement", Source: domain.PromptSourceLocal,
	})
	if err != nil {
		t.Fatalf("create supplement: %v", err)
	}
	if err := repo.Publish(ctx, supplement.ID); err != nil {
		t.Fatalf("publish supplement: %v", err)
	}
	supplementActive, found, err := repo.ActiveLayer(ctx, domain.PromptKey{PromptID: domain.PromptIDChatReply, Kind: domain.PromptKindChatReply, Provider: domain.PromptProviderOpenAI, BotID: botID, ScopeType: domain.PromptScopeGroup})
	if err != nil || !found || supplementActive.Version != 1 {
		t.Fatalf("group active = %+v found=%v err=%v", supplementActive, found, err)
	}

	changed := accountContent
	changed.OpenAI = &domain.OpenAIPrompt{SystemPrompt: "账号新版本"}
	second, err := repo.CreateDraftVersion(ctx, domain.PromptDraftInput{
		PromptID: domain.PromptIDChatReply, Kind: domain.PromptKindChatReply, Name: "chat account v2",
		Provider: domain.PromptProviderOpenAI, BotID: botID, ScopeType: domain.PromptScopeAccount,
		Content: changed, Note: "v2", Source: domain.PromptSourceLocal,
	})
	if err != nil || second.Version != 2 {
		t.Fatalf("create second = %+v err=%v", second, err)
	}
	if err := repo.Publish(ctx, second.ID); err != nil {
		t.Fatalf("publish second: %v", err)
	}
	service := intentapp.NewPromptService(repo, slog.Default())
	layered := service.ResolveFor(ctx, domain.PromptIDChatReply, domain.PromptKindChatReply, domain.PromptProviderOpenAI, botID, domain.PromptScopeGroup, 700, map[string]string{"bot_name": "Nagi", "scope_type": "group"})
	if layered.OpenAI == nil || !strings.Contains(layered.OpenAI.SystemPrompt, "账号新版本") || !strings.Contains(layered.OpenAI.SystemPrompt, "补充：") {
		t.Fatalf("account+group composition = %+v", layered.OpenAI)
	}
	firstArchived, err := repo.Get(ctx, account.ID)
	if err != nil || firstArchived.Status != domain.PromptStatusArchived || firstArchived.ArchivedAt == "" {
		t.Fatalf("archived first = %+v err=%v", firstArchived, err)
	}
	if _, _, err := repo.RollbackVersion(ctx, domain.PromptKey{PromptID: domain.PromptIDChatReply, Kind: domain.PromptKindChatReply, Provider: domain.PromptProviderOpenAI, BotID: botID, ScopeType: domain.PromptScopeAccount}, account.ID); err != nil {
		t.Fatalf("rollback: %v", err)
	}
	rolledBack, err := repo.Get(ctx, account.ID)
	if err != nil || rolledBack.Status != domain.PromptStatusPublished || rolledBack.ArchivedAt != "" {
		t.Fatalf("rolled back = %+v err=%v", rolledBack, err)
	}

	archivedID, err := repo.RestoreBuiltin(ctx, domain.PromptKey{PromptID: domain.PromptIDChatReply, Kind: domain.PromptKindChatReply, Provider: domain.PromptProviderOpenAI, BotID: botID, ScopeType: domain.PromptScopeGroup})
	if err != nil || archivedID != supplement.ID {
		t.Fatalf("restore group = %d err=%v", archivedID, err)
	}
	if _, found, err := repo.ActiveLayer(ctx, domain.PromptKey{PromptID: domain.PromptIDChatReply, Kind: domain.PromptKindChatReply, Provider: domain.PromptProviderOpenAI, BotID: botID, ScopeType: domain.PromptScopeGroup}); err != nil || found {
		t.Fatalf("group still active after restore: found=%v err=%v", found, err)
	}

	// A supplement can still be created and appended after restore, but the
	// service must not duplicate the builtin full content into the supplement.
	resolved := service.ResolveFor(ctx, domain.PromptIDChatReply, domain.PromptKindChatReply, domain.PromptProviderOpenAI, botID, domain.PromptScopeGroup, 700, map[string]string{"bot_name": "Nagi", "scope_type": "group"})
	if resolved.OpenAI == nil || strings.Contains(resolved.OpenAI.SystemPrompt, "补充：") {
		t.Fatalf("restored group unexpectedly has supplement: %q", resolved.OpenAI)
	}
}

func TestPromptLayerPublishVersionAndRollbackGuards(t *testing.T) {
	pool := dbtest.Open(t)
	repo, err := NewPromptsRepo(pool)
	if err != nil {
		t.Fatalf("new prompt repo: %v", err)
	}
	ctx := context.Background()
	botID := int64(881002)
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM intent_prompt_versions WHERE bot_id=$1`, botID)
	})
	content := domain.BuiltinPromptFor(domain.PromptKindChatReply, domain.PromptProviderOpenAI)
	key := domain.PromptKey{PromptID: domain.PromptIDChatReply, Kind: domain.PromptKindChatReply, Provider: domain.PromptProviderOpenAI, BotID: botID, ScopeType: domain.PromptScopeAccount}
	first, err := repo.CreateDraftVersion(ctx, domain.PromptDraftInput{PromptID: key.PromptID, Kind: key.Kind, Name: "first", Provider: key.Provider, BotID: botID, ScopeType: domain.PromptScopeAccount, Content: content, Source: domain.PromptSourceLocal})
	if err != nil {
		t.Fatalf("create first: %v", err)
	}
	published, archivedID, err := repo.PublishVersion(ctx, first.ID)
	if err != nil || archivedID != 0 || published.Status != domain.PromptStatusPublished {
		t.Fatalf("publish first = %+v archived=%d err=%v", published, archivedID, err)
	}
	secondContent := content
	secondContent.OpenAI = &domain.OpenAIPrompt{SystemPrompt: "second"}
	second, err := repo.CreateDraftVersion(ctx, domain.PromptDraftInput{PromptID: key.PromptID, Kind: key.Kind, Name: "second", Provider: key.Provider, BotID: botID, ScopeType: domain.PromptScopeAccount, Content: secondContent, Source: domain.PromptSourceLocal})
	if err != nil {
		t.Fatalf("create second: %v", err)
	}
	published, archivedID, err = repo.PublishVersion(ctx, second.ID)
	if err != nil || archivedID != first.ID || published.Status != domain.PromptStatusPublished {
		t.Fatalf("publish second = %+v archived=%d err=%v", published, archivedID, err)
	}
	groupContent := domain.EmptyPromptSupplement(domain.PromptKindChatReply, domain.PromptProviderOpenAI)
	group, err := repo.CreateDraftVersion(ctx, domain.PromptDraftInput{PromptID: key.PromptID, Kind: key.Kind, Name: "group", Provider: key.Provider, BotID: botID, ScopeType: domain.PromptScopeGroup, Content: groupContent, Source: domain.PromptSourceLocal})
	if err != nil {
		t.Fatalf("create group: %v", err)
	}
	if _, _, err := repo.RollbackVersion(ctx, key, group.ID); err != ErrPromptNotFound {
		t.Fatalf("cross-layer rollback err = %v", err)
	}
	if _, _, err := repo.RollbackVersion(ctx, key, second.ID); err != ErrPromptNotArchived {
		t.Fatalf("draft rollback err = %v", err)
	}
	active, found, err := repo.ActiveLayer(ctx, key)
	if err != nil || !found || active.ID != second.ID {
		t.Fatalf("published changed after rejected rollback = %+v found=%v err=%v", active, found, err)
	}
	rolled, archivedID, err := repo.RollbackVersion(ctx, key, first.ID)
	if err != nil || rolled.Status != domain.PromptStatusPublished || archivedID != second.ID {
		t.Fatalf("rollback first = %+v archived=%d err=%v", rolled, archivedID, err)
	}
}

func TestPromptResolveLegacyFallbackAndRestoreBuiltin(t *testing.T) {
	pool := dbtest.Open(t)
	repo, err := NewPromptsRepo(pool)
	if err != nil {
		t.Fatalf("new prompt repo: %v", err)
	}
	ctx := context.Background()
	botID := int64(881003)
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM intent_prompt_versions WHERE bot_id=$1 OR (bot_id=0 AND prompt_id=$2)`, botID, domain.PromptIDChatReply)
	})
	legacyContent := domain.BuiltinPromptFor(domain.PromptKindChatReply, domain.PromptProviderOpenAI)
	legacyContent.OpenAI.SystemPrompt = "legacy global"
	legacy, err := repo.CreateDraftVersion(ctx, domain.PromptDraftInput{PromptID: domain.PromptIDChatReply, Kind: domain.PromptKindChatReply, Name: "legacy", Provider: domain.PromptProviderOpenAI, BotID: 0, ScopeType: domain.PromptScopeAccount, Content: legacyContent, Source: domain.PromptSourceLocal})
	if err != nil {
		t.Fatalf("create legacy: %v", err)
	}
	if err := repo.Publish(ctx, legacy.ID); err != nil {
		t.Fatalf("publish legacy: %v", err)
	}
	service := intentapp.NewPromptService(repo, slog.Default())
	resolved := service.ResolveFor(ctx, domain.PromptIDChatReply, domain.PromptKindChatReply, domain.PromptProviderOpenAI, botID, domain.PromptScopeGroup, 700, map[string]string{"bot_name": "Nagi", "scope_type": "group"})
	if resolved.OpenAI == nil || !strings.Contains(resolved.OpenAI.SystemPrompt, "legacy global") {
		t.Fatalf("legacy fallback = %+v", resolved.OpenAI)
	}
	key := domain.PromptKey{PromptID: domain.PromptIDChatReply, Kind: domain.PromptKindChatReply, Provider: domain.PromptProviderOpenAI, BotID: botID, ScopeType: domain.PromptScopeAccount}
	archivedID, err := repo.RestoreBuiltin(ctx, key)
	if err != nil || archivedID != 0 {
		t.Fatalf("restore without local published = %d err=%v", archivedID, err)
	}
	resolved = service.ResolveFor(ctx, domain.PromptIDChatReply, domain.PromptKindChatReply, domain.PromptProviderOpenAI, botID, domain.PromptScopeGroup, 700, map[string]string{"bot_name": "Nagi", "scope_type": "group"})
	if resolved.OpenAI == nil || strings.Contains(resolved.OpenAI.SystemPrompt, "legacy global") {
		t.Fatalf("restore did not suppress legacy fallback: %+v", resolved.OpenAI)
	}
	botContent := domain.BuiltinPromptFor(domain.PromptKindChatReply, domain.PromptProviderOpenAI)
	botContent.OpenAI.SystemPrompt = "bot account"
	botDraft, err := repo.CreateDraftVersion(ctx, domain.PromptDraftInput{PromptID: domain.PromptIDChatReply, Kind: domain.PromptKindChatReply, Name: "bot account", Provider: domain.PromptProviderOpenAI, BotID: botID, ScopeType: domain.PromptScopeAccount, Content: botContent, Source: domain.PromptSourceLocal})
	if err != nil {
		t.Fatalf("create bot: %v", err)
	}
	if _, _, err := repo.PublishVersion(ctx, botDraft.ID); err != nil {
		t.Fatalf("publish bot: %v", err)
	}
	resolved = service.ResolveFor(ctx, domain.PromptIDChatReply, domain.PromptKindChatReply, domain.PromptProviderOpenAI, botID, domain.PromptScopeGroup, 700, map[string]string{"bot_name": "Nagi", "scope_type": "group"})
	if resolved.OpenAI == nil || !strings.Contains(resolved.OpenAI.SystemPrompt, "bot account") {
		t.Fatalf("bot prompt = %+v", resolved.OpenAI)
	}
	if _, err := repo.RestoreBuiltin(ctx, domain.PromptKey{PromptID: domain.PromptIDChatReply, Kind: domain.PromptKindChatReply, Provider: domain.PromptProviderOpenAI, BotID: botID, ScopeType: domain.PromptScopeAccount}); err != nil {
		t.Fatalf("restore: %v", err)
	}
	resolved = service.ResolveFor(ctx, domain.PromptIDChatReply, domain.PromptKindChatReply, domain.PromptProviderOpenAI, botID, domain.PromptScopeGroup, 700, map[string]string{"bot_name": "Nagi", "scope_type": "group"})
	if resolved.OpenAI == nil || strings.Contains(resolved.OpenAI.SystemPrompt, "legacy global") || strings.Contains(resolved.OpenAI.SystemPrompt, "bot account") {
		t.Fatalf("restore did not return builtin: %+v", resolved.OpenAI)
	}
}
