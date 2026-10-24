// Package infrastructure implements the directory repository on PostgreSQL.
// directory/infrastructure 在 PostgreSQL 上实现目录仓库。
package infrastructure

import (
	"context"
	"errors"
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
		INSERT INTO bot_accounts (bot_id, nickname, status, last_seen_at)
		VALUES ($1, $2, 'online', now())
		ON CONFLICT (bot_id) DO UPDATE SET nickname = EXCLUDED.nickname, status = 'online', last_seen_at = now()
	`, botID, nickname)
	return err
}

// LatestBotID returns the most recently seen bot account. It lets the service
// recover the account identity after a restart, when the in-memory value is gone.
// LatestBotID 返回最近活跃的机器人账号，用于进程重启后恢复账号身份。
func (r *Repo) LatestBotID(ctx context.Context) (int64, bool, error) {
	var botID int64
	err := r.pool.QueryRow(ctx, `
		SELECT bot_id FROM bot_accounts ORDER BY last_seen_at DESC NULLS LAST LIMIT 1
	`).Scan(&botID)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, fmt.Errorf("directory: latest bot account: %w", err)
	}
	return botID, true, nil
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

// ListContacts returns one page of contacts matching the search text.
// ListContacts 返回匹配搜索文本的一页联系人。
func (r *Repo) ListContacts(ctx context.Context, q domain.PageQuery) (domain.ContactPage, error) {
	var total int64
	if err := r.pool.QueryRow(ctx, `
		SELECT count(*) FROM contacts
		WHERE ($1 = '' OR nickname ILIKE '%' || $1 || '%' OR remark ILIKE '%' || $1 || '%')
	`, q.Search).Scan(&total); err != nil {
		return domain.ContactPage{}, fmt.Errorf("directory: count contacts: %w", err)
	}
	rows, err := r.pool.Query(ctx, `
		SELECT user_id, nickname, remark, COALESCE(last_seen_at, to_timestamp(0))
		FROM contacts
		WHERE ($1 = '' OR nickname ILIKE '%' || $1 || '%' OR remark ILIKE '%' || $1 || '%')
		ORDER BY nickname, user_id
		LIMIT $2 OFFSET $3
	`, q.Search, q.Limit, q.Offset())
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

// ListGroups returns one page of groups matching the search text.
// ListGroups 返回匹配搜索文本的一页群。
func (r *Repo) ListGroups(ctx context.Context, q domain.PageQuery) (domain.GroupPage, error) {
	var total int64
	if err := r.pool.QueryRow(ctx, `
		SELECT count(*) FROM qq_groups
		WHERE ($1 = '' OR name ILIKE '%' || $1 || '%')
	`, q.Search).Scan(&total); err != nil {
		return domain.GroupPage{}, fmt.Errorf("directory: count groups: %w", err)
	}
	rows, err := r.pool.Query(ctx, `
		SELECT group_id, name, member_count, COALESCE(last_seen_at, to_timestamp(0))
		FROM qq_groups
		WHERE ($1 = '' OR name ILIKE '%' || $1 || '%')
		ORDER BY name, group_id
		LIMIT $2 OFFSET $3
	`, q.Search, q.Limit, q.Offset())
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

// ListGroupMembers returns one page of members of one group.
// ListGroupMembers 返回某个群的一页成员。
func (r *Repo) ListGroupMembers(ctx context.Context, groupID int64, q domain.PageQuery) (domain.MemberPage, error) {
	var total int64
	if err := r.pool.QueryRow(ctx, `
		SELECT count(*) FROM group_members WHERE group_id = $1
	`, groupID).Scan(&total); err != nil {
		return domain.MemberPage{}, fmt.Errorf("directory: count group members: %w", err)
	}
	rows, err := r.pool.Query(ctx, `
		SELECT group_id, user_id, nickname, card, role, COALESCE(last_seen_at, to_timestamp(0))
		FROM group_members
		WHERE group_id = $1 AND ($2 = '' OR nickname ILIKE '%' || $2 || '%' OR card ILIKE '%' || $2 || '%')
		ORDER BY role DESC, card, user_id
		LIMIT $3 OFFSET $4
	`, groupID, strings.TrimSpace(q.Search), q.Limit, q.Offset())
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
