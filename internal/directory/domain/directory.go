// Package domain defines pure directory value types for contacts, groups and members.
// directory/domain 定义联系人、群与成员的纯目录值类型。
package domain

import (
	"time"
)

// Contact is one friend entry of the local directory.
// Contact 是本地目录中的一名好友。
type Contact struct {
	UserID     int64
	Nickname   string
	Remark     string
	LastSeenAt time.Time
}

// Group is one group entry of the local directory.
// Group 是本地目录中的一个群。
type Group struct {
	GroupID     int64
	Name        string
	MemberCount int64
	LastSeenAt  time.Time
}

// GroupMember is one member entry of one group.
// GroupMember 是某个群内的一名成员。
type GroupMember struct {
	GroupID    int64
	UserID     int64
	Nickname   string
	Card       string
	Role       string
	LastSeenAt time.Time
}

// PageQuery is the shared paging and search input for directory listings.
// PageQuery 是目录列表共用的分页与搜索输入。
type PageQuery struct {
	Search string
	Page   int
	Limit  int
}

// Normalized clamps page and limit into safe bounds.
// Normalized 将 page 与 limit 收敛到安全范围。
func (q PageQuery) Normalized() PageQuery {
	if q.Page < 1 {
		q.Page = 1
	}
	if q.Limit < 1 {
		q.Limit = 50
	}
	if q.Limit > 200 {
		q.Limit = 200
	}
	return q
}

// Offset is the SQL offset for the normalized query.
// Offset 返回规范化查询的 SQL 偏移量。
func (q PageQuery) Offset() int { return (q.Page - 1) * q.Limit }

// ContactPage is one page of contacts plus the total count.
// ContactPage 是一页联系人及总数。
type ContactPage struct {
	Items []Contact
	Total int64
	Page  int
	Limit int
}

// GroupPage is one page of groups plus the total count.
// GroupPage 是一页群及总数。
type GroupPage struct {
	Items []Group
	Total int64
	Page  int
	Limit int
}

// MemberPage is one page of group members plus the total count.
// MemberPage 是一页群成员及总数。
type MemberPage struct {
	Items []GroupMember
	Total int64
	Page  int
	Limit int
}

// SyncResult reports how many entries one synchronization run wrote.
// SyncResult 报告一次同步写入的条目数量。
type SyncResult struct {
	BotID    int64
	Contacts int
	Groups   int
	Members  int
}
