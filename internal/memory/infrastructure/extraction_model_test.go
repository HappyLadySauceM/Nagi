package infrastructure

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/HappyLadySauceM/Nagi/internal/memory/domain"
	models "github.com/HappyLadySauceM/Nagi/internal/models/contracts"
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
	fitted, err := fitExtractionInput(input, 500)
	if err != nil {
		t.Fatalf("fit extraction input: %v", err)
	}
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
	if !strings.Contains(text, "短消息") {
		t.Fatalf("short message was over-trimmed after the budget was met: %s", text)
	}
}

// TestFitExtractionInputRejectsImpossibleBudget verifies that an input whose
// metadata alone exceeds the budget, or whose text cannot shrink further, is
// rejected instead of being passed to the model over budget.
// TestFitExtractionInputRejectsImpossibleBudget 验证仅元数据已超预算或正文无法继续缩短时
// 返回错误，而不是把超预算输入传给模型。
func TestFitExtractionInputRejectsImpossibleBudget(t *testing.T) {
	cases := []struct {
		name     string
		input    extractionInput
		maxRunes int
	}{
		{
			name: "metadata exceeds budget",
			input: extractionInput{
				BotID: 1, ConversationID: "conv", ScopeType: "group", ScopeID: 700,
				Messages: []extractionInputMessage{{MessageID: 11, SenderID: 22, Text: "x"}},
			},
			maxRunes: 10,
		},
		{
			name: "single rune cannot be trimmed",
			input: extractionInput{
				BotID: 1, ConversationID: "conv", ScopeType: "group", ScopeID: 700,
				Messages: []extractionInputMessage{{MessageID: 11, SenderID: 22, Text: "茶"}},
			},
			maxRunes: 1,
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			_, err := fitExtractionInput(testCase.input, testCase.maxRunes)
			if err == nil || !strings.Contains(err.Error(), "rune budget") {
				t.Fatalf("error = %v, want explicit rune budget error", err)
			}
		})
	}
}

type panicResolver struct{}

func (panicResolver) ResolveModel(context.Context, string, int64, string, int64) (models.Resolved, error) {
	panic("model resolver must not be called for an over-budget input")
}

func (panicResolver) BilibiliCredential(context.Context) (string, error) { return "", nil }

// TestModelExtractorRejectsOverBudgetBeforeResolve verifies the budget guard
// runs before model resolution and therefore before any model call.
// TestModelExtractorRejectsOverBudgetBeforeResolve 验证预算校验先于模型解析，
// 因此不会发起模型调用。
func TestModelExtractorRejectsOverBudgetBeforeResolve(t *testing.T) {
	extractor := NewModelExtractor(panicResolver{})
	_, err := extractor.Extract(context.Background(), domain.ExtractionRequest{
		Scope: domain.ExtractionScope{BotID: 1, ConversationID: "conv", ScopeType: "group", ScopeID: 700},
		Messages: []domain.ExtractionSourceMessage{
			{ID: 11, SenderID: 22, Text: "茶"},
		},
		MaxRunes: 1,
	})
	if err == nil || !strings.Contains(err.Error(), "rune budget") {
		t.Fatalf("error = %v, want explicit rune budget error", err)
	}
}
