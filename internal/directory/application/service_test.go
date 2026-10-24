// Tests for directory sync mapping and preconditions.
// 目录同步映射与前置条件的测试。
package application

import (
	"context"
	"io"
	"log/slog"
	"strings"
	"testing"

	domain "github.com/HappyLadySauceM/QQBot/internal/directory/domain"
	obcontracts "github.com/HappyLadySauceM/QQBot/internal/onebot/contracts"
	obdomain "github.com/HappyLadySauceM/QQBot/internal/onebot/domain"
)

// testLogger discards log output.
// testLogger 丢弃日志输出。
func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// fakeInfo satisfies InfoReader by embedding the interface and overriding used methods.
// fakeInfo 内嵌 InfoReader 接口并重写用到的方法。
type fakeInfo struct {
	obcontracts.InfoReader
	login   obdomain.LoginInfo
	friends []obdomain.FriendInfo
	groups  []obdomain.GroupInfo
	members []obdomain.GroupMemberInfo
}

func (f *fakeInfo) GetLoginInfo(context.Context) (obdomain.LoginInfo, error) { return f.login, nil }
func (f *fakeInfo) GetFriendList(context.Context) ([]obdomain.FriendInfo, error) {
	return f.friends, nil
}
func (f *fakeInfo) GetGroupList(context.Context, bool) ([]obdomain.GroupInfo, error) {
	return f.groups, nil
}
func (f *fakeInfo) GetGroupMemberList(context.Context, obdomain.GroupID) ([]obdomain.GroupMemberInfo, error) {
	return f.members, nil
}

// fakeRepo records upsert calls.
// fakeRepo 记录 upsert 调用。
type fakeRepo struct {
	account      []int64
	storedBotID  int64
	hasStoredBot bool
	contacts     map[int64][]domain.Contact
	groups       map[int64][]domain.Group
	members      map[int64][]domain.GroupMember
}

func newFakeRepo() *fakeRepo {
	return &fakeRepo{
		contacts: make(map[int64][]domain.Contact),
		groups:   make(map[int64][]domain.Group),
		members:  make(map[int64][]domain.GroupMember),
	}
}

func (f *fakeRepo) UpsertBotAccount(_ context.Context, botID int64, _ string) error {
	f.account = append(f.account, botID)
	return nil
}
func (f *fakeRepo) UpsertContacts(_ context.Context, botID int64, cs []domain.Contact) error {
	f.contacts[botID] = cs
	return nil
}
func (f *fakeRepo) UpsertGroups(_ context.Context, botID int64, gs []domain.Group) error {
	f.groups[botID] = gs
	return nil
}
func (f *fakeRepo) UpsertGroupMembers(_ context.Context, botID, groupID int64, ms []domain.GroupMember) error {
	f.members[groupID] = ms
	return nil
}
func (f *fakeRepo) ListContacts(context.Context, domain.PageQuery) (domain.ContactPage, error) {
	return domain.ContactPage{}, nil
}
func (f *fakeRepo) ListGroups(context.Context, domain.PageQuery) (domain.GroupPage, error) {
	return domain.GroupPage{}, nil
}
func (f *fakeRepo) ListGroupMembers(context.Context, int64, domain.PageQuery) (domain.MemberPage, error) {
	return domain.MemberPage{}, nil
}

// LatestBotID returns the stored account used by restart recovery tests.
// LatestBotID 返回重启恢复测试使用的存储账号。
func (f *fakeRepo) LatestBotID(context.Context) (int64, bool, error) {
	return f.storedBotID, f.hasStoredBot, nil
}

func newTestService(info *fakeInfo, repo *fakeRepo) *Service {
	svc, err := NewService(repo, info, testLogger())
	if err != nil {
		panic(err)
	}
	return svc
}

// TestSyncUpsertsAccountContactsAndGroups verifies that Sync maps OneBot results
// into repository upserts with correct values and counts.
// TestSyncUpsertsAccountContactsAndGroups 验证 Sync 将 OneBot 结果正确映射为
// 仓库 upsert 的值与数量。
func TestSyncUpsertsAccountContactsAndGroups(t *testing.T) {
	info := &fakeInfo{
		login:   obdomain.LoginInfo{UserID: 10000, Nickname: "小雪"},
		friends: []obdomain.FriendInfo{{UserID: 1, Nickname: "甲", Remark: "备注甲"}, {UserID: 2, Nickname: "乙"}},
		groups:  []obdomain.GroupInfo{{GroupID: 7, GroupName: "测试群", MemberCount: 3}},
	}
	repo := newFakeRepo()
	svc := newTestService(info, repo)

	result, err := svc.Sync(context.Background())
	if err != nil {
		t.Fatalf("sync: %v", err)
	}
	if result.BotID != 10000 || result.Contacts != 2 || result.Groups != 1 {
		t.Fatalf("sync result = %+v", result)
	}
	if len(repo.account) != 1 || repo.account[0] != 10000 {
		t.Fatalf("bot account upserts = %v", repo.account)
	}
	contacts := repo.contacts[10000]
	if len(contacts) != 2 || contacts[0].Remark != "备注甲" || contacts[1].UserID != 2 {
		t.Fatalf("contacts = %+v", contacts)
	}
	groups := repo.groups[10000]
	if len(groups) != 1 || groups[0].GroupID != 7 || groups[0].MemberCount != 3 {
		t.Fatalf("groups = %+v", groups)
	}
}

// TestSyncGroupMembersRequiresSyncedAccount verifies that member sync fails with a
// clear error before any account sync happened.
// TestSyncGroupMembersRequiresSyncedAccount 验证未同步账号前成员同步返回明确错误。
func TestSyncGroupMembersRequiresSyncedAccount(t *testing.T) {
	svc := newTestService(&fakeInfo{}, newFakeRepo())
	_, err := svc.SyncGroupMembers(context.Background(), 7)
	if err == nil || !strings.Contains(err.Error(), "not synced") {
		t.Fatalf("expected not-synced error, got %v", err)
	}
}

// TestSyncGroupMembersRecoversBotFromRepository verifies that a member refresh
// works after a restart, when only the stored account row is left.
// TestSyncGroupMembersRecoversBotFromRepository 验证进程重启后仅剩存储账号行时，
// 成员刷新仍能恢复账号并写入成员。
func TestSyncGroupMembersRecoversBotFromRepository(t *testing.T) {
	info := &fakeInfo{members: []obdomain.GroupMemberInfo{{UserID: 5, Nickname: "丙"}}}
	repo := newFakeRepo()
	repo.storedBotID = 20000
	repo.hasStoredBot = true
	svc := newTestService(info, repo)

	if _, err := svc.SyncGroupMembers(context.Background(), 7); err != nil {
		t.Fatalf("sync members after restart: %v", err)
	}
	members := repo.members[7]
	if len(members) != 1 || members[0].UserID != 5 {
		t.Fatalf("members = %+v", members)
	}
	if len(repo.account) != 0 {
		t.Fatalf("recovery must not upsert an account row: %v", repo.account)
	}
}

// TestSyncGroupMembersUpsertsMembers verifies the member mapping after a sync.
// TestSyncGroupMembersUpsertsMembers 验证账号同步后的成员映射。
func TestSyncGroupMembersUpsertsMembers(t *testing.T) {
	info := &fakeInfo{
		login:   obdomain.LoginInfo{UserID: 10000},
		members: []obdomain.GroupMemberInfo{{UserID: 5, Nickname: "丙", Card: "管理", Role: obdomain.MemberRole("admin")}},
	}
	repo := newFakeRepo()
	svc := newTestService(info, repo)
	if _, err := svc.Sync(context.Background()); err != nil {
		t.Fatalf("sync: %v", err)
	}
	if _, err := svc.SyncGroupMembers(context.Background(), 7); err != nil {
		t.Fatalf("sync members: %v", err)
	}
	members := repo.members[7]
	if len(members) != 1 || members[0].UserID != 5 || members[0].Role != "admin" {
		t.Fatalf("members = %+v", members)
	}
}
