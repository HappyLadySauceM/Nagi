// Integration test for the scope policy repository, gated by NAGI_TEST_DSN.
// 作用域策略仓库的集成测试，以 NAGI_TEST_DSN 为门控。
package infrastructure

import (
	"context"
	"errors"
	"testing"

	scopeapp "github.com/HappyLadySauceM/Nagi/internal/scope/application"
	"github.com/HappyLadySauceM/Nagi/internal/scope/domain"
	"github.com/HappyLadySauceM/Nagi/pkg/db/dbtest"
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
		ToolPermissions: map[string]bool{"qq.group.read": false, "qq.message.write": true},
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
	// Overrides must survive the JSONB round trip; dropping a false entry would
	// silently re-grant a permission the administrator revoked.
	// 覆盖项必须经过 JSONB 往返后仍然存在；丢掉 false 条目会悄悄恢复管理员已撤销的权限。
	if len(stored.ToolPermissions) != 2 || stored.ToolPermissions["qq.group.read"] ||
		!stored.ToolPermissions["qq.message.write"] {
		t.Fatalf("stored tool permissions = %+v", stored.ToolPermissions)
	}
	if _, found, err := repo.Get(ctx, botID, domain.ScopeGroup, 404); err != nil || found {
		t.Fatalf("missing scope: found=%v err=%v", found, err)
	}

	bot := int64(botID)
	list, err := repo.List(ctx, &bot)
	if err != nil || len(list) == 0 {
		t.Fatalf("list: len=%d err=%v", len(list), err)
	}
	// List must return the stored value columns, not just the identity; a row
	// with only identity filled makes the UI show every policy as disabled.
	// List 必须返回值列而不只是身份列；只填身份的回归会让界面把所有策略显示为未启用。
	var listed *domain.Policy
	for i := range list {
		if list[i].ScopeType == domain.ScopeGroup && list[i].ScopeID == groupID {
			listed = &list[i]
			break
		}
	}
	if listed == nil {
		t.Fatalf("list missing group %d: %+v", groupID, list)
	}
	if !listed.Enabled || listed.ReplyMode != domain.ModeIntentBased || !listed.StoreMessages {
		t.Fatalf("listed policy lost its values: %+v", *listed)
	}
	if len(listed.ToolPermissions) != 2 {
		t.Fatalf("listed policy lost its tool permissions: %+v", listed.ToolPermissions)
	}

	// Deleting must report the affected row and remove it from later reads.
	// 删除必须报告命中行，并让后续读取不再返回该策略。
	deleted, err := repo.Delete(ctx, botID, domain.ScopeGroup, groupID)
	if err != nil || !deleted {
		t.Fatalf("delete: deleted=%v err=%v", deleted, err)
	}
	if _, found, err := repo.Get(ctx, botID, domain.ScopeGroup, groupID); err != nil || found {
		t.Fatalf("after delete: found=%v err=%v", found, err)
	}
	if deletedAgain, err := repo.Delete(ctx, botID, domain.ScopeGroup, groupID); err != nil || deletedAgain {
		t.Fatalf("second delete should be a no-op: deleted=%v err=%v", deletedAgain, err)
	}
}

func TestTemplateVersionConflictDoesNotCreateIndependentPolicy(t *testing.T) {
	pool := dbtest.Open(t)
	repo, err := NewRepo(pool)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	const botID = 990111
	if _, err := pool.Exec(ctx, "INSERT INTO bot_accounts (bot_id, nickname) VALUES ($1, 'template-test') ON CONFLICT DO NOTHING", botID); err != nil {
		t.Fatal(err)
	}
	defer pool.Exec(ctx, "DELETE FROM scope_policies WHERE bot_id=$1", botID)
	template := domain.Default(domain.ScopePrivate, 0)
	if err := repo.Upsert(ctx, botID, template); err != nil {
		t.Fatal(err)
	}
	stored, _, err := repo.Get(ctx, botID, domain.ScopePrivate, 0)
	if err != nil {
		t.Fatal(err)
	}
	oldVersion := stored.Version
	if err := repo.Upsert(ctx, botID, template); err != nil {
		t.Fatal(err)
	}
	independent := domain.Default(domain.ScopePrivate, 123)
	independent.Enabled = true
	independent.ExpectedTemplateVersion = &oldVersion
	if err := repo.Upsert(ctx, botID, independent); !errors.Is(err, domain.ErrTemplateConflict) {
		t.Fatalf("stale save: %v", err)
	}
	if _, found, err := repo.Get(ctx, botID, domain.ScopePrivate, 123); err != nil || found {
		t.Fatalf("stale save persisted: %v %v", found, err)
	}
	latest, _, err := repo.Get(ctx, botID, domain.ScopePrivate, 0)
	if err != nil {
		t.Fatal(err)
	}
	independent.ExpectedTemplateVersion = &latest.Version
	if err := repo.Upsert(ctx, botID, independent); err != nil {
		t.Fatal(err)
	}
}

func TestExplicitZeroPolicyLimitsRoundTrip(t *testing.T) {
	pool := dbtest.Open(t)
	repo, err := NewRepo(pool)
	if err != nil {
		t.Fatal(err)
	}
	service, err := scopeapp.NewService(repo)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	const botID = 990112
	if _, err := pool.Exec(ctx, "INSERT INTO bot_accounts(bot_id,nickname) VALUES($1,'zero-limits') ON CONFLICT DO NOTHING", botID); err != nil {
		t.Fatal(err)
	}
	defer pool.Exec(ctx, "DELETE FROM scope_policies WHERE bot_id=$1", botID)
	policy := domain.Default(domain.ScopePrivate, 155)
	policy.MinReplyIntervalMS = 0
	policy.MaxRepliesPerHour = 0
	policy.MaxMessagesPerRun = 0
	policy.MaxTotalCharsPerRun = 0
	if err := service.Upsert(ctx, botID, policy); err != nil {
		t.Fatal(err)
	}
	persisted, found, err := repo.Get(ctx, botID, domain.ScopePrivate, 155)
	if err != nil || !found || persisted.MinReplyIntervalMS != 0 || persisted.MaxRepliesPerHour != 0 ||
		persisted.MaxMessagesPerRun != 0 || persisted.MaxTotalCharsPerRun != 0 || persisted.MaxCharsPerMessage != 4500 {
		t.Fatalf("zero values changed in storage: %+v %v", persisted, err)
	}
	resolved, err := service.Resolve(ctx, botID, domain.ScopePrivate, 155)
	if err != nil || resolved.MinReplyIntervalMS != 0 || resolved.MaxRepliesPerHour != 0 ||
		resolved.MaxMessagesPerRun != 0 || resolved.MaxTotalCharsPerRun != 0 {
		t.Fatalf("zero values changed in resolution: %+v %v", resolved, err)
	}
}
