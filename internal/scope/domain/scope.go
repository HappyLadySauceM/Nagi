// Package domain defines scope types, reply modes and the effective policy model.
// scope/domain 定义作用域类型、回复模式与生效策略模型。
package domain

import (
	"fmt"
	"time"
)

// ScopeType is the kind of conversation a policy applies to.
// ScopeType 是策略作用的会话类型。
type ScopeType string

// Supported scope types.
// 支持的作用域类型。
const (
	ScopeGroup   ScopeType = "group"
	ScopePrivate ScopeType = "private"
)

// Valid reports whether the scope type is supported.
// Valid 报告作用域类型是否受支持。
func (t ScopeType) Valid() bool { return t == ScopeGroup || t == ScopePrivate }

// ReplyMode is the reply participation mode of one scope.
// ReplyMode 是单个作用域的回复参与模式。
type ReplyMode string

// Supported reply modes.
// 支持的回复模式。
const (
	ModeMentionOnly ReplyMode = "mention_only"
	ModeIntentBased ReplyMode = "intent_based"
	ModeAlwaysReply ReplyMode = "always_reply"
	ModeObserveOnly ReplyMode = "observe_only"
)

// Valid reports whether the reply mode is supported.
// Valid 报告回复模式是否受支持。
func (m ReplyMode) Valid() bool {
	switch m {
	case ModeMentionOnly, ModeIntentBased, ModeAlwaysReply, ModeObserveOnly:
		return true
	}
	return false
}

// Policy is one scope policy row; zero-value numeric fields are replaced by defaults.
// Policy 是一条作用域策略；数值字段的零值会由默认值替代。
type Policy struct {
	ScopeType ScopeType
	ScopeID   int64

	Enabled          bool
	ReplyMode        ReplyMode
	StoreMessages    bool
	AllowMemoryWrite bool
	AllowHistoryRead bool

	MinReplyIntervalMS int
	MaxRepliesPerHour  int
	RetentionDays      int
}

// Default returns the safe default policy for a scope: everything off.
// Default 返回一个作用域的安全默认策略：全部关闭。
func Default(t ScopeType, scopeID int64) Policy {
	return Policy{
		ScopeType:          t,
		ScopeID:            scopeID,
		ReplyMode:          ModeMentionOnly,
		MinReplyIntervalMS: 10000,
		MaxRepliesPerHour:  20,
		RetentionDays:      30,
	}
}

// WithDefaults fills zero numeric fields and the reply mode with defaults,
// so a stored partial row resolves to a complete effective policy.
// WithDefaults 用默认值补齐数值零值与回复模式，
// 使存储的部分行能解析为完整的生效策略。
func (p Policy) WithDefaults() Policy {
	if !p.ReplyMode.Valid() {
		p.ReplyMode = ModeMentionOnly
	}
	if p.MinReplyIntervalMS <= 0 {
		p.MinReplyIntervalMS = 10000
	}
	if p.MaxRepliesPerHour <= 0 {
		p.MaxRepliesPerHour = 20
	}
	if p.RetentionDays <= 0 {
		p.RetentionDays = 30
	}
	return p
}

// Validate checks scope type, reply mode and numeric bounds.
// Validate 校验作用域类型、回复模式与数值边界。
func (p Policy) Validate() error {
	if !p.ScopeType.Valid() {
		return fmt.Errorf("scope: invalid scope type %q", p.ScopeType)
	}
	if p.ScopeID <= 0 {
		return fmt.Errorf("scope: scope id must be positive")
	}
	if !p.ReplyMode.Valid() {
		return fmt.Errorf("scope: invalid reply mode %q", p.ReplyMode)
	}
	if p.MinReplyIntervalMS < 0 || p.MaxRepliesPerHour < 0 || p.RetentionDays < 0 {
		return fmt.Errorf("scope: policy numbers must not be negative")
	}
	return nil
}

// ExpiresAt derives the retention deadline from the policy; zero means no expiry.
// ExpiresAt 按策略推导留存期限；零值表示不过期。
func (p Policy) ExpiresAt(from time.Time) time.Time {
	if p.RetentionDays <= 0 {
		return time.Time{}
	}
	return from.AddDate(0, 0, p.RetentionDays)
}
