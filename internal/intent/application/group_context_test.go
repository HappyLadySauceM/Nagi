package application

import (
	"strings"
	"testing"
	"time"

	"github.com/HappyLadySauceM/Nagi/internal/intent/domain"
	ob "github.com/HappyLadySauceM/Nagi/internal/onebot/domain"
	scope "github.com/HappyLadySauceM/Nagi/internal/scope/domain"
)

func TestGroupWindowRetentionAndBudget(t *testing.T) {
	now := time.Unix(1000, 0)
	message := func(id, sender int64, age time.Duration, text string) domain.Message {
		return domain.Message{RowID: id, MessageID: -id, SenderID: sender, CreatedAt: now.Add(-age), Text: text}
	}
	older := message(1, 11, 120*time.Second+time.Millisecond, "outside")
	call := message(2, 11, 120*time.Second, "Nagi，请继续这道题")
	reply := message(3, 1, 119*time.Second, "好的")
	reply.IsBot = true
	in := domain.Input{Task: domain.Task{BotID: 1, Kind: "group", Message: message(10, 11, 0, "接着问")},
		CallIdentity: &domain.CallIdentity{BotID: 1, CallName: "Nagi"}, Policy: scope.Policy{AllowHistoryRead: true}}
	in.Recent = []domain.Message{older, call, reply}
	for i := int64(4); i < 10; i++ {
		in.Recent = append(in.Recent, message(i, 12, time.Duration(10-i)*time.Second, strings.Repeat("干扰", 450)))
	}
	in.Recent = append(in.Recent, message(11, 13, 0, "future"))
	b, bTruncated := recentGroupMessages(in, false)
	c, cTruncated := recentGroupMessages(in, true)
	if len(b) != 6 || len(c) != 6 || !bTruncated || !cTruncated || b[0].RowID != 4 || c[0].RowID != 2 || c[1].RowID != 3 || c[5].RowID != 9 {
		t.Fatalf("window B=%v C=%v", b, c)
	}
	in.Recent = c
	in.Topics = &domain.TopicContext{Reference: &call, Candidates: []domain.TopicCandidate{{ID: 1, History: []domain.Message{call, reply}}}}
	window := buildGroupContext(in, cTruncated)
	if window.HistoryUnits > domain.GroupContextBudget || len(window.Messages) != 6 || !window.Truncated {
		t.Fatalf("window budget=%d count=%d truncated=%v", window.HistoryUnits, len(window.Messages), window.Truncated)
	}
	if a := window.Messages[0]; a.RowID != 2 || a.MillisecondsAgo != 120000 || strings.Join(a.Sources, ",") != "quote,recent,nagi_topic_1" {
		t.Fatalf("admitted reference=%+v", a)
	}
	in.Policy.AllowHistoryRead = false
	if denied := buildGroupContext(in, false); len(denied.Messages) != 0 || denied.HistoryUnits != 0 {
		t.Fatalf("denied history=%+v", denied)
	}
}

func TestGroupEvidenceRetainsMentionPositionAndOmitsAttachments(t *testing.T) {
	in := domain.Input{Task: domain.Task{Message: domain.Message{RowID: 3, MessageID: -3}, Segments: []ob.Segment{
		ob.Text("你知道 "), ob.At(13), ob.Text(" 是谁吗？"), {Type: ob.SegImage, Data: map[string]string{"url": "https://example.invalid/synthetic"}},
	}}}
	m := buildGroupContext(in, false).Current
	if m.Text != "你知道 @13 是谁吗？[图片]" || len(m.Mentions) != 1 || m.Mentions[0] != "13" || m.Sources[0] != "pending_judgment" {
		t.Fatalf("current=%+v", m)
	}
}

func TestGroupEvidenceRetainsTranscriptionWithoutAudioReference(t *testing.T) {
	segments := []ob.Segment{{Type: ob.SegRecord, Data: map[string]string{"file": "synthetic-audio"}}}
	m := domain.Message{Segments: markTranscribedSegments(segments), Text: "[CQ:record,file=synthetic-audio]\n[语音转写] 这是语音内容"}
	got := groupEvidence(m, time.Time{}, "recent")
	if got.Text != "[语音]\n[语音转写] 这是语音内容" {
		t.Fatalf("text=%q", got.Text)
	}
}
