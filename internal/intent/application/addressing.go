package application

import (
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/HappyLadySauceM/Nagi/internal/intent/domain"
	ob "github.com/HappyLadySauceM/Nagi/internal/onebot/domain"
)

func groupCallReason(in domain.Input) string {
	if directNameCall(in.Task.Message.Text, in.Task.Segments, in.BotName) {
		return "direct_name_call"
	}
	if !in.Policy.AllowHistoryRead || in.Task.Message.SenderID <= 0 ||
		!shortCall(callText(in.Task.Message.Text, in.Task.Segments)) {
		return ""
	}
	recent, at := in.HistoryBeforeCurrent()
	if len(recent) == 0 || at.IsZero() {
		return ""
	}
	previous := recent[len(recent)-1]
	elapsed := at.Sub(previous.CreatedAt)
	if previous.IsBot || previous.SenderID != in.Task.Message.SenderID ||
		previous.CreatedAt.IsZero() || elapsed < 0 || elapsed > 30*time.Second {
		return ""
	}
	if !in.Activity.LastReplyAt.IsZero() && !in.Activity.LastReplyAt.Before(previous.CreatedAt) {
		return ""
	}
	if directNameCall(previous.Text, previous.Segments, in.BotName) {
		return "unanswered_call_followup"
	}
	return ""
}

func callText(text string, segments []ob.Segment) string {
	if len(segments) > 0 {
		for _, segment := range segments {
			if segment.Type != ob.SegText {
				return ""
			}
		}
		text = ob.PlainText(segments)
	}
	return strings.TrimSpace(text)
}

func directNameCall(text string, segments []ob.Segment, name string) bool {
	name = strings.ToLower(strings.TrimSpace(name))
	text = strings.ToLower(callText(text, segments))
	if name == "" || text == "" {
		return false
	}
	const punctuation = " \t，,：:！!？?。."
	text = strings.TrimRight(text, punctuation)
	if rest, ok := strings.CutPrefix(text, name); ok && (rest == "" || shortCall(rest)) {
		return true
	}
	if rest, ok := strings.CutSuffix(text, name); ok {
		return shortCall(rest)
	}
	return false
}

func shortCall(text string) bool {
	switch strings.Trim(text, " \t，,：:！!？?。.") {
	case "在吗", "在嘛", "在不在", "在不", "有人吗", "有人嘛", "你好", "嗨":
		return true
	}
	return false
}

// requestsFollowingContent identifies a directed request that explicitly refers
// to material the sender is about to send.
// requestsFollowingContent 识别明确指向机器人、且预告即将发送内容的请求。
func requestsFollowingContent(in domain.Input) bool {
	if !in.Policy.AllowHistoryRead {
		return false
	}
	var text strings.Builder
	if len(in.Task.Segments) == 0 {
		text.WriteString(in.Task.Message.Text)
	}
	for _, segment := range in.Task.Segments {
		switch segment.Type {
		case ob.SegText:
			text.WriteString(segment.Data["text"])
		case ob.SegAt:
			if segment.Data["qq"] != strconv.FormatInt(in.Task.BotID, 10) {
				return false
			}
		case ob.SegReply:
			if !in.RepliesToBot() {
				return false
			}
		default:
			return false
		}
	}
	body := strings.ToLower(strings.TrimSpace(text.String()))
	if !in.MentionsBot() && !in.RepliesToBot() {
		name := strings.ToLower(strings.TrimSpace(in.BotName))
		rest, ok := strings.CutPrefix(body, name)
		if name == "" || !ok {
			return false
		}
		body = strings.TrimLeft(rest, " \t，,：:")
		if !strings.HasPrefix(body, "你") && !strings.HasPrefix(body, "请") {
			return false
		}
	}
	request := strings.Contains(body, "看看") || strings.Contains(body, "看一下") ||
		strings.Contains(body, "看下") || strings.Contains(body, "分析")
	forthcoming := strings.Contains(body, "下面发") || strings.Contains(body, "接下来发") ||
		strings.Contains(body, "等会发") || strings.Contains(body, "待会发") ||
		strings.Contains(body, "我发给你")
	return request && forthcoming
}

func eligibleSupplement(task domain.Task) bool {
	quoted := false
	for _, segment := range task.Segments {
		if segment.Type == ob.SegAt && segment.Data["qq"] != strconv.FormatInt(task.BotID, 10) {
			return false
		}
		quoted = quoted || segment.Type == ob.SegReply
	}
	hasImage, text := supplementContents(task)
	return hasImage || quoted && text != "" || utf8.RuneCountInString(text) >= 6
}

func supplementContents(task domain.Task) (hasImage bool, text string) {
	if len(task.Segments) == 0 {
		return false, strings.TrimSpace(task.Message.Text)
	}
	for _, segment := range task.Segments {
		if segment.Type == ob.SegImage {
			hasImage = true
		}
	}
	return hasImage, strings.TrimSpace(ob.PlainText(task.Segments))
}

func awaitingReply(in domain.Input) bool {
	previous := in.AwaitedRequest
	if !in.Policy.AllowHistoryRead || previous == nil || previous.SenderID != in.Task.Message.SenderID ||
		!eligibleSupplement(in.Task) {
		return false
	}
	for _, segment := range in.Task.Segments {
		if segment.Type == ob.SegReply && segment.Data["id"] != strconv.FormatInt(previous.MessageID, 10) {
			return false
		}
	}
	hasImage, text := supplementContents(in.Task)
	switch {
	case strings.Contains(previous.Text, "图片") || strings.Contains(previous.Text, "发图"):
		return hasImage
	case strings.Contains(previous.Text, "文字") || strings.Contains(previous.Text, "文本"):
		return text != ""
	default:
		return hasImage || text != ""
	}
}

func refersToEarlierDiscussion(task domain.Task) bool {
	_, text := supplementContents(task)
	return strings.Contains(text, "刚才") || strings.Contains(text, "之前") ||
		strings.Contains(text, "前面") || strings.Contains(text, "上面") || strings.Contains(text, "接着聊")
}

func replyHistory(in domain.Input, reason string) []domain.Message {
	history, _ := in.HistoryBeforeCurrent()
	if in.Task.Kind != "group" {
		return in.Recent
	}
	if reason == "awaited_content_received" && awaitingReply(in) {
		return []domain.Message{*in.AwaitedRequest}
	}

	var quoted []domain.Message
	for _, segment := range in.Task.Segments {
		if segment.Type != ob.SegReply {
			continue
		}
		for _, message := range history {
			if segment.Data["id"] == strconv.FormatInt(message.MessageID, 10) {
				quoted = append(quoted, message)
				break
			}
		}
	}
	if len(quoted) != 0 || reason == "direct_name_call" {
		return quoted
	}
	if reason == "unanswered_call_followup" && len(history) != 0 {
		return history[len(history)-1:]
	}
	if reason == "active_thread_continuation" {
		for i := len(history) - 1; i >= 0; i-- {
			if history[i].IsBot {
				return history[i : i+1]
			}
		}
	}
	if refersToEarlierDiscussion(in.Task) {
		const maxDiscussionMessages = 4
		evidence := make([]domain.Message, 0, maxDiscussionMessages)
		for i := len(history) - 1; i >= 0 && len(evidence) < maxDiscussionMessages; i-- {
			message := history[i]
			if len(message.Segments) > 0 {
				message.Text = ob.PlainText(message.Segments)
				message.Segments = nil
			}
			if strings.TrimSpace(message.Text) != "" {
				evidence = append(evidence, message)
			}
		}
		slices.Reverse(evidence)
		return evidence
	}
	return nil
}
