package infrastructure

import (
	"encoding/json"
	"strings"
	"testing"
	"unicode/utf8"
)

// TestFitExtractionInputCountsLabelsAndTruncates verifies that source labels,
// message ids and sender ids are included in the rune budget, and that an
// oversized message carries an explicit truncation marker.
// TestFitExtractionInputCountsLabelsAndTruncates 验证来源标签、消息 ID 与发送者 ID
// 计入字符预算，超长消息带有明确截断标记。
func TestFitExtractionInputCountsLabelsAndTruncates(t *testing.T) {
	input := extractionInput{
		BotID: 1, ConversationID: "conv", ScopeType: "group", ScopeID: 700,
		Messages: []extractionInputMessage{
			{MessageID: 11, SenderID: 22, Text: strings.Repeat("茶", 10000)},
			{MessageID: 12, SenderID: 23, Text: "短消息"},
		},
	}
	fitted := fitExtractionInput(input, 500)
	payload, err := json.Marshal(fitted)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	text := string(payload)
	if got := utf8.RuneCountInString(text); got > 500 {
		t.Fatalf("serialized extraction input = %d runes, want <= 500", got)
	}
	if !strings.Contains(text, `"message_id":11`) || !strings.Contains(text, `"sender_id":22`) || !strings.Contains(text, `"message_id":12`) {
		t.Fatalf("source labels missing from bounded input: %s", text)
	}
	if !strings.Contains(text, "内容已截断") {
		t.Fatalf("oversized message was truncated without marker: %s", text)
	}
}
