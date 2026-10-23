// Integration test for the scope policy repository, gated by QQBOT_TEST_DSN.
// 作用域策略仓库的集成测试，以 QQBOT_TEST_DSN 为门控。
package infrastructure

import (
	"context"
	"testing"

	"github.com/HappyLadySauceM/QQBot/internal/scope/domain"
	"github.com/HappyLadySauceM/QQBot/pkg/db/dbtest"
)

// TestScopePolicyRoundTrip verifies upsert, get, list and the not-found path.
// TestScopePolicyRoundTrip 验证 upsert、get、list 与未找到路径。
func TestScopePolicyRoundTrip(t *testing.T) {
	pool := dbtest.Open(t)
	repo, err := NewRepo(pool)
	if err != nil {
		t.Fatalf("new repo: %v", err)
	}
	ctx := context.Background()
	const botID, groupID = 990001, 990002

	// scope_policies references bot_accounts, so seed the account row first.
	// scope_policies 关联 bot_accounts，需先写入账号行。
	if _, err := pool.Exec(ctx,
		`INSERT INTO bot_accounts (bot_id, nickname) VALUES ($1, 'scope-test') ON CONFLICT (bot_id) DO NOTHING`,
		botID,
	); err != nil {
		t.Fatalf("seed bot account: %v", err)
	}

	if err := repo.Upsert(ctx, botID, domain.Policy{
		ScopeType: domain.ScopeGroup, ScopeID: groupID,
		Enabled: true, ReplyMode: domain.ModeIntentBased, StoreMessages: true,
		ToolPolicy: []string{"send_message"},
	}); err != nil {
		t.Fatalf("upsert: %v", err)
	}

	stored, found, err := repo.Get(ctx, botID, domain.ScopeGroup, groupID)
	if err != nil || !found {
		t.Fatalf("get: found=%v err=%v", found, err)
	}
	if !stored.Enabled || stored.ReplyMode != domain.ModeIntentBased || !stored.StoreMessages {
		t.Fatalf("stored policy = %+v", stored)
	}
	if len(stored.ToolPolicy) != 1 || stored.ToolPolicy[0] != "send_message" {
		t.Fatalf("tool policy = %v", stored.ToolPolicy)
	}

	if _, found, err := repo.Get(ctx, botID, domain.ScopeGroup, 404); err != nil || found {
		t.Fatalf("missing scope: found=%v err=%v", found, err)
	}

	bot := int64(botID)
	list, err := repo.List(ctx, &bot)
	if err != nil || len(list) == 0 {
		t.Fatalf("list: len=%d err=%v", len(list), err)
	}
}
