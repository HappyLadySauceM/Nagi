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

	contacts, err := repo.ListContacts(ctx, domain.PageQuery{Search: "张三", Page: 1, Limit: 10})
	if err != nil || contacts.Total < 1 || len(contacts.Items) == 0 {
		t.Fatalf("list contacts: total=%d items=%d err=%v", contacts.Total, len(contacts.Items), err)
	}
	if contacts.Items[0].Remark != "同学" {
		t.Fatalf("contact remark = %q", contacts.Items[0].Remark)
	}

	groups, err := repo.ListGroups(ctx, domain.PageQuery{Search: "集成", Page: 1, Limit: 10})
	if err != nil || groups.Total < 1 {
		t.Fatalf("list groups: total=%d err=%v", groups.Total, err)
	}

	members, err := repo.ListGroupMembers(ctx, groupID, domain.PageQuery{Page: 1, Limit: 10})
	if err != nil || members.Total != 1 || len(members.Items) != 1 {
		t.Fatalf("list members: total=%d items=%d err=%v", members.Total, len(members.Items), err)
	}
	if members.Items[0].Role != "admin" || members.Items[0].Card != "三哥" {
		t.Fatalf("member = %+v", members.Items[0])
	}
}
