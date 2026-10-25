// Integration test for the directory repository, gated by QQBOT_TEST_DSN.
// 目录仓库的集成测试，以 QQBOT_TEST_DSN 为门控。
package infrastructure

import (
	"context"
	"testing"

	"github.com/HappyLadySauceM/QQBot/internal/directory/domain"
	"github.com/HappyLadySauceM/QQBot/pkg/db/dbtest"
)

// TestDirectoryRoundTrip verifies bot account, contact, group and member upserts
// plus the paginated listings.
// TestDirectoryRoundTrip 验证账号、联系人、群与成员的 upsert 以及分页列表。
func TestDirectoryRoundTrip(t *testing.T) {
	pool := dbtest.Open(t)
	repo, err := NewRepo(pool)
	if err != nil {
		t.Fatalf("new repo: %v", err)
	}
	ctx := context.Background()
	const botID, groupID = 990101, 990102

	if err := repo.UpsertBotAccount(ctx, botID, "测试机器人"); err != nil {
		t.Fatalf("upsert bot account: %v", err)
	}
	accounts, err := repo.ListBotAccounts(ctx)
	if err != nil {
		// 0005 起该查询不再读取 bot_accounts.status；列清单与表结构必须保持一致。
		t.Fatalf("list bot accounts: %v", err)
	}
	// The shared test database may hold rows from other packages, so match ours.
	// 共享测试库可能存有其他包的账号行，因此只校验本用例的这一条。
	foundAccount := false
	for _, account := range accounts {
		if account.BotID == botID {
			foundAccount = true
			if account.Nickname != "测试机器人" {
				t.Fatalf("bot account nickname = %q", account.Nickname)
			}
		}
	}
	if !foundAccount {
		t.Fatalf("bot account %d missing: %+v", botID, accounts)
	}
	if err := repo.UpsertContacts(ctx, botID, []domain.Contact{
		{UserID: 990103, Nickname: "张三", Remark: "同学"},
	}); err != nil {
		t.Fatalf("upsert contacts: %v", err)
	}
	if err := repo.UpsertGroups(ctx, botID, []domain.Group{
		{GroupID: groupID, Name: "集成测试群", MemberCount: 2},
	}); err != nil {
		t.Fatalf("upsert groups: %v", err)
	}
	if err := repo.UpsertGroupMembers(ctx, botID, groupID, []domain.GroupMember{
		{GroupID: groupID, UserID: 990103, Nickname: "张三", Card: "三哥", Role: "admin"},
	}); err != nil {
		t.Fatalf("upsert members: %v", err)
	}

	contacts, err := repo.ListContacts(ctx, botID, domain.PageQuery{Search: "张三", Page: 1, Limit: 10})
	if err != nil || contacts.Total < 1 || len(contacts.Items) == 0 {
		t.Fatalf("list contacts: total=%d items=%d err=%v", contacts.Total, len(contacts.Items), err)
	}
	if contacts.Items[0].Remark != "同学" {
		t.Fatalf("contact remark = %q", contacts.Items[0].Remark)
	}

	groups, err := repo.ListGroups(ctx, botID, domain.PageQuery{Search: "集成", Page: 1, Limit: 10})
	if err != nil || groups.Total < 1 {
		t.Fatalf("list groups: total=%d err=%v", groups.Total, err)
	}

	members, err := repo.ListGroupMembers(ctx, botID, groupID, domain.PageQuery{Page: 1, Limit: 10})
	if err != nil || members.Total != 1 || len(members.Items) != 1 {
		t.Fatalf("list members: total=%d items=%d err=%v", members.Total, len(members.Items), err)
	}
	if members.Items[0].Role != "admin" || members.Items[0].Card != "三哥" {
		t.Fatalf("member = %+v", members.Items[0])
	}

	// QQ numbers and group numbers must be searchable, not just names.
	// 搜索必须能命中 QQ 号与群号，而不仅是名称。
	byUserID, err := repo.ListContacts(ctx, botID, domain.PageQuery{Search: "990103", Page: 1, Limit: 10})
	if err != nil || byUserID.Total != 1 {
		t.Fatalf("search contacts by qq: total=%d err=%v", byUserID.Total, err)
	}
	byGroupID, err := repo.ListGroups(ctx, botID, domain.PageQuery{Search: "990102", Page: 1, Limit: 10})
	if err != nil || byGroupID.Total != 1 {
		t.Fatalf("search groups by group id: total=%d err=%v", byGroupID.Total, err)
	}
	byMemberID, err := repo.ListGroupMembers(ctx, botID, groupID, domain.PageQuery{Search: "990103", Page: 1, Limit: 10})
	if err != nil || byMemberID.Total != 1 {
		t.Fatalf("search members by qq: total=%d err=%v", byMemberID.Total, err)
	}

	// Another account must never see this account's rows.
	// 另一个账号不得看到本账号的数据。
	otherContacts, err := repo.ListContacts(ctx, botID+1, domain.PageQuery{Page: 1, Limit: 10})
	if err != nil || otherContacts.Total != 0 {
		t.Fatalf("cross-account contacts: total=%d err=%v", otherContacts.Total, err)
	}

	// Membership deletions back the group_increase/group_decrease handler.
	// 成员删除方法支撑入群/退群事件处理器。
	if err := repo.DeleteGroupMember(ctx, botID, groupID, 990103); err != nil {
		t.Fatalf("delete group member: %v", err)
	}
	if members, err := repo.ListGroupMembers(ctx, botID, groupID, domain.PageQuery{Page: 1, Limit: 10}); err != nil || members.Total != 0 {
		t.Fatalf("after delete member: total=%d err=%v", members.Total, err)
	}
	if err := repo.UpsertGroupMembers(ctx, botID, groupID, []domain.GroupMember{{GroupID: groupID, UserID: 990104, Nickname: "李四"}}); err != nil {
		t.Fatalf("re-upsert member: %v", err)
	}
	if err := repo.DeleteGroupMembers(ctx, botID, groupID); err != nil {
		t.Fatalf("clear group members: %v", err)
	}
	if members, err := repo.ListGroupMembers(ctx, botID, groupID, domain.PageQuery{Page: 1, Limit: 10}); err != nil || members.Total != 0 {
		t.Fatalf("after clear members: total=%d err=%v", members.Total, err)
	}
	if err := repo.DeleteGroup(ctx, botID, groupID); err != nil {
		t.Fatalf("delete group: %v", err)
	}
	if groups, err := repo.ListGroups(ctx, botID, domain.PageQuery{Page: 1, Limit: 10}); err != nil || groups.Total != 0 {
		t.Fatalf("after delete group: total=%d err=%v", groups.Total, err)
	}
}
