// Package application implements directory sync and query use cases.
// directory/application 实现目录同步与查询用例。
package application

import (
	"context"
	"fmt"
	"log/slog"
	"sync"

	"github.com/HappyLadySauceM/QQBot/internal/directory/domain"
	obcontracts "github.com/HappyLadySauceM/QQBot/internal/onebot/contracts"
	obdomain "github.com/HappyLadySauceM/QQBot/internal/onebot/domain"
)

// Repository persists and queries the local directory; implemented by infrastructure.
// Repository 持久化并查询本地目录；由 infrastructure 实现。
type Repository interface {
	UpsertBotAccount(ctx context.Context, botID int64, nickname string) error
	UpsertContacts(ctx context.Context, botID int64, contacts []domain.Contact) error
	UpsertGroups(ctx context.Context, botID int64, groups []domain.Group) error
	UpsertGroupMembers(ctx context.Context, botID, groupID int64, members []domain.GroupMember) error
	ListContacts(ctx context.Context, q domain.PageQuery) (domain.ContactPage, error)
	ListGroups(ctx context.Context, q domain.PageQuery) (domain.GroupPage, error)
	ListGroupMembers(ctx context.Context, groupID int64, q domain.PageQuery) (domain.MemberPage, error)
}

// Service provides directory sync and read-only queries over the last synced account.
// Service 提供目录同步，以及针对最近同步账号的只读查询。
type Service struct {
	repo   Repository
	info   obcontracts.InfoReader
	logger *slog.Logger

	mu        sync.RWMutex
	lastBotID int64
}

// NewService validates dependencies and builds the service.
// NewService 校验依赖并构建服务。
func NewService(repo Repository, info obcontracts.InfoReader, logger *slog.Logger) (*Service, error) {
	if repo == nil || info == nil || logger == nil {
		return nil, fmt.Errorf("directory: dependencies required")
	}
	return &Service{repo: repo, info: info, logger: logger}, nil
}

// Sync refreshes the bot account, friends and groups from the connected OneBot
// implementation using upserts; it never hard-deletes stale rows.
// Sync 通过 upsert 从已连接的 OneBot 实现刷新机器人账号、好友与群；
// 不会硬删除过期行。
func (s *Service) Sync(ctx context.Context) (domain.SyncResult, error) {
	login, err := s.info.GetLoginInfo(ctx)
	if err != nil {
		return domain.SyncResult{}, fmt.Errorf("directory: get login info: %w", err)
	}
	friends, err := s.info.GetFriendList(ctx)
	if err != nil {
		return domain.SyncResult{}, fmt.Errorf("directory: get friend list: %w", err)
	}
	groups, err := s.info.GetGroupList(ctx, false)
	if err != nil {
		return domain.SyncResult{}, fmt.Errorf("directory: get group list: %w", err)
	}

	contacts := make([]domain.Contact, 0, len(friends))
	for _, f := range friends {
		contacts = append(contacts, domain.Contact{
			UserID:   int64(f.UserID),
			Nickname: f.Nickname,
			Remark:   f.Remark,
		})
	}
	groupList := make([]domain.Group, 0, len(groups))
	for _, g := range groups {
		groupList = append(groupList, domain.Group{
			GroupID:     int64(g.GroupID),
			Name:        g.GroupName,
			MemberCount: g.MemberCount,
		})
	}

	botID := int64(login.UserID)
	if err := s.repo.UpsertBotAccount(ctx, botID, login.Nickname); err != nil {
		return domain.SyncResult{}, fmt.Errorf("directory: upsert bot account: %w", err)
	}
	if err := s.repo.UpsertContacts(ctx, botID, contacts); err != nil {
		return domain.SyncResult{}, fmt.Errorf("directory: upsert contacts: %w", err)
	}
	if err := s.repo.UpsertGroups(ctx, botID, groupList); err != nil {
		return domain.SyncResult{}, fmt.Errorf("directory: upsert groups: %w", err)
	}
	s.mu.Lock()
	s.lastBotID = botID
	s.mu.Unlock()
	s.logger.Info("directory synced", "bot_id", botID, "contacts", len(contacts), "groups", len(groupList))
	return domain.SyncResult{BotID: botID, Contacts: len(contacts), Groups: len(groupList)}, nil
}

// SyncGroupMembers refreshes members of one group on demand; the bot account must
// have been synced first so the rows can reference it.
// SyncGroupMembers 按需刷新一个群的成员；必须先同步过机器人账号，
// 成员行才能关联到该账号。
func (s *Service) SyncGroupMembers(ctx context.Context, groupID int64) (domain.SyncResult, error) {
	if groupID <= 0 {
		return domain.SyncResult{}, fmt.Errorf("directory: group id must be positive")
	}
	s.mu.RLock()
	botID := s.lastBotID
	s.mu.RUnlock()
	if botID == 0 {
		return domain.SyncResult{}, fmt.Errorf("directory: bot account not synced yet")
	}
	members, err := s.info.GetGroupMemberList(ctx, obdomain.GroupID(groupID))
	if err != nil {
		return domain.SyncResult{}, fmt.Errorf("directory: get group member list: %w", err)
	}
	list := make([]domain.GroupMember, 0, len(members))
	for _, m := range members {
		list = append(list, domain.GroupMember{
			GroupID:  groupID,
			UserID:   int64(m.UserID),
			Nickname: m.Nickname,
			Card:     m.Card,
			Role:     string(m.Role),
		})
	}
	if err := s.repo.UpsertGroupMembers(ctx, botID, groupID, list); err != nil {
		return domain.SyncResult{}, fmt.Errorf("directory: upsert group members: %w", err)
	}
	s.logger.Info("group members synced", "bot_id", botID, "group_id", groupID, "members", len(list))
	return domain.SyncResult{BotID: botID, Members: len(list)}, nil
}

// Contacts returns one page of the friend directory.
// Contacts 返回一页好友目录。
func (s *Service) Contacts(ctx context.Context, q domain.PageQuery) (domain.ContactPage, error) {
	return s.repo.ListContacts(ctx, q.Normalized())
}

// Groups returns one page of the group directory.
// Groups 返回一页群目录。
func (s *Service) Groups(ctx context.Context, q domain.PageQuery) (domain.GroupPage, error) {
	return s.repo.ListGroups(ctx, q.Normalized())
}

// GroupMembers returns one page of group members, optionally refreshing first.
// GroupMembers 返回一页群成员，可先触发按需刷新。
func (s *Service) GroupMembers(ctx context.Context, groupID int64, q domain.PageQuery, refresh bool) (domain.MemberPage, error) {
	if refresh {
		if _, err := s.SyncGroupMembers(ctx, groupID); err != nil {
			return domain.MemberPage{}, err
		}
	}
	return s.repo.ListGroupMembers(ctx, groupID, q.Normalized())
}
