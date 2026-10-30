// Package qq holds protocol limits shared by configuration defaults and the
// policy and settings domains.
// Package qq 提供协议层常量，供配置默认值与策略/配置领域复用。
package qq

// MaxTextRunes is the maximum rune count of one QQ text message. Longer text
// is rejected or truncated by the client, so it is the natural default for the
// per-message sending budget.
// MaxTextRunes 是单条 QQ 文本消息的最大字符数。更长的文本会被客户端拒绝或
// 截断，因此它是单条发送预算的合理默认值。
const MaxTextRunes = 4500
