// Package infrastructure implements the directory repository on PostgreSQL.
// directory/infrastructure 在 PostgreSQL 上实现目录仓库。
package infrastructure

import (
	"context"
	"fmt"
	"github.com/HappyLadySauceM/Nagi/pkg/db/ent"
	"github.com/HappyLadySauceM/Nagi/pkg/db/ent/botaccount"
	"github.com/HappyLadySauceM/Nagi/pkg/db/ent/contact"
	"github.com/HappyLadySauceM/Nagi/pkg/db/ent/groupmember"
	"github.com/HappyLadySauceM/Nagi/pkg/db/ent/qqgroup"
	"strings"

	entsql "entgo.io/ent/dialect/sql"
	"github.com/HappyLadySauceM/Nagi/pkg/db"
	"time"

	"github.com/HappyLadySauceM/Nagi/internal/directory/domain"
)

// Repo is the pgx-backed directory repository.
// Repo 是基于 pgx 的目录仓库。
type Repo struct {
	pool *db.Pool
}

// NewRepo builds the repository; the pool must not be nil.
// NewRepo 构建仓库；pool 不能为 nil。
func NewRepo(pool *db.Pool) (*Repo, error) {
	if pool == nil {
		return nil, fmt.Errorf("directory: pool required")
	}
	return &Repo{pool: pool}, nil
}

// UpsertBotAccount stores or refreshes the bot account row.
// UpsertBotAccount 存储或刷新机器人账号行。
func (r *Repo) UpsertBotAccount(ctx context.Context, botID int64, nickname string) error {
	return r.pool.Client.BotAccount.Create().SetID(botID).SetNickname(nickname).SetLastSeenAt(time.Now()).OnConflict(entsql.ConflictColumns("bot_id")).UpdateNewValues().Exec(ctx)
}

// BotAccountExists reports whether the account row exists.
// BotAccountExists 报告账号行是否存在。
func (r *Repo) BotAccountExists(ctx context.Context, botID int64) (bool, error) {
	return r.pool.Client.BotAccount.Query().Where(botaccount.ID(botID)).Exist(ctx)
}

// ListBotAccounts returns all stored account rows ordered by recency.
// ListBotAccounts 按活跃时间返回全部已存储账号行。
func (r *Repo) ListBotAccounts(ctx context.Context) ([]domain.BotAccount, error) {
	rows, err := r.pool.Client.BotAccount.Query().Order(botaccount.ByLastSeenAt(entsql.OrderDesc(), entsql.OrderNullsLast()), botaccount.ByID()).All(ctx)
	out := []domain.BotAccount{}
	for _, v := range rows {
		a := domain.BotAccount{BotID: v.ID, Nickname: v.Nickname, LastSeenAt: time.Unix(0, 0)}
		if v.LastSeenAt != nil {
			a.LastSeenAt = *v.LastSeenAt
		}
		out = append(out, a)
	}
	return out, err
}

// UpsertContacts batch-upserts friend entries with last_seen refresh.
// UpsertContacts 批量 upsert 好友条目并刷新 last_seen。
func (r *Repo) UpsertContacts(ctx context.Context, botID int64, contacts []domain.Contact) error {
	tx, err := r.pool.Client.Tx(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, v := range contacts {
		if err := tx.Client().Contact.Create().SetBotID(botID).SetUserID(v.UserID).SetNickname(v.Nickname).SetRemark(v.Remark).SetLastSeenAt(time.Now()).OnConflict(entsql.ConflictColumns("bot_id", "user_id")).UpdateNewValues().Exec(ctx); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// UpsertGroups batch-upserts group entries with last_seen refresh.
// UpsertGroups 批量 upsert 群条目并刷新 last_seen。
func (r *Repo) UpsertGroups(ctx context.Context, botID int64, groups []domain.Group) error {
	tx, err := r.pool.Client.Tx(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, v := range groups {
		if err := tx.Client().QQGroup.Create().SetBotID(botID).SetGroupID(v.GroupID).SetName(v.Name).SetMemberCount(int(v.MemberCount)).SetLastSeenAt(time.Now()).OnConflict(entsql.ConflictColumns("bot_id", "group_id")).UpdateNewValues().Exec(ctx); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// UpsertGroupMembers batch-upserts member entries of one group.
// UpsertGroupMembers 批量 upsert 某个群的成员条目。
func (r *Repo) UpsertGroupMembers(ctx context.Context, botID, groupID int64, members []domain.GroupMember) error {
	tx, err := r.pool.Client.Tx(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, v := range members {
		if err := tx.Client().GroupMember.Create().SetBotID(botID).SetGroupID(groupID).SetUserID(v.UserID).SetNickname(v.Nickname).SetCard(v.Card).SetRole(v.Role).SetLastSeenAt(time.Now()).OnConflict(entsql.ConflictColumns("bot_id", "group_id", "user_id")).UpdateNewValues().Exec(ctx); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// ListContacts returns one page of the account's contacts matching the search text.
// ListContacts 返回该账号匹配搜索文本的一页联系人。
func (r *Repo) ListContacts(ctx context.Context, botID int64, q domain.PageQuery) (domain.ContactPage, error) {
	query := r.pool.Client.Contact.Query().Where(contact.BotID(botID))
	search := q.Search
	if search != "" {
		query.Where(func(s *entsql.Selector) {
			s.Where(entsql.Or(entsql.ContainsFold(s.C("nickname"), search), entsql.ContainsFold(s.C("remark"), search), entsql.P(func(b *entsql.Builder) { b.WriteString("CAST(user_id AS TEXT) LIKE ").Arg("%" + search + "%") })))
		})
	}
	total, err := query.Clone().Count(ctx)
	if err != nil {
		return domain.ContactPage{}, err
	}
	rows, err := query.Order(ent.Asc(contact.FieldNickname, contact.FieldUserID)).Limit(q.Limit).Offset(q.Offset()).All(ctx)
	page := domain.ContactPage{Total: int64(total), Page: q.Page, Limit: q.Limit, Items: []domain.Contact{}}
	for _, v := range rows {
		seen := time.Unix(0, 0)
		if v.LastSeenAt != nil {
			seen = *v.LastSeenAt
		}
		page.Items = append(page.Items, domain.Contact{UserID: v.UserID, Nickname: v.Nickname, Remark: v.Remark, LastSeenAt: seen})
	}
	return page, err
}

// ListGroups returns one page of the account's groups matching the search text.
// ListGroups 返回该账号匹配搜索文本的一页群。
func (r *Repo) ListGroups(ctx context.Context, botID int64, q domain.PageQuery) (domain.GroupPage, error) {
	query := r.pool.Client.QQGroup.Query().Where(qqgroup.BotID(botID))
	search := q.Search
	if search != "" {
		query.Where(func(s *entsql.Selector) {
			s.Where(entsql.Or(entsql.ContainsFold(s.C("name"), search), entsql.P(func(b *entsql.Builder) { b.WriteString("CAST(group_id AS TEXT) LIKE ").Arg("%" + search + "%") })))
		})
	}
	total, err := query.Clone().Count(ctx)
	if err != nil {
		return domain.GroupPage{}, err
	}
	rows, err := query.Order(ent.Asc(qqgroup.FieldName, qqgroup.FieldGroupID)).Limit(q.Limit).Offset(q.Offset()).All(ctx)
	page := domain.GroupPage{Total: int64(total), Page: q.Page, Limit: q.Limit, Items: []domain.Group{}}
	for _, v := range rows {
		seen := time.Unix(0, 0)
		if v.LastSeenAt != nil {
			seen = *v.LastSeenAt
		}
		page.Items = append(page.Items, domain.Group{GroupID: v.GroupID, Name: v.Name, MemberCount: int64(v.MemberCount), LastSeenAt: seen})
	}
	return page, err
}

// ListGroupMembers returns one page of members of one group for one account.
// ListGroupMembers 返回某个账号下某个群的一页成员。
func (r *Repo) ListGroupMembers(ctx context.Context, botID, groupID int64, q domain.PageQuery) (domain.MemberPage, error) {
	query := r.pool.Client.GroupMember.Query().Where(groupmember.BotID(botID), groupmember.GroupID(groupID))
	search := strings.TrimSpace(q.Search)
	if search != "" {
		query.Where(func(s *entsql.Selector) {
			s.Where(entsql.Or(entsql.ContainsFold(s.C("nickname"), search), entsql.ContainsFold(s.C("card"), search), entsql.P(func(b *entsql.Builder) { b.WriteString("CAST(user_id AS TEXT) LIKE ").Arg("%" + search + "%") })))
		})
	}
	total, err := query.Clone().Count(ctx)
	if err != nil {
		return domain.MemberPage{}, err
	}
	rows, err := query.Order(ent.Desc(groupmember.FieldRole), ent.Asc(groupmember.FieldCard, groupmember.FieldUserID)).Limit(q.Limit).Offset(q.Offset()).All(ctx)
	page := domain.MemberPage{Total: int64(total), Page: q.Page, Limit: q.Limit, Items: []domain.GroupMember{}}
	for _, v := range rows {
		seen := time.Unix(0, 0)
		if v.LastSeenAt != nil {
			seen = *v.LastSeenAt
		}
		page.Items = append(page.Items, domain.GroupMember{GroupID: v.GroupID, UserID: v.UserID, Nickname: v.Nickname, Card: v.Card, Role: v.Role, LastSeenAt: seen})
	}
	return page, err
}

// DeleteGroupMember removes one member row; re-running it is harmless.
// DeleteGroupMember 删除单条成员行；重复执行无副作用。
func (r *Repo) DeleteGroupMember(ctx context.Context, botID, groupID, userID int64) error {
	_, err := r.pool.Client.GroupMember.Delete().Where(groupmember.BotID(botID), groupmember.GroupID(groupID), groupmember.UserID(userID)).Exec(ctx)
	return err
}

// DeleteGroupMembers clears every member row of one group.
// DeleteGroupMembers 清空一个群的全部成员行。
func (r *Repo) DeleteGroupMembers(ctx context.Context, botID, groupID int64) error {
	_, err := r.pool.Client.GroupMember.Delete().Where(groupmember.BotID(botID), groupmember.GroupID(groupID)).Exec(ctx)
	return err
}

// DeleteGroup removes one group row.
// DeleteGroup 删除单条群行。
func (r *Repo) DeleteGroup(ctx context.Context, botID, groupID int64) error {
	_, err := r.pool.Client.QQGroup.Delete().Where(qqgroup.BotID(botID), qqgroup.GroupID(groupID)).Exec(ctx)
	return err
}
