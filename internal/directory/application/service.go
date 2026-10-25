// Package application implements directory sync and query use cases.
// directory/application 实现目录同步与查询用例。
package application

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/HappyLadySauceM/QQBot/internal/directory/domain"
	obcontracts "github.com/HappyLadySauceM/QQBot/internal/onebot/contracts"
	obdomain "github.com/HappyLadySauceM/QQBot/internal/onebot/domain"
)

// ErrBotNotSynced reports that the bot account row is missing, so member rows
// cannot reference it. Callers should ask the operator to sync the directory.
// ErrBotNotSynced 表示机器人账号行不存在，成员行无法关联；调用方应提示先同步目录。
var ErrBotNotSynced = errors.New("directory: bot account not synced yet")

// ErrAccountOffline reports that no connected endpoint serves the account.
// ErrAccountOffline 表示没有已连接的端点服务于该账号。
var ErrAccountOffline = errors.New("directory: bot account is not connected")

// Repository persists and queries the local directory; implemented by infrastructure.
// Repository 持久化并查询本地目录；由 infrastructure 实现。
type Repository interface {
	UpsertBotAccount(ctx context.Context, botID int64, nickname string) error
	UpsertContacts(ctx context.Context, botID int64, contacts []domain.Contact) error
	UpsertGroups(ctx context.Context, botID int64, groups []domain.Group) error
	UpsertGroupMembers(ctx context.Context, botID, groupID int64, members []domain.GroupMember) error
	// BotAccountExists reports whether the account row exists; member rows need it.
	// BotAccountExists 报告账号行是否存在；成员行依赖该行。
	BotAccountExists(ctx context.Context, botID int64) (bool, error)
	ListContacts(ctx context.Context, botID int64, q domain.PageQuery) (domain.ContactPage, error)
	ListGroups(ctx context.Context, botID int64, q domain.PageQuery) (domain.GroupPage, error)
	ListGroupMembers(ctx context.Context, botID, groupID int64, q domain.PageQuery) (domain.MemberPage, error)
	// ListBotAccounts returns stored account rows for nickname lookup.
	// ListBotAccounts 返回已存储的账号行，用于查询昵称。
	ListBotAccounts(ctx context.Context) ([]domain.BotAccount, error)
	// DeleteGroupMember removes one member row after a leave notification.
	// DeleteGroupMember 在成员退群通知后删除一行成员记录。
	DeleteGroupMember(ctx context.Context, botID, groupID, userID int64) error
	// DeleteGroupMembers clears every member row of one group.
	// DeleteGroupMembers 清空一个群的全部成员记录。
	DeleteGroupMembers(ctx context.Context, botID, groupID int64) error
	// DeleteGroup removes one group row, used when the bot itself left.
	// DeleteGroup 删除群行，用于机器人自己被移出群的情况。
	DeleteGroup(ctx context.Context, botID, groupID int64) error
}

// InfoSource resolves the OneBot reader of one account.
// InfoSource 按账号解析对应的 OneBot 查询能力。
type InfoSource interface {
	InfoReader(botID int64) (obcontracts.InfoReader, bool)
}

// Service provides directory sync and read-only queries per bot account.
// Service 提供按机器人账号隔离的目录同步与只读查询。
type Service struct {
	repo   Repository
	info   InfoSource
	logger *slog.Logger
}

// NewService validates dependencies and builds the service.
// NewService 校验依赖并构建服务。
func NewService(repo Repository, info InfoSource, logger *slog.Logger) (*Service, error) {
	if repo == nil || info == nil || logger == nil {
		return nil, fmt.Errorf("directory: dependencies required")
	}
	return &Service{repo: repo, info: info, logger: logger}, nil
}

// Sync refreshes the bot account, friends and groups of one account using
// upserts; it never hard-deletes stale rows.
// Sync 通过 upsert 刷新指定账号的机器人账号、好友与群；不会硬删除过期行。
func (s *Service) Sync(ctx context.Context, botID int64) (domain.SyncResult, error) {
	reader, err := s.reader(botID)
	if err != nil {
		return domain.SyncResult{}, err
	}
	login, err := reader.GetLoginInfo(ctx)
	if err != nil {
		return domain.SyncResult{}, fmt.Errorf("directory: get login info: %w", err)
	}
	// An endpoint must never write rows under an account it does not serve.
	// 端点不得为不属于自己的账号写入数据。
	if got := int64(login.UserID); got != botID {
		return domain.SyncResult{}, fmt.Errorf("directory: endpoint reports bot %d, expected %d", got, botID)
	}
	friends, err := reader.GetFriendList(ctx)
	if err != nil {
		return domain.SyncResult{}, fmt.Errorf("directory: get friend list: %w", err)
	}
	groups, err := reader.GetGroupList(ctx, false)
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

	if err := s.repo.UpsertBotAccount(ctx, botID, login.Nickname); err != nil {
		return domain.SyncResult{}, fmt.Errorf("directory: upsert bot account: %w", err)
	}
	if err := s.repo.UpsertContacts(ctx, botID, contacts); err != nil {
		return domain.SyncResult{}, fmt.Errorf("directory: upsert contacts: %w", err)
	}
	if err := s.repo.UpsertGroups(ctx, botID, groupList); err != nil {
		return domain.SyncResult{}, fmt.Errorf("directory: upsert groups: %w", err)
	}
	s.logger.Info("directory synced", "bot_id", botID, "contacts", len(contacts), "groups", len(groupList))
	return domain.SyncResult{BotID: botID, Contacts: len(contacts), Groups: len(groupList)}, nil
}

// SyncGroupMembers refreshes members of one group on demand; the bot account
// must have been synced first so the rows can reference it.
// SyncGroupMembers 按需刷新一个群的成员；必须先同步过机器人账号，成员行才能关联。
func (s *Service) SyncGroupMembers(ctx context.Context, botID, groupID int64) (domain.SyncResult, error) {
	if groupID <= 0 {
		return domain.SyncResult{}, fmt.Errorf("directory: group id must be positive")
	}
	reader, err := s.reader(botID)
	if err != nil {
		return domain.SyncResult{}, err
	}
	synced, err := s.repo.BotAccountExists(ctx, botID)
	if err != nil {
		return domain.SyncResult{}, fmt.Errorf("directory: check bot account: %w", err)
	}
	if !synced {
		return domain.SyncResult{}, ErrBotNotSynced
	}
	members, err := reader.GetGroupMemberList(ctx, obdomain.GroupID(groupID))
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

// reader resolves the OneBot reader of the account and fails when it is offline.
// reader 解析账号的 OneBot 查询能力，账号离线时失败。
func (s *Service) reader(botID int64) (obcontracts.InfoReader, error) {
	if botID <= 0 {
		return nil, ErrAccountOffline
	}
	reader, ok := s.info.InfoReader(botID)
	if !ok {
		return nil, ErrAccountOffline
	}
	return reader, nil
}

// Contacts returns one page of the account's friend directory.
// Contacts 返回该账号的一页好友目录。
func (s *Service) Contacts(ctx context.Context, botID int64, q domain.PageQuery) (domain.ContactPage, error) {
	return s.repo.ListContacts(ctx, botID, q.Normalized())
}

// Groups returns one page of the account's group directory.
// Groups 返回该账号的一页群目录。
func (s *Service) Groups(ctx context.Context, botID int64, q domain.PageQuery) (domain.GroupPage, error) {
	return s.repo.ListGroups(ctx, botID, q.Normalized())
}

// GroupMembers returns one page of group members, optionally refreshing first.
// GroupMembers 返回一页群成员，可先触发按需刷新。
func (s *Service) GroupMembers(ctx context.Context, botID, groupID int64, q domain.PageQuery, refresh bool) (domain.MemberPage, error) {
	if refresh {
		if _, err := s.SyncGroupMembers(ctx, botID, groupID); err != nil {
			return domain.MemberPage{}, err
		}
	}
	return s.repo.ListGroupMembers(ctx, botID, groupID, q.Normalized())
}

// ApplyMemberJoin records one member that joined a group, then refreshes the
// group member count. A failed profile lookup leaves the directory untouched.
// ApplyMemberJoin 记录单个入群成员并刷新群成员数；资料查询失败时不改动目录。
func (s *Service) ApplyMemberJoin(ctx context.Context, botID, groupID, userID int64) error {
	if groupID <= 0 || userID <= 0 {
		return fmt.Errorf("directory: group id and user id must be positive")
	}
	reader, err := s.reader(botID)
	if err != nil {
		return err
	}
	synced, err := s.repo.BotAccountExists(ctx, botID)
	if err != nil {
		return fmt.Errorf("directory: check bot account: %w", err)
	}
	if !synced {
		return ErrBotNotSynced
	}
	info, err := reader.GetGroupMemberInfo(ctx, obdomain.GroupID(groupID), obdomain.UserID(userID), false)
	if err != nil {
		return fmt.Errorf("directory: get group member info: %w", err)
	}
	member := domain.GroupMember{
		GroupID:  groupID,
		UserID:   int64(info.UserID),
		Nickname: info.Nickname,
		Card:     info.Card,
		Role:     string(info.Role),
	}
	if err := s.repo.UpsertGroupMembers(ctx, botID, groupID, []domain.GroupMember{member}); err != nil {
		return fmt.Errorf("directory: upsert joined member: %w", err)
	}
	s.refreshGroupMeta(ctx, botID, groupID, reader)
	s.logger.Info("group member joined", "bot_id", botID, "group_id", groupID, "user_id", member.UserID)
	return nil
}

// ApplyMemberLeave removes one member row after a leave notification.
// ApplyMemberLeave 在退群通知后删除对应成员行并刷新群成员数。
func (s *Service) ApplyMemberLeave(ctx context.Context, botID, groupID, userID int64) error {
	if groupID <= 0 || userID <= 0 {
		return fmt.Errorf("directory: group id and user id must be positive")
	}
	if err := s.repo.DeleteGroupMember(ctx, botID, groupID, userID); err != nil {
		return fmt.Errorf("directory: delete left member: %w", err)
	}
	if reader, err := s.reader(botID); err == nil {
		s.refreshGroupMeta(ctx, botID, groupID, reader)
	}
	s.logger.Info("group member left", "bot_id", botID, "group_id", groupID, "user_id", userID)
	return nil
}

// ApplyBotJoined syncs the full member list after the bot itself was added to a
// group: the stored rows are stale or missing at that point.
// ApplyBotJoined 在机器人自己被拉进群后全量同步成员：此时存量行已过期或缺失。
func (s *Service) ApplyBotJoined(ctx context.Context, botID, groupID int64) error {
	if _, err := s.SyncGroupMembers(ctx, botID, groupID); err != nil {
		return err
	}
	if reader, err := s.reader(botID); err == nil {
		s.refreshGroupMeta(ctx, botID, groupID, reader)
	}
	return nil
}

// ApplyBotLeft clears the group directory after the bot itself was removed.
// ApplyBotLeft 在机器人自己被移出群后清空该群的目录数据。
func (s *Service) ApplyBotLeft(ctx context.Context, botID, groupID int64) error {
	if botID <= 0 || groupID <= 0 {
		return fmt.Errorf("directory: bot id and group id must be positive")
	}
	if err := s.repo.DeleteGroupMembers(ctx, botID, groupID); err != nil {
		return fmt.Errorf("directory: clear group members: %w", err)
	}
	if err := s.repo.DeleteGroup(ctx, botID, groupID); err != nil {
		return fmt.Errorf("directory: delete group: %w", err)
	}
	s.logger.Info("bot left group, directory cleared", "bot_id", botID, "group_id", groupID)
	return nil
}

// refreshGroupMeta updates the cached member count; failures are logged only
// because a stale count must never fail a membership event.
// refreshGroupMeta 刷新群成员数；计数过期不应让成员事件失败，因此只记日志。
func (s *Service) refreshGroupMeta(ctx context.Context, botID, groupID int64, reader obcontracts.InfoReader) {
	info, err := reader.GetGroupInfo(ctx, obdomain.GroupID(groupID), false)
	if err != nil {
		s.logger.Warn("refresh group meta failed", "bot_id", botID, "group_id", groupID, "error", err)
		return
	}
	if err := s.repo.UpsertGroups(ctx, botID, []domain.Group{{
		GroupID:     int64(info.GroupID),
		Name:        info.GroupName,
		MemberCount: info.MemberCount,
	}}); err != nil {
		s.logger.Warn("upsert group meta failed", "bot_id", botID, "group_id", groupID, "error", err)
	}
}

// BotAccounts returns stored account rows for nickname lookup.
// BotAccounts 返回已存储的账号行，用于查询昵称。
func (s *Service) BotAccounts(ctx context.Context) ([]domain.BotAccount, error) {
	return s.repo.ListBotAccounts(ctx)
}
