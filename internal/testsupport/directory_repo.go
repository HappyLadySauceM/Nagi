// Package testsupport provides shared in-memory fakes for unit tests.
// testsupport 包提供供单元测试复用的内存版 fake。
package testsupport

import (
	"context"

	"github.com/HappyLadySauceM/Nagi/internal/directory/domain"
)

// DirectoryRepo is an in-memory directory repository.
// DirectoryRepo 是内存版目录仓库。
type DirectoryRepo struct {
	Accounts map[int64]string
	Contacts map[int64][]domain.Contact
	Groups   map[int64][]domain.Group
	Members  map[int64][]domain.GroupMember
	Roles    map[int64]string
	Page     domain.PageQuery
	PageBot  int64
}

// NewDirectoryRepo builds an empty directory repository.
// NewDirectoryRepo 构造空的目录仓库。
func NewDirectoryRepo() *DirectoryRepo {
	return &DirectoryRepo{
		Accounts: make(map[int64]string),
		Contacts: make(map[int64][]domain.Contact),
		Groups:   make(map[int64][]domain.Group),
		Members:  make(map[int64][]domain.GroupMember),
		Roles:    make(map[int64]string),
	}
}

func (f *DirectoryRepo) UpsertBotAccount(_ context.Context, botID int64, nickname string) error {
	f.Accounts[botID] = nickname
	return nil
}
func (f *DirectoryRepo) BotAccountExists(_ context.Context, botID int64) (bool, error) {
	_, ok := f.Accounts[botID]
	return ok, nil
}
func (f *DirectoryRepo) UpsertContacts(_ context.Context, botID int64, cs []domain.Contact) error {
	f.Contacts[botID] = cs
	return nil
}
func (f *DirectoryRepo) UpsertGroups(_ context.Context, botID int64, gs []domain.Group) error {
	f.Groups[botID] = gs
	return nil
}
func (f *DirectoryRepo) UpsertGroupMembers(_ context.Context, botID, groupID int64, ms []domain.GroupMember) error {
	f.Members[groupID] = ms
	for _, member := range ms {
		f.Roles[groupID] = member.Role
	}
	return nil
}
func (f *DirectoryRepo) BotGroupRole(_ context.Context, _ int64, groupID int64) (string, error) {
	return f.Roles[groupID], nil
}
func (f *DirectoryRepo) ListContacts(_ context.Context, botID int64, q domain.PageQuery) (domain.ContactPage, error) {
	f.PageBot, f.Page = botID, q
	return domain.ContactPage{}, nil
}
func (f *DirectoryRepo) ListGroups(context.Context, int64, domain.PageQuery) (domain.GroupPage, error) {
	return domain.GroupPage{}, nil
}
func (f *DirectoryRepo) ListGroupMembers(context.Context, int64, int64, domain.PageQuery) (domain.MemberPage, error) {
	return domain.MemberPage{}, nil
}
func (f *DirectoryRepo) DeleteGroupMember(_ context.Context, _ int64, groupID, userID int64) error {
	kept := make([]domain.GroupMember, 0, len(f.Members[groupID]))
	for _, member := range f.Members[groupID] {
		if member.UserID != userID {
			kept = append(kept, member)
		}
	}
	f.Members[groupID] = kept
	return nil
}
func (f *DirectoryRepo) DeleteGroupMembers(_ context.Context, _ int64, groupID int64) error {
	delete(f.Members, groupID)
	return nil
}
func (f *DirectoryRepo) DeleteGroup(_ context.Context, botID, groupID int64) error {
	kept := make([]domain.Group, 0, len(f.Groups[botID]))
	for _, group := range f.Groups[botID] {
		if group.GroupID != groupID {
			kept = append(kept, group)
		}
	}
	f.Groups[botID] = kept
	return nil
}
func (f *DirectoryRepo) ListBotAccounts(context.Context) ([]domain.BotAccount, error) {
	out := make([]domain.BotAccount, 0, len(f.Accounts))
	for id, nick := range f.Accounts {
		out = append(out, domain.BotAccount{BotID: id, Nickname: nick})
	}
	return out, nil
}
