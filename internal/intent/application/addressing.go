package application

import (
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/HappyLadySauceM/Nagi/internal/intent/domain"
	ob "github.com/HappyLadySauceM/Nagi/internal/onebot/domain"
)

func mentionsBotName(task domain.Task, name string) bool {
	name = strings.ToLower(strings.TrimSpace(name))
	if name == "" {
		return false
	}
	text := task.Message.Text
	if len(task.Segments) > 0 {
		text = nameCallText(task.Segments)
	}
	text = strings.ToLower(text)
	for start := 0; start < len(text); {
		offset := strings.Index(text[start:], name)
		if offset < 0 {
			return false
		}
		start += offset
		end := start + len(name)
		before, _ := utf8.DecodeLastRuneInString(text[:start])
		after, _ := utf8.DecodeRuneInString(text[end:])
		if !nameWordRune(before) && !nameWordRune(after) {
			return true
		}
		start = end
	}
	return false
}

func nameCallText(segments []ob.Segment) string {
	var text strings.Builder
	for _, segment := range segments {
		if segment.Type == ob.SegText {
			text.WriteString(segment.Data["text"])
		} else {
			text.WriteByte(0)
		}
	}
	return strings.Trim(text.String(), "\x00 \t\r\n")
}

func nameWordRune(r rune) bool {
	return r == '_' || unicode.IsNumber(r) || unicode.IsLetter(r) && !unicode.In(r, unicode.Han, unicode.Hiragana, unicode.Katakana, unicode.Hangul)
}

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
	if directNameCall(callText(previous.Text, previous.Segments), nil, in.BotName) {
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
	for _, segment := range segments {
		if segment.Type != ob.SegText && segment.Type != ob.SegImage {
			return false
		}
	}
	if len(segments) > 0 {
		text = nameCallText(segments)
	}
	name = strings.ToLower(strings.TrimSpace(name))
	text = strings.ToLower(strings.TrimSpace(text))
	if name == "" || text == "" {
		return false
	}
	const punctuation = " \t，,：:！!？?。."
	if rest, ok := strings.CutPrefix(text, name); ok {
		rest = strings.TrimSpace(strings.ReplaceAll(rest, "\x00", ""))
		question := strings.HasSuffix(rest, "?") || strings.HasSuffix(rest, "？")
		rest = strings.Trim(rest, punctuation)
		question = question || strings.HasSuffix(rest, "吗") || strings.HasSuffix(rest, "嘛")
		if rest == "" || shortCall(rest) {
			return true
		}
		if rest == "是谁" || rest == "你是谁" {
			return true
		}
		switch strings.TrimPrefix(rest, "请") {
		case "告诉我几点了", "告诉我现在几点了":
			return true
		}
		if tail, ok := strings.CutPrefix(rest, "你怎么看"); ok &&
			!strings.HasPrefix(tail, "都") && !strings.HasPrefix(tail, "也") &&
			(question || tail == "" || strings.HasPrefix(tail, "这") || strings.HasPrefix(tail, "那")) {
			return true
		}
		for _, request := range []string{"请帮", "请解释", "请分析", "帮我", "能帮", "可以帮"} {
			if strings.HasPrefix(rest, request) {
				return true
			}
		}
		if question {
			for _, request := range []string{"你能", "你可以", "你觉得"} {
				if strings.HasPrefix(rest, request) {
					return true
				}
			}
		}
	}
	if rest, ok := strings.CutSuffix(strings.TrimRight(text, punctuation), name); ok {
		return strings.Trim(rest, punctuation) == "谁是" || shortCall(rest)
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
	if len(quoted) != 0 || reason == "direct_name_call" && !refersToEarlierDiscussion(in.Task) {
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
