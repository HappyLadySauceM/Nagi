// Package domain defines scope types, reply modes and the effective policy model.
// scope/domain 定义作用域类型、回复模式与生效策略模型。
package domain

import (
	"fmt"
	"regexp"
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

	// ToolPermissions holds explicit overrides of tool permission grants for
	// this scope. A missing key means "use the configured default", so changing
	// the default baseline never rewrites stored rows.
	// ToolPermissions 保存该作用域的工具权限显式覆盖。缺少键表示"使用配置默认"，
	// 因此调整默认基线不会重写已存行。
	ToolPermissions map[string]bool
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
	if err := validateToolPermissions(p.ToolPermissions); err != nil {
		return err
	}
	return nil
}

// maxToolPermissions bounds one policy override map; the set of permissions is
// small, so a larger map only signals bad input.
// maxToolPermissions 限制单个策略的覆盖项数量；权限集合本身很小，
// 超出上限只说明输入异常。
const maxToolPermissions = 32

// maxPermissionKeyLen bounds one permission identifier.
// maxPermissionKeyLen 限制单个权限标识的长度。
const maxPermissionKeyLen = 64

// permissionKeyPattern accepts namespaced permission identifiers such as
// qq.group.read. Stored keys are matched against registered tools; this only
// rejects malformed input so the domain stays free of plugin dependencies.
// permissionKeyPattern 接受 qq.group.read 这类带命名空间的权限标识。
// 已存键会与注册工具比对；这里只拒绝畸形输入，使领域层不依赖插件包。
var permissionKeyPattern = regexp.MustCompile(`^[a-z][a-z0-9_]*(\.[a-z][a-z0-9_]*)+$`)

// validateToolPermissions checks the override key shape and count.
// validateToolPermissions 校验覆盖键的形状与数量。
func validateToolPermissions(overrides map[string]bool) error {
	if len(overrides) > maxToolPermissions {
		return fmt.Errorf("scope: too many tool permission overrides (%d)", len(overrides))
	}
	for key := range overrides {
		if len(key) > maxPermissionKeyLen || !permissionKeyPattern.MatchString(key) {
			return fmt.Errorf("scope: invalid tool permission %q", key)
		}
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
