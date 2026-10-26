// Tests for directory sync mapping and account isolation.
// 目录同步映射与账号隔离的测试。
package application

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"

	domain "github.com/HappyLadySauceM/Nagi/internal/directory/domain"
	obcontracts "github.com/HappyLadySauceM/Nagi/internal/onebot/contracts"
	obdomain "github.com/HappyLadySauceM/Nagi/internal/onebot/domain"
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
	member  obdomain.GroupMemberInfo
	group   obdomain.GroupInfo
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
func (f *fakeInfo) GetGroupMemberInfo(context.Context, obdomain.GroupID, obdomain.UserID, bool) (obdomain.GroupMemberInfo, error) {
	return f.member, nil
}
func (f *fakeInfo) GetGroupInfo(context.Context, obdomain.GroupID, bool) (obdomain.GroupInfo, error) {
	return f.group, nil
}

// fakeInfos resolves one reader per account.
// fakeInfos 按账号解析对应的查询能力。
type fakeInfos map[int64]obcontracts.InfoReader

func (f fakeInfos) InfoReader(botID int64) (obcontracts.InfoReader, bool) {
	reader, ok := f[botID]
	return reader, ok
}

// fakeRepo records upsert calls and account rows.
// fakeRepo 记录 upsert 调用与账号行。
type fakeRepo struct {
	accounts map[int64]bool
	contacts map[int64][]domain.Contact
	groups   map[int64][]domain.Group
	members  map[int64][]domain.GroupMember
	page     domain.PageQuery
	pageBot  int64
}

func newFakeRepo() *fakeRepo {
	return &fakeRepo{
		accounts: make(map[int64]bool),
		contacts: make(map[int64][]domain.Contact),
		groups:   make(map[int64][]domain.Group),
		members:  make(map[int64][]domain.GroupMember),
	}
}

func (f *fakeRepo) UpsertBotAccount(_ context.Context, botID int64, _ string) error {
	f.accounts[botID] = true
	return nil
}
func (f *fakeRepo) BotAccountExists(_ context.Context, botID int64) (bool, error) {
	return f.accounts[botID], nil
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
func (f *fakeRepo) ListContacts(_ context.Context, botID int64, q domain.PageQuery) (domain.ContactPage, error) {
	f.pageBot, f.page = botID, q
	return domain.ContactPage{}, nil
}
func (f *fakeRepo) ListGroups(context.Context, int64, domain.PageQuery) (domain.GroupPage, error) {
	return domain.GroupPage{}, nil
}
func (f *fakeRepo) ListGroupMembers(context.Context, int64, int64, domain.PageQuery) (domain.MemberPage, error) {
	return domain.MemberPage{}, nil
}
func (f *fakeRepo) DeleteGroupMember(_ context.Context, _ int64, groupID, userID int64) error {
	kept := make([]domain.GroupMember, 0, len(f.members[groupID]))
	for _, member := range f.members[groupID] {
		if member.UserID != userID {
			kept = append(kept, member)
		}
	}
	f.members[groupID] = kept
	return nil
}
func (f *fakeRepo) DeleteGroupMembers(_ context.Context, _ int64, groupID int64) error {
	delete(f.members, groupID)
	return nil
}
func (f *fakeRepo) DeleteGroup(_ context.Context, botID, groupID int64) error {
	kept := make([]domain.Group, 0, len(f.groups[botID]))
	for _, group := range f.groups[botID] {
		if group.GroupID != groupID {
			kept = append(kept, group)
		}
	}
	f.groups[botID] = kept
	return nil
}
func (f *fakeRepo) ListBotAccounts(context.Context) ([]domain.BotAccount, error) {
	out := make([]domain.BotAccount, 0, len(f.accounts))
	for id := range f.accounts {
		out = append(out, domain.BotAccount{BotID: id})
	}
	return out, nil
}

func newTestService(info fakeInfos, repo *fakeRepo) *Service {
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
	svc := newTestService(fakeInfos{10000: info}, repo)

	result, err := svc.Sync(context.Background(), 10000)
	if err != nil {
		t.Fatalf("sync: %v", err)
	}
	if result.BotID != 10000 || result.Contacts != 2 || result.Groups != 1 {
		t.Fatalf("sync result = %+v", result)
	}
	if !repo.accounts[10000] {
		t.Fatalf("bot account row missing: %+v", repo.accounts)
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

// TestSyncRejectsEndpointServingAnotherAccount verifies that a mismatched
// login identity never writes rows under the requested account.
// TestSyncRejectsEndpointServingAnotherAccount 验证登录身份不一致时不会给请求账号写数据。
func TestSyncRejectsEndpointServingAnotherAccount(t *testing.T) {
	info := &fakeInfo{login: obdomain.LoginInfo{UserID: 20000}}
	repo := newFakeRepo()
	svc := newTestService(fakeInfos{10000: info}, repo)

	if _, err := svc.Sync(context.Background(), 10000); err == nil {
		t.Fatal("expected identity mismatch error")
	}
	if len(repo.contacts) != 0 || len(repo.groups) != 0 {
		t.Fatalf("mismatched endpoint must not write rows: %+v", repo)
	}
}

// TestSyncWithoutConnectionReportsOffline verifies the account must be connected.
// TestSyncWithoutConnectionReportsOffline 验证账号必须在线。
func TestSyncWithoutConnectionReportsOffline(t *testing.T) {
	if _, err := newTestService(fakeInfos{}, newFakeRepo()).Sync(context.Background(), 10000); !errors.Is(err, ErrAccountOffline) {
		t.Fatalf("expected offline error, got %v", err)
	}
}

// TestSyncGroupMembersRequiresSyncedAccount verifies that member sync fails with a
// clear error before any account sync happened.
// TestSyncGroupMembersRequiresSyncedAccount 验证未同步账号前成员同步返回明确错误。
func TestSyncGroupMembersRequiresSyncedAccount(t *testing.T) {
	svc := newTestService(fakeInfos{10000: &fakeInfo{}}, newFakeRepo())
	_, err := svc.SyncGroupMembers(context.Background(), 10000, 7)
	if !errors.Is(err, ErrBotNotSynced) {
		t.Fatalf("expected not-synced error, got %v", err)
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
	svc := newTestService(fakeInfos{10000: info}, repo)
	if _, err := svc.Sync(context.Background(), 10000); err != nil {
		t.Fatalf("sync: %v", err)
	}
	if _, err := svc.SyncGroupMembers(context.Background(), 10000, 7); err != nil {
		t.Fatalf("sync members: %v", err)
	}
	members := repo.members[7]
	if len(members) != 1 || members[0].UserID != 5 || members[0].Role != "admin" {
		t.Fatalf("members = %+v", members)
	}
}

// TestContactsScopesQueryToAccount verifies listings forward the account id.
// TestContactsScopesQueryToAccount 验证列表查询会把账号传给仓库。
func TestContactsScopesQueryToAccount(t *testing.T) {
	repo := newFakeRepo()
	svc := newTestService(fakeInfos{}, repo)
	if _, err := svc.Contacts(context.Background(), 30000, domain.PageQuery{Page: 1}); err != nil {
		t.Fatalf("contacts: %v", err)
	}
	if repo.pageBot != 30000 {
		t.Fatalf("page bot id = %d, want 30000", repo.pageBot)
	}
}

// TestApplyMemberJoinUpsertsOneMember verifies a join notice writes exactly the
// joined member and refreshes the group member count.
// TestApplyMemberJoinUpsertsOneMember 验证入群通知只写入该成员并刷新群成员数。
func TestApplyMemberJoinUpsertsOneMember(t *testing.T) {
	info := &fakeInfo{
		login:  obdomain.LoginInfo{UserID: 10000},
		member: obdomain.GroupMemberInfo{UserID: 42, Nickname: "新人", Card: "新同学", Role: "member"},
		group:  obdomain.GroupInfo{GroupID: 7, GroupName: "测试群", MemberCount: 4},
	}
	repo := newFakeRepo()
	repo.accounts[10000] = true
	svc := newTestService(fakeInfos{10000: info}, repo)
	if err := svc.ApplyMemberJoin(context.Background(), 10000, 7, 42); err != nil {
		t.Fatalf("apply join: %v", err)
	}
	members := repo.members[7]
	if len(members) != 1 || members[0].UserID != 42 || members[0].Card != "新同学" {
		t.Fatalf("members = %+v", members)
	}
	groups := repo.groups[10000]
	if len(groups) != 1 || groups[0].MemberCount != 4 {
		t.Fatalf("groups = %+v", groups)
	}
}

// TestApplyMemberLeaveDeletesOneMember verifies a leave notice removes only the
// member that left.
// TestApplyMemberLeaveDeletesOneMember 验证退群通知只删除离开的成员。
func TestApplyMemberLeaveDeletesOneMember(t *testing.T) {
	info := &fakeInfo{login: obdomain.LoginInfo{UserID: 10000}}
	repo := newFakeRepo()
	repo.members[7] = []domain.GroupMember{{GroupID: 7, UserID: 42}, {GroupID: 7, UserID: 43}}
	svc := newTestService(fakeInfos{10000: info}, repo)
	if err := svc.ApplyMemberLeave(context.Background(), 10000, 7, 42); err != nil {
		t.Fatalf("apply leave: %v", err)
	}
	members := repo.members[7]
	if len(members) != 1 || members[0].UserID != 43 {
		t.Fatalf("members = %+v", members)
	}
}

// TestApplyBotLeftClearsGroup verifies the bot being removed wipes the group.
// TestApplyBotLeftClearsGroup 验证机器人被移出群时清空该群目录数据。
func TestApplyBotLeftClearsGroup(t *testing.T) {
	repo := newFakeRepo()
	repo.members[7] = []domain.GroupMember{{GroupID: 7, UserID: 42}}
	repo.groups[10000] = []domain.Group{{GroupID: 7, Name: "测试群"}}
	svc := newTestService(fakeInfos{10000: &fakeInfo{}}, repo)
	if err := svc.ApplyBotLeft(context.Background(), 10000, 7); err != nil {
		t.Fatalf("apply bot left: %v", err)
	}
	if _, ok := repo.members[7]; ok {
		t.Fatalf("members should be cleared: %+v", repo.members)
	}
	if len(repo.groups[10000]) != 0 {
		t.Fatalf("groups = %+v", repo.groups[10000])
	}
}

// TestApplyMemberJoinWithoutConnectionReportsOffline verifies an offline account
// cannot apply membership notices.
// TestApplyMemberJoinWithoutConnectionReportsOffline 验证账号离线时不处理成员事件。
func TestApplyMemberJoinWithoutConnectionReportsOffline(t *testing.T) {
	svc := newTestService(fakeInfos{}, newFakeRepo())
	if err := svc.ApplyMemberJoin(context.Background(), 10000, 7, 42); !errors.Is(err, ErrAccountOffline) {
		t.Fatalf("expected offline error, got %v", err)
	}
}

// TestApplyMemberJoinRequiresSyncedAccount verifies a join notice cannot write
// member rows before the account row exists.
// TestApplyMemberJoinRequiresSyncedAccount 验证账号行不存在时入群通知不写成员行。
func TestApplyMemberJoinRequiresSyncedAccount(t *testing.T) {
	info := &fakeInfo{
		login:  obdomain.LoginInfo{UserID: 10000},
		member: obdomain.GroupMemberInfo{UserID: 42},
	}
	repo := newFakeRepo()
	svc := newTestService(fakeInfos{10000: info}, repo)
	if err := svc.ApplyMemberJoin(context.Background(), 10000, 7, 42); !errors.Is(err, ErrBotNotSynced) {
		t.Fatalf("expected not-synced error, got %v", err)
	}
	if len(repo.members) != 0 {
		t.Fatalf("members must stay untouched: %+v", repo.members)
	}
}

// TestApplyBotJoinedSyncsFullMemberList verifies the bot joining a group pulls
// every member and refreshes the group metadata.
// TestApplyBotJoinedSyncsFullMemberList 验证机器人入群后全量同步成员并刷新群信息。
func TestApplyBotJoinedSyncsFullMemberList(t *testing.T) {
	info := &fakeInfo{
		login:   obdomain.LoginInfo{UserID: 10000},
		members: []obdomain.GroupMemberInfo{{UserID: 42}, {UserID: 43}},
		group:   obdomain.GroupInfo{GroupID: 7, GroupName: "测试群", MemberCount: 2},
	}
	repo := newFakeRepo()
	repo.accounts[10000] = true
	svc := newTestService(fakeInfos{10000: info}, repo)
	if err := svc.ApplyBotJoined(context.Background(), 10000, 7); err != nil {
		t.Fatalf("apply bot joined: %v", err)
	}
	if members := repo.members[7]; len(members) != 2 {
		t.Fatalf("members = %+v", members)
	}
	if groups := repo.groups[10000]; len(groups) != 1 || groups[0].MemberCount != 2 {
		t.Fatalf("groups = %+v", groups)
	}
}

// TestApplyMemberLeaveWorksWhileOffline verifies a leave notice still removes
// the row when the endpoint is gone.
// TestApplyMemberLeaveWorksWhileOffline 验证端点离线时退群通知仍能删除成员行。
func TestApplyMemberLeaveWorksWhileOffline(t *testing.T) {
	repo := newFakeRepo()
	repo.members[7] = []domain.GroupMember{{GroupID: 7, UserID: 42}}
	svc := newTestService(fakeInfos{}, repo)
	if err := svc.ApplyMemberLeave(context.Background(), 10000, 7, 42); err != nil {
		t.Fatalf("apply leave while offline: %v", err)
	}
	if len(repo.members[7]) != 0 {
		t.Fatalf("member should be removed: %+v", repo.members[7])
	}
}
