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

func callNameMentioned(in domain.Input) bool {
	if in.CallIdentity == nil {
		return mentionsBotName(in.Task, in.BotName)
	}
	for _, name := range in.CallNames() {
		if mentionsBotName(in.Task, name) {
			return true
		}
	}
	return false
}

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
	body := strings.TrimSpace(text.String())
	return forthcomingMaterial(body) != ""
}

func forthcomingMaterial(text string) string {
	if !strings.Contains(text, "发") || !strings.Contains(text, "下面") && !strings.Contains(text, "接下来") &&
		!strings.Contains(text, "等会") && !strings.Contains(text, "待会") && !strings.Contains(text, "稍后") && !strings.Contains(text, "接着") {
		return ""
	}
	return materialType(text)
}

func materialType(text string) string {
	switch {
	case strings.Contains(text, "图片") || strings.Contains(text, "截图") || strings.Contains(text, "发图") || strings.Contains(text, "张图"):
		return "image"
	case strings.Contains(text, "文字") || strings.Contains(text, "文本") || strings.Contains(text, "日志") || strings.Contains(text, "代码"):
		return "text"
	}
	return ""
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

func hasSupplementText(task domain.Task) bool {
	_, text := supplementContents(task)
	return text != ""
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
	if at := in.Task.Message.CreatedAt; !at.IsZero() && !previous.CreatedAt.IsZero() && (at.Before(previous.CreatedAt) || at.Sub(previous.CreatedAt) > 30*time.Second) {
		return false
	}
	for _, segment := range in.Task.Segments {
		if segment.Type == ob.SegReply && segment.Data["id"] != strconv.FormatInt(previous.MessageID, 10) {
			return false
		}
	}
	hasImage, text := supplementContents(in.Task)
	switch materialType(previous.Text) {
	case "image":
		return hasImage
	case "text":
		return hasTextMaterialEvidence(previous.Text, text)
	default:
		return false
	}
}

func hasTextMaterialEvidence(request, text string) bool {
	if strings.HasPrefix(text, "```") && strings.Contains(text[3:], "\n") {
		return true
	}
	for _, label := range []string{"文字", "文本", "日志", "代码"} {
		if !strings.Contains(request, label) {
			continue
		}
		for _, prefix := range []string{label, "这是" + label} {
			if rest, ok := strings.CutPrefix(text, prefix); ok {
				rest = strings.TrimSpace(rest)
				if strings.HasPrefix(rest, ":") || strings.HasPrefix(rest, "：") {
					return strings.TrimSpace(strings.TrimLeft(rest, ":：")) != ""
				}
			}
		}
	}
	return false
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
	if len(quoted) != 0 {
		return quoted
	}
	if in.GroupContext != nil {
		return nil
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
