// Package domain defines scope types, reply modes and the effective policy model.
// scope/domain 定义作用域类型、回复模式与生效策略模型。
package domain

import (
	"errors"
	"fmt"
	"regexp"

	"github.com/HappyLadySauceM/Nagi/pkg/qq"
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

// ErrTemplateConflict reports a stale expected template version.
// ErrTemplateConflict 表示预期模板版本已过期。
var ErrTemplateConflict = errors.New("scope: template version conflict")

// Policy is one scope policy row. A zero sending limit means unlimited.
// Policy 是一条作用域策略；发送上限的零值表示不限。
type Policy struct {
	ExpectedTemplateVersion *int64
	ScopeType               ScopeType
	ScopeID                 int64
	Source                  string
	TemplateVersion         int64
	Version                 int64

	Enabled          bool
	ReplyMode        ReplyMode
	StoreMessages    bool
	AllowMemoryWrite bool
	AllowHistoryRead bool

	MinReplyIntervalMS int
	MaxRepliesPerHour  int

	// ToolPermissions holds explicit overrides of tool permission grants for
	// this scope. A missing key means "use the configured default", so changing
	// the default baseline never rewrites stored rows.
	// ToolPermissions 保存该作用域的工具权限显式覆盖。缺少键表示"使用配置默认"，
	// 因此调整默认基线不会重写已存行。
	ToolPermissions     map[string]bool
	MaxMessagesPerRun   int
	MaxCharsPerMessage  int
	MaxTotalCharsPerRun int
	MessageIntervalMS   int
}

// Default returns the default policy of a scope. Message storage is on by
// default, and every sending limit is unlimited except the single-message cap,
// which follows the QQ text limit.
// Default 返回作用域的默认策略。消息存储默认开启；除单条上限取 QQ 文本上限外，
// 其余发送上限默认不限制。
func Default(t ScopeType, scopeID int64) Policy {
	return Policy{
		ScopeType:          t,
		ScopeID:            scopeID,
		ReplyMode:          ModeMentionOnly,
		StoreMessages:      true,
		MinReplyIntervalMS: 10000,
		MaxRepliesPerHour:  0,
		MaxMessagesPerRun:  0, MaxCharsPerMessage: qq.MaxTextRunes, MaxTotalCharsPerRun: 0, MessageIntervalMS: 700,
	}
}

// WithDefaults fills legacy policies that carry no reply mode. Zero is a real
// value now (unlimited), so sending limits are never overwritten here.
// WithDefaults 仅补齐缺少回复模式的旧策略。零值现在是有效取值（表示不限），
// 因此不会在此覆盖发送上限。
func (p Policy) WithDefaults() Policy {
	if !p.ReplyMode.Valid() {
		p.ReplyMode = ModeMentionOnly
	}
	return p
}

// Validate checks scope type, reply mode and numeric bounds.
// Validate 校验作用域类型、回复模式与数值边界。
func (p Policy) Validate() error {
	if !p.ScopeType.Valid() {
		return fmt.Errorf("scope: invalid scope type %q", p.ScopeType)
	}
	if p.ScopeID < 0 {
		return fmt.Errorf("scope: scope id must not be negative")
	}
	if !p.ReplyMode.Valid() {
		return fmt.Errorf("scope: invalid reply mode %q", p.ReplyMode)
	}
	if p.MinReplyIntervalMS < 0 || p.MaxRepliesPerHour < 0 {
		return fmt.Errorf("scope: policy numbers must not be negative")
	}
	if p.MaxMessagesPerRun < 0 || p.MaxCharsPerMessage < 0 || p.MaxTotalCharsPerRun < 0 || p.MessageIntervalMS < 0 || p.MessageIntervalMS > 60000 {
		return fmt.Errorf("scope: invalid sending limits")
	}
	if p.MaxTotalCharsPerRun > 0 && p.MaxCharsPerMessage > p.MaxTotalCharsPerRun {
		return fmt.Errorf("scope: message limit exceeds run limit")
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
const maxToolPermissions = 128

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
