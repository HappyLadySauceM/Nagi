package application

import (
	"cmp"
	"encoding/json"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/HappyLadySauceM/Nagi/internal/intent/domain"
	media "github.com/HappyLadySauceM/Nagi/internal/media/contracts"
	ob "github.com/HappyLadySauceM/Nagi/internal/onebot/domain"
)

const groupScanLimit = 96

func recentGroupMessages(in domain.Input, retained bool) ([]domain.Message, bool) {
	history, at := in.HistoryBeforeCurrent()
	history = slices.DeleteFunc(slices.Clone(history), func(m domain.Message) bool {
		return m.CreatedAt.Before(at.Add(-domain.GroupWindow))
	})
	if len(history) <= domain.GroupRecentLimit {
		return history, false
	}
	newest := len(history) - domain.GroupRecentLimit
	if retained {
		for i := newest - 1; i >= 0; i-- {
			m, reply := history[i], history[i+1]
			if m.IsBot || m.SenderID != in.Task.Message.SenderID || !reply.IsBot || !callsAccount(in, m) {
				continue
			}
			return append([]domain.Message{m, reply}, history[newest+2:]...), true
		}
	}
	return history[newest:], true
}

func callsAccount(in domain.Input, m domain.Message) bool {
	segments := evidenceSegments(m)
	bot := strconv.FormatInt(in.Task.BotID, 10)
	for _, segment := range segments {
		if segment.Type == ob.SegAt && segment.Data["qq"] == bot {
			return true
		}
	}
	text := strings.TrimLeftFunc(ob.PlainText(segments), func(r rune) bool {
		return unicode.IsSpace(r) || r == '@' || r == '＠'
	})
	for _, name := range in.CallNames() {
		rest, found := strings.CutPrefix(text, name)
		if found && (rest == "" || strings.IndexFunc(rest, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsPunct(r) }) == 0) {
			return true
		}
	}
	return false
}

func evidenceSegments(m domain.Message) []ob.Segment {
	if len(m.Segments) > 0 {
		return m.Segments
	}
	return ob.ParseCQ(m.Text)
}

func groupEvidence(m domain.Message, at time.Time, source string) domain.GroupEvidence {
	e := domain.GroupEvidence{MessageID: m.MessageID, RowID: m.RowID, SenderID: m.SenderID, Bot: m.IsBot, Sources: []string{source}}
	if !at.IsZero() && !m.CreatedAt.IsZero() {
		e.MillisecondsAgo = at.Sub(m.CreatedAt).Milliseconds()
	}
	var text strings.Builder
	var transcribed bool
	for _, part := range evidenceSegments(m) {
		switch part.Type {
		case ob.SegText:
			text.WriteString(part.Data["text"])
		case ob.SegAt:
			id := part.Data["qq"]
			if len(id) <= 32 && id != "" && len(e.Mentions) < 12 {
				e.Mentions = append(e.Mentions, id)
				text.WriteString("@" + id)
			}
		case ob.SegReply:
			id := part.Data["id"]
			if len(id) <= 32 && id != "" && len(e.Replies) < 12 {
				e.Replies = append(e.Replies, id)
			}
		case ob.SegImage:
			text.WriteString("[图片]")
		case ob.SegRecord:
			transcribed = transcribed || part.Data[media.TranscriptMarker] == "true"
			text.WriteString("[语音]")
		default:
			text.WriteString("[附件]")
		}
	}
	if transcribed {
		_, transcript, found := strings.Cut(m.Text, "\n[语音转写] ")
		if found {
			text.WriteString("\n[语音转写] ")
			text.WriteString(ob.PlainText(ob.ParseCQ(transcript)))
		}
	}
	e.Text = text.String()
	return e
}

func buildGroupContext(in domain.Input, truncated bool) *domain.GroupContext {
	out := &domain.GroupContext{Version: "group-context-c-v1", Meter: domain.GroupContextMeter, HistoryLimit: domain.GroupContextBudget, Truncated: truncated}
	current := in.Task.Message
	current.Segments = in.Task.Segments
	out.Current = groupEvidence(current, current.CreatedAt, "pending_judgment")
	if !in.Policy.AllowHistoryRead {
		return out
	}
	type evidenceKey struct {
		ID, Row int64
		Bot     bool
	}
	seen := make(map[evidenceKey]int)
	add := func(m domain.Message, source string) {
		current := in.Task.Message
		lateConfirmedQuote := source == "quote" && m.IsBot && in.Topics.QuotedBot
		if !lateConfirmedQuote && ((m.RowID > 0 && current.RowID > 0 && m.RowID >= current.RowID) || (!current.CreatedAt.IsZero() && m.CreatedAt.After(current.CreatedAt))) || (m.MessageID != 0 && !m.IsBot && m.MessageID == current.MessageID) {
			return
		}
		key := evidenceKey{m.MessageID, m.RowID, m.IsBot}
		if m.MessageID != 0 {
			key.Row = 0
		}
		if index, ok := seen[key]; ok && (key.ID != 0 || key.Row != 0) {
			if !slices.Contains(out.Messages[index].Sources, source) {
				out.Messages[index].Sources = append(out.Messages[index].Sources, source)
			}
			return
		}
		seen[key] = len(out.Messages)
		out.Messages = append(out.Messages, groupEvidence(m, current.CreatedAt, source))
	}
	if in.Topics != nil && in.Topics.Reference != nil {
		add(*in.Topics.Reference, "quote")
	}
	if in.AwaitedRequest != nil {
		add(*in.AwaitedRequest, "awaited")
	}
	for _, m := range in.Recent {
		add(m, "recent")
	}
	if in.Topics != nil {
		for i, topic := range in.Topics.Candidates {
			for _, m := range slices.Backward(topic.History) {
				add(m, domain.TopicQuestionKey(i))
			}
		}
	}
	budget := domain.GroupContextBudget - 2
	selected := out.Messages[:0]
	for _, m := range out.Messages {
		original := m.Text
		runes := []rune(m.Text)
		m.Text = string(runes[:min(len(runes), 192)])
		m.Truncated = m.Text != original
		payload, _ := json.Marshal(m)
		if len(payload)+1 > budget {
			m.Text = ""
			m.Truncated = original != ""
			payload, _ = json.Marshal(m)
			if len(payload)+1 > budget {
				out.Truncated = true
				continue
			}
		}
		budget -= len(payload) + 1
		selected = append(selected, m)
		out.Truncated = out.Truncated || m.Truncated
	}
	out.Messages = selected
	slices.SortStableFunc(out.Messages, func(a, b domain.GroupEvidence) int {
		return cmp.Or(cmp.Compare(b.MillisecondsAgo, a.MillisecondsAgo), cmp.Compare(a.RowID, b.RowID))
	})
	payload, _ := json.Marshal(out.Messages)
	out.HistoryUnits = len(payload)
	return out
}

func withoutDecisionHistory(in domain.Input) domain.Input {
	in.Policy.AllowHistoryRead = false
	in.Recent, in.AwaitedRequest, in.Topics = nil, nil, nil
	if in.GroupContext != nil {
		in.GroupContext = buildGroupContext(in, false)
	}
	return in
}

func snapshotRecent(in domain.Input) []domain.Message {
	if in.GroupContext == nil {
		return in.Recent
	}
	recent := slices.Clone(in.Recent)
	for i, m := range recent {
		recent[i].Text = groupEvidence(m, in.Task.Message.CreatedAt, "recent").Text
		recent[i].Segments = nil
	}
	return recent
}

func groupRecentAudit(window *domain.GroupContext) map[string]any {
	if window == nil {
		return nil
	}
	ids := make([]map[string]any, 0, len(window.Messages))
	for _, m := range window.Messages {
		ids = append(ids, map[string]any{"MessageID": m.MessageID, "RowID": m.RowID, "MillisecondsAgo": m.MillisecondsAgo, "Sources": m.Sources, "Truncated": m.Truncated})
	}
	return map[string]any{"Version": window.Version, "Meter": window.Meter, "HistoryUnits": window.HistoryUnits, "HistoryLimit": window.HistoryLimit, "Truncated": window.Truncated, "Messages": ids}
}

func markTranscribedSegments(segments []ob.Segment) []ob.Segment {
	segments = slices.Clone(segments)
	for i, segment := range segments {
		if segment.Type == ob.SegRecord {
			segments[i].Data = map[string]string{media.TranscriptMarker: "true"}
			for key, value := range segment.Data {
				if key != media.TranscriptMarker {
					segments[i].Data[key] = value
				}
			}
		}
	}
	return segments
}
