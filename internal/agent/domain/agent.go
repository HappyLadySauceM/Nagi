// Package domain defines the LLM chat request/result model and output checks.
// agent/domain 定义 LLM 对话请求/结果模型与输出检查。
package domain

import (
	"fmt"
	"regexp"
	"strings"
)

// replyURLPattern matches outbound links; the model must not hand out URLs it
// may have fabricated, so any reply containing one is dropped (fail-closed).
// replyURLPattern 匹配出站链接；模型可能编造 URL，包含链接的回复一律丢弃
// （关闭式失败）。
var replyURLPattern = regexp.MustCompile(`(?i)(https?://|www\.)`)

// ChatMessage is one OpenAI-compatible chat message.
// ChatMessage 是一条 OpenAI 兼容对话消息。
type ChatMessage struct {
	Role    string
	Content string
}

// ChatRequest is one generation request; MaxTokens bounds the reply length.
// ChatRequest 是一次生成请求；MaxTokens 限制回复长度。
type ChatRequest struct {
	Messages  []ChatMessage
	MaxTokens int
}

// ChatResult is one generation result with usage information.
// ChatResult 是一次生成结果及用量信息。
type ChatResult struct {
	Text             string
	Model            string
	PromptTokens     int64
	CompletionTokens int64
}

// MaxReplyRunes bounds the sent reply length after generation.
// MaxReplyRunes 限制发送回复的最大字符数。
const MaxReplyRunes = 500

// SanitizeReply trims the generated text, drops at-all patterns (including
// fullwidth variants), rejects outbound links and enforces the length bound;
// empty or rejected output reports not sendable.
// SanitizeReply 修剪生成文本、去除 @ 全体模式（含全角变体）、拒绝出站链接
// 并执行长度上限；输出为空或被拒绝时报告不可发送。
func SanitizeReply(text string) (string, error) {
	text = strings.TrimSpace(text)
	for _, pattern := range []string{
		"@全体成员", "@所有人", "@everyone", "@all", "[CQ:at,qq=all]",
		"＠全体成员", "＠所有人", "＠everyone", "＠all",
	} {
		text = strings.ReplaceAll(text, pattern, "")
	}
	text = strings.TrimSpace(text)
	if text == "" {
		return "", fmt.Errorf("agent: empty reply")
	}
	if replyURLPattern.MatchString(text) {
		return "", fmt.Errorf("agent: reply contains a link")
	}
	runes := []rune(text)
	if len(runes) > MaxReplyRunes {
		text = string(runes[:MaxReplyRunes])
	}
	return text, nil
}
