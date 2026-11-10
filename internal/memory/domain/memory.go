// Package domain defines the long-term memory model with visibility levels.
// memory/domain 定义带可见性分层的长期记忆模型。
package domain

import (
	"fmt"
	"strings"
	"time"
	"unicode"
)

// Visibility controls which conversations may read a memory.
// Visibility 控制哪些会话可以读取一条记忆。
type Visibility string

// Supported visibility levels; bot_shared requires administrator approval.
// 支持的可见性级别；bot_shared 需要管理员批准。
const (
	VisibilityBotShared    Visibility = "bot_shared"
	VisibilityGroup        Visibility = "group"
	VisibilityPrivateUser  Visibility = "private_user"
	VisibilityConversation Visibility = "conversation"
)

// Valid reports whether the visibility is supported.
// Valid 报告可见性是否受支持。
func (v Visibility) Valid() bool {
	switch v {
	case VisibilityBotShared, VisibilityGroup, VisibilityPrivateUser, VisibilityConversation:
		return true
	}
	return false
}

// Status is the memory lifecycle state.
// Status 是记忆生命周期状态。
type Status string

// Supported statuses: candidates need approval before becoming active.
// 支持的状态：候选需审核后才激活。
const (
	StatusCandidate  Status = "candidate"
	StatusActive     Status = "active"
	StatusSuperseded Status = "superseded"
	StatusDeleted    Status = "deleted"
)

// Valid reports whether the status is supported.
// Valid 报告状态是否受支持。
func (s Status) Valid() bool {
	switch s {
	case StatusCandidate, StatusActive, StatusSuperseded, StatusDeleted:
		return true
	}
	return false
}

// Memory is one long-term memory entry.
// Memory 是一条长期记忆。
type Memory struct {
	ID              string
	BotID           int64
	Visibility      Visibility
	OwnerKey        string
	Content         string
	SourceMessageID int64
	Source          string
	Confidence      float64
	Status          Status
	ApprovedBy      string
	ExpiresAt       time.Time
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

// Validate checks visibility, owner and content requirements.
// Validate 校验可见性、归属与内容要求。
func (m Memory) Validate() error {
	if !m.Visibility.Valid() {
		return fmt.Errorf("memory: invalid visibility %q", m.Visibility)
	}
	if m.BotID <= 0 {
		return fmt.Errorf("memory: bot id must be positive")
	}
	// bot_shared memories must never carry a personal owner binding.
	// bot_shared 记忆不得携带个人归属。
	if m.Visibility == VisibilityBotShared && m.OwnerKey != "" {
		return fmt.Errorf("memory: bot_shared memory must not have an owner key")
	}
	if m.Visibility != VisibilityBotShared && m.OwnerKey == "" {
		return fmt.Errorf("memory: visibility %s requires an owner key", m.Visibility)
	}
	if strings.TrimSpace(m.Content) == "" {
		return fmt.Errorf("memory: content must not be empty")
	}
	if m.Confidence < 0 || m.Confidence > 1 {
		return fmt.Errorf("memory: confidence must be within [0,1]")
	}
	return nil
}

// VisibleTo reports whether the memory may be read by the given conversation.
// The caller must also verify the memory is active and unexpired.
// VisibleTo 报告该记忆是否可被指定会话读取；调用方还必须校验激活与未过期状态。
func (m Memory) VisibleTo(kind string, targetID int64, conversationID string) bool {
	switch m.Visibility {
	case VisibilityBotShared:
		return true
	case VisibilityGroup:
		return kind == "group" && m.OwnerKey == fmt.Sprint(targetID)
	case VisibilityPrivateUser:
		return kind == "private" && m.OwnerKey == fmt.Sprint(targetID)
	case VisibilityConversation:
		return conversationID != "" && m.OwnerKey == conversationID
	default:
		return false
	}
}

// SearchRequest is the visibility-filtered retrieval input; Kind is "group" or
// "private" and ConversationID optionally widens to one conversation's memories.
// SearchRequest 是经可见性过滤的检索输入；Kind 为 "group" 或 "private"，
// ConversationID 可选地扩展到单会话记忆。
type SearchRequest struct {
	BotID          int64
	Kind           string
	TargetID       int64
	ConversationID string
	Query          string
	Limit          int
}

// ListFilter is the management listing filter.
// ListFilter 是管理端列表过滤条件。
type ListFilter struct {
	BotID      *int64
	Visibility Visibility
	OwnerKey   string
	Status     Status
	Search     string
	Page       int
	Limit      int
}

// Normalized clamps the paging fields of the filter.
// Normalized 收敛过滤条件的分页字段。
func (f ListFilter) Normalized() ListFilter {
	if f.Page < 1 {
		f.Page = 1
	}
	if f.Limit < 1 {
		f.Limit = 50
	}
	if f.Limit > 200 {
		f.Limit = 200
	}
	return f
}

// Offset is the SQL offset for the normalized filter.
// Offset 返回规范化过滤条件的 SQL 偏移量。
func (f ListFilter) Offset() int { return (f.Page - 1) * f.Limit }

// KeywordLimit bounds the number of search tokens extracted from one query.
// KeywordLimit 限制单条查询提取的检索词数量。
const KeywordLimit = 8

// queryStopWords drops low-signal Chinese bigrams that would otherwise turn
// short chatter into an overly broad recall.
// queryStopWords 过滤低信息量的中文双字词，避免短句触发过宽的召回。
var queryStopWords = map[string]struct{}{
	"的是": {}, "了吗": {}, "什么": {}, "怎么": {}, "这个": {}, "那个": {},
	"我们": {}, "你们": {}, "他们": {}, "可以": {}, "一下": {}, "就是": {},
	"不是": {}, "没有": {}, "还是": {}, "然后": {}, "现在": {}, "时候": {},
	"觉得": {}, "那么": {}, "这么": {}, "如果": {}, "因为": {}, "所以": {},
	"但是": {}, "而且": {}, "已经": {}, "一个": {}, "一种": {}, "能否": {},
	"是否": {}, "好的": {}, "嗯嗯": {},
}

// QueryKeywords extracts bounded keyword tokens from a free-form chat query.
// Han runs become overlapping two-rune windows; ASCII/digit runs stay whole.
// The result is de-duplicated and capped by KeywordLimit.
// QueryKeywords 从自由文本查询中提取有界检索词：中文按双字滑窗，ASCII/数字整体保留；
// 结果去重并受 KeywordLimit 限制。
func QueryKeywords(query string) []string {
	query = strings.TrimSpace(query)
	if query == "" {
		return nil
	}
	out := make([]string, 0, KeywordLimit)
	seen := make(map[string]struct{}, KeywordLimit)
	add := func(token string) {
		if token == "" || len(out) >= KeywordLimit {
			return
		}
		if _, exists := seen[token]; exists {
			return
		}
		if _, stop := queryStopWords[token]; stop {
			return
		}
		seen[token] = struct{}{}
		out = append(out, token)
	}
	run := make([]rune, 0, 16)
	flush := func() {
		if len(run) == 0 {
			return
		}
		ascii := true
		for _, r := range run {
			if r > unicode.MaxASCII {
				ascii = false
				break
			}
		}
		if ascii {
			if len(run) >= 2 {
				add(strings.ToLower(string(run)))
			}
		} else {
			for i := 0; i+1 < len(run); i++ {
				add(string(run[i : i+2]))
			}
		}
		run = run[:0]
	}
	for _, r := range query {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			run = append(run, r)
			continue
		}
		flush()
	}
	flush()
	return out
}
