package application

import (
	"strings"
	"time"

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
