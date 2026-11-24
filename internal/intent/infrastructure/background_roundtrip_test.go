package infrastructure

import (
	"context"
	"testing"

	intentapp "github.com/HappyLadySauceM/Nagi/internal/intent/application"
	"github.com/HappyLadySauceM/Nagi/internal/intent/domain"
	"github.com/HappyLadySauceM/Nagi/pkg/db/dbtest"
)

func TestPublishedIntentRoleRoundTrip(t *testing.T) {
	pool := dbtest.Open(t)
	repo, err := NewPromptsRepo(pool)
	if err != nil {
		t.Fatal(err)
	}
	svc := intentapp.NewPromptService(repo, nil, PromptCodec{})
	ctx := context.Background()
	const bot, other = 938611, 938612
	t.Cleanup(func() {
		if _, err := pool.Exec(ctx, `DELETE FROM intent_prompt_versions WHERE bot_id IN ($1,$2)`, bot, other); err != nil {
			t.Error(err)
		}
	})
	create := func(botID int64, scope string, fields *domain.PromptIntentContext) domain.PromptRecord {
		content := domain.BuiltinPromptFor(domain.PromptKindChatReply, domain.PromptProviderOpenAI)
		if scope == domain.PromptScopeGroup {
			content.OpenAI = &domain.OpenAIPrompt{}
		}
		content.IntentContext = fields
		row, err := svc.CreateDraftFor(ctx, domain.PromptIDChatReply, domain.PromptKindChatReply, domain.PromptProviderOpenAI, botID, scope, &content, "", 0, "", "", "", "")
		if err != nil {
			t.Fatal(err)
		}
		return row
	}
	publish := func(row domain.PromptRecord) {
		if _, _, err := svc.PublishVersion(ctx, row.ID); err != nil {
			t.Fatal(err)
		}
	}
	live := func(botID int64, kind string, group int64) (*domain.PromptIntentContext, []domain.BackgroundRoleLayer) {
		role, refs, ok := svc.IntentRoleBackground(ctx, domain.Input{Task: domain.Task{BotID: botID, Kind: kind, TargetID: group}})
		if !ok {
			t.Fatal("cannot resolve role")
		}
		return role, refs
	}
	old := create(bot, domain.PromptScopeAccount, nil)
	publish(old)
	one := create(bot, domain.PromptScopeAccount, &domain.PromptIntentContext{Identity: "published-role", Responsibilities: []string{"synthetic-duty {{bot_id}}"}})
	if role, _ := live(bot, "group", 1); role != nil {
		t.Fatalf("draft or legacy background = %+v", role)
	}
	preview := svc.PreviewForVersion(ctx, one, "group", 1, nil)
	if preview.IntentContext == nil || preview.IntentContext.Identity != "published-role" {
		t.Fatalf("draft preview lost role: %+v", preview.IntentContext)
	}
	publish(one)
	group := create(bot, domain.PromptScopeGroup, &domain.PromptIntentContext{GroupConventions: []string{"shared-group-type"}})
	publish(group)
	for _, target := range []int64{1, 2} {
		role, refs := live(bot, "group", target)
		if role == nil || role.Identity != "published-role" || len(role.Responsibilities) != 1 || role.Responsibilities[0] != "synthetic-duty 938611" || len(role.GroupConventions) != 1 || len(refs) != 2 {
			t.Fatalf("group %d role=%+v refs=%+v", target, role, refs)
		}
		frozen, ok := svc.FrozenIntentRole(ctx, domain.Input{Task: domain.Task{BotID: bot, Kind: "group", TargetID: target}}, refs, intentapp.RoleBackgroundHash(role))
		if !ok || frozen.Identity != role.Identity {
			t.Fatalf("frozen role not reproducible: %+v", frozen)
		}
	}
	if role, _ := live(other, "group", 1); role != nil {
		t.Fatalf("role crossed accounts: %+v", role)
	}
	if role, refs := live(bot, "private", 1); role != nil || len(refs) != 0 {
		t.Fatalf("group role crossed scope: %+v", role)
	}
	key := domain.PromptKey{PromptID: one.PromptID, Kind: one.Kind, Provider: one.Provider, BotID: bot, ScopeType: domain.PromptScopeAccount}
	if _, _, err := svc.RollbackVersion(ctx, key, old.ID); err != nil {
		t.Fatal(err)
	}
	if role, _ := live(bot, "group", 1); role == nil || role.Identity != "" || len(role.GroupConventions) != 1 {
		t.Fatalf("rollback mismatched layer: %+v", role)
	}
}

func TestParsePromptContentForWithVariablesAllowsExtra(t *testing.T) {
	p := domain.BuiltinPromptFor(domain.PromptKindChatReply, domain.PromptProviderOpenAI)
	p.OpenAI.SystemPrompt = "你好 {{user_name}}"
	content := domain.PromptContentJSON(p)
	if _, err := (PromptCodec{}).Decode(domain.PromptKindChatReply, domain.PromptProviderOpenAI, content, nil); err == nil {
		t.Fatal("stored content with custom variable accepted without extra")
	}
	parsed, err := (PromptCodec{}).Decode(domain.PromptKindChatReply, domain.PromptProviderOpenAI, content, []string{"user_name"})
	if err != nil {
		t.Fatalf("parse with extra: %v", err)
	}
	if parsed.OpenAI.SystemPrompt != p.OpenAI.SystemPrompt {
		t.Fatalf("parsed = %+v", parsed.OpenAI)
	}
}
