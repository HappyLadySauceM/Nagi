// Package infrastructure implements the directory repository on PostgreSQL.
// directory/infrastructure 在 PostgreSQL 上实现目录仓库。
package infrastructure

import (
	"context"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/HappyLadySauceM/QQBot/internal/directory/domain"
)

// Repo is the pgx-backed directory repository.
// Repo 是基于 pgx 的目录仓库。
type Repo struct {
	pool *pgxpool.Pool
}

// NewRepo builds the repository; the pool must not be nil.
// NewRepo 构建仓库；pool 不能为 nil。
func NewRepo(pool *pgxpool.Pool) (*Repo, error) {
	if pool == nil {
		return nil, fmt.Errorf("directory: pool required")
	}
	return &Repo{pool: pool}, nil
}

// UpsertBotAccount stores or refreshes the bot account row.
// UpsertBotAccount 存储或刷新机器人账号行。
func (r *Repo) UpsertBotAccount(ctx context.Context, botID int64, nickname string) error {
	_, err := r.pool.Exec(ctx, `
		INSERT INTO bot_accounts (bot_id, nickname, last_seen_at)
		VALUES ($1, $2, now())
		ON CONFLICT (bot_id) DO UPDATE SET nickname = EXCLUDED.nickname, last_seen_at = now()
	`, botID, nickname)
	return err
}

// BotAccountExists reports whether the account row exists.
// BotAccountExists 报告账号行是否存在。
func (r *Repo) BotAccountExists(ctx context.Context, botID int64) (bool, error) {
	var exists bool
	err := r.pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM bot_accounts WHERE bot_id = $1)`, botID).Scan(&exists)
	if err != nil {
		return false, fmt.Errorf("directory: check bot account: %w", err)
	}
	return exists, nil
}

// ListBotAccounts returns all stored account rows ordered by recency.
// ListBotAccounts 按活跃时间返回全部已存储账号行。
func (r *Repo) ListBotAccounts(ctx context.Context) ([]domain.BotAccount, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT bot_id, nickname, COALESCE(last_seen_at, to_timestamp(0))
		FROM bot_accounts
		ORDER BY last_seen_at DESC NULLS LAST, bot_id
	`)
	if err != nil {
		return nil, fmt.Errorf("directory: list bot accounts: %w", err)
	}
	defer rows.Close()
	accounts := []domain.BotAccount{}
	for rows.Next() {
		var a domain.BotAccount
		if err := rows.Scan(&a.BotID, &a.Nickname, &a.LastSeenAt); err != nil {
			return nil, fmt.Errorf("directory: scan bot account: %w", err)
		}
		accounts = append(accounts, a)
	}
	return accounts, rows.Err()
}

// UpsertContacts batch-upserts friend entries with last_seen refresh.
// UpsertContacts 批量 upsert 好友条目并刷新 last_seen。
func (r *Repo) UpsertContacts(ctx context.Context, botID int64, contacts []domain.Contact) error {
	batch := &pgx.Batch{}
	for _, c := range contacts {
		batch.Queue(`
			INSERT INTO contacts (bot_id, user_id, nickname, remark, last_seen_at)
			VALUES ($1, $2, $3, $4, now())
			ON CONFLICT (bot_id, user_id) DO UPDATE SET nickname = EXCLUDED.nickname, remark = EXCLUDED.remark, last_seen_at = now()
		`, botID, c.UserID, c.Nickname, c.Remark)
	}
	return r.pool.SendBatch(ctx, batch).Close()
}

// UpsertGroups batch-upserts group entries with last_seen refresh.
// UpsertGroups 批量 upsert 群条目并刷新 last_seen。
func (r *Repo) UpsertGroups(ctx context.Context, botID int64, groups []domain.Group) error {
	batch := &pgx.Batch{}
	for _, g := range groups {
		batch.Queue(`
			INSERT INTO qq_groups (bot_id, group_id, name, member_count, last_seen_at)
			VALUES ($1, $2, $3, $4, now())
			ON CONFLICT (bot_id, group_id) DO UPDATE SET name = EXCLUDED.name, member_count = EXCLUDED.member_count, last_seen_at = now()
		`, botID, g.GroupID, g.Name, g.MemberCount)
	}
	return r.pool.SendBatch(ctx, batch).Close()
}

// UpsertGroupMembers batch-upserts member entries of one group.
// UpsertGroupMembers 批量 upsert 某个群的成员条目。
func (r *Repo) UpsertGroupMembers(ctx context.Context, botID, groupID int64, members []domain.GroupMember) error {
	batch := &pgx.Batch{}
	for _, m := range members {
		batch.Queue(`
			INSERT INTO group_members (bot_id, group_id, user_id, nickname, card, role, last_seen_at)
			VALUES ($1, $2, $3, $4, $5, $6, now())
			ON CONFLICT (bot_id, group_id, user_id) DO UPDATE SET nickname = EXCLUDED.nickname, card = EXCLUDED.card, role = EXCLUDED.role, last_seen_at = now()
		`, botID, groupID, m.UserID, m.Nickname, m.Card, m.Role)
	}
	return r.pool.SendBatch(ctx, batch).Close()
}

// ListContacts returns one page of the account's contacts matching the search text.
// ListContacts 返回该账号匹配搜索文本的一页联系人。
func (r *Repo) ListContacts(ctx context.Context, botID int64, q domain.PageQuery) (domain.ContactPage, error) {
	var total int64
	if err := r.pool.QueryRow(ctx, `
		SELECT count(*) FROM contacts
		WHERE bot_id = $1
		  AND ($2 = ''
		       OR nickname ILIKE '%' || $2 || '%'
		       OR remark ILIKE '%' || $2 || '%'
		       OR CAST(user_id AS TEXT) LIKE '%' || $2 || '%')
	`, botID, q.Search).Scan(&total); err != nil {
		return domain.ContactPage{}, fmt.Errorf("directory: count contacts: %w", err)
	}
	rows, err := r.pool.Query(ctx, `
		SELECT user_id, nickname, remark, COALESCE(last_seen_at, to_timestamp(0))
		FROM contacts
		WHERE bot_id = $1
		  AND ($2 = ''
		       OR nickname ILIKE '%' || $2 || '%'
		       OR remark ILIKE '%' || $2 || '%'
		       OR CAST(user_id AS TEXT) LIKE '%' || $2 || '%')
		ORDER BY nickname, user_id
		LIMIT $3 OFFSET $4
	`, botID, q.Search, q.Limit, q.Offset())
	if err != nil {
		return domain.ContactPage{}, fmt.Errorf("directory: list contacts: %w", err)
	}
	defer rows.Close()
	page := domain.ContactPage{Total: total, Page: q.Page, Limit: q.Limit, Items: []domain.Contact{}}
	for rows.Next() {
		var c domain.Contact
		if err := rows.Scan(&c.UserID, &c.Nickname, &c.Remark, &c.LastSeenAt); err != nil {
			return domain.ContactPage{}, fmt.Errorf("directory: scan contact: %w", err)
		}
		page.Items = append(page.Items, c)
	}
	return page, rows.Err()
}

// ListGroups returns one page of the account's groups matching the search text.
// ListGroups 返回该账号匹配搜索文本的一页群。
func (r *Repo) ListGroups(ctx context.Context, botID int64, q domain.PageQuery) (domain.GroupPage, error) {
	var total int64
	if err := r.pool.QueryRow(ctx, `
		SELECT count(*) FROM qq_groups
		WHERE bot_id = $1
		  AND ($2 = ''
		       OR name ILIKE '%' || $2 || '%'
		       OR CAST(group_id AS TEXT) LIKE '%' || $2 || '%')
	`, botID, q.Search).Scan(&total); err != nil {
		return domain.GroupPage{}, fmt.Errorf("directory: count groups: %w", err)
	}
	rows, err := r.pool.Query(ctx, `
		SELECT group_id, name, member_count, COALESCE(last_seen_at, to_timestamp(0))
		FROM qq_groups
		WHERE bot_id = $1
		  AND ($2 = ''
		       OR name ILIKE '%' || $2 || '%'
		       OR CAST(group_id AS TEXT) LIKE '%' || $2 || '%')
		ORDER BY name, group_id
		LIMIT $3 OFFSET $4
	`, botID, q.Search, q.Limit, q.Offset())
	if err != nil {
		return domain.GroupPage{}, fmt.Errorf("directory: list groups: %w", err)
	}
	defer rows.Close()
	page := domain.GroupPage{Total: total, Page: q.Page, Limit: q.Limit, Items: []domain.Group{}}
	for rows.Next() {
		var g domain.Group
		if err := rows.Scan(&g.GroupID, &g.Name, &g.MemberCount, &g.LastSeenAt); err != nil {
			return domain.GroupPage{}, fmt.Errorf("directory: scan group: %w", err)
		}
		page.Items = append(page.Items, g)
	}
	return page, rows.Err()
}

// ListGroupMembers returns one page of members of one group for one account.
// ListGroupMembers 返回某个账号下某个群的一页成员。
func (r *Repo) ListGroupMembers(ctx context.Context, botID, groupID int64, q domain.PageQuery) (domain.MemberPage, error) {
	var total int64
	if err := r.pool.QueryRow(ctx, `
		SELECT count(*) FROM group_members
		WHERE bot_id = $1 AND group_id = $2
		  AND ($3 = ''
		       OR nickname ILIKE '%' || $3 || '%'
		       OR card ILIKE '%' || $3 || '%'
		       OR CAST(user_id AS TEXT) LIKE '%' || $3 || '%')
	`, botID, groupID, strings.TrimSpace(q.Search)).Scan(&total); err != nil {
		return domain.MemberPage{}, fmt.Errorf("directory: count group members: %w", err)
	}
	rows, err := r.pool.Query(ctx, `
		SELECT group_id, user_id, nickname, card, role, COALESCE(last_seen_at, to_timestamp(0))
		FROM group_members
		WHERE bot_id = $1 AND group_id = $2
		  AND ($3 = ''
		       OR nickname ILIKE '%' || $3 || '%'
		       OR card ILIKE '%' || $3 || '%'
		       OR CAST(user_id AS TEXT) LIKE '%' || $3 || '%')
		ORDER BY role DESC, card, user_id
		LIMIT $4 OFFSET $5
	`, botID, groupID, strings.TrimSpace(q.Search), q.Limit, q.Offset())
	if err != nil {
		return domain.MemberPage{}, fmt.Errorf("directory: list group members: %w", err)
	}
	defer rows.Close()
	page := domain.MemberPage{Total: total, Page: q.Page, Limit: q.Limit, Items: []domain.GroupMember{}}
	for rows.Next() {
		var m domain.GroupMember
		if err := rows.Scan(&m.GroupID, &m.UserID, &m.Nickname, &m.Card, &m.Role, &m.LastSeenAt); err != nil {
			return domain.MemberPage{}, fmt.Errorf("directory: scan group member: %w", err)
		}
		page.Items = append(page.Items, m)
	}
	return page, rows.Err()
}

// DeleteGroupMember removes one member row; re-running it is harmless.
// DeleteGroupMember 删除单条成员行；重复执行无副作用。
func (r *Repo) DeleteGroupMember(ctx context.Context, botID, groupID, userID int64) error {
	if _, err := r.pool.Exec(ctx,
		`DELETE FROM group_members WHERE bot_id = $1 AND group_id = $2 AND user_id = $3`,
		botID, groupID, userID,
	); err != nil {
		return fmt.Errorf("directory: delete group member: %w", err)
	}
	return nil
}

// DeleteGroupMembers clears every member row of one group.
// DeleteGroupMembers 清空一个群的全部成员行。
func (r *Repo) DeleteGroupMembers(ctx context.Context, botID, groupID int64) error {
	if _, err := r.pool.Exec(ctx,
		`DELETE FROM group_members WHERE bot_id = $1 AND group_id = $2`,
		botID, groupID,
	); err != nil {
		return fmt.Errorf("directory: delete group members: %w", err)
	}
	return nil
}

// DeleteGroup removes one group row.
// DeleteGroup 删除单条群行。
func (r *Repo) DeleteGroup(ctx context.Context, botID, groupID int64) error {
	if _, err := r.pool.Exec(ctx,
		`DELETE FROM qq_groups WHERE bot_id = $1 AND group_id = $2`,
		botID, groupID,
	); err != nil {
		return fmt.Errorf("directory: delete group: %w", err)
	}
	return nil
}
