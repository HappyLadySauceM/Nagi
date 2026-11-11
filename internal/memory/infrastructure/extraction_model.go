package infrastructure

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/cloudwego/eino-ext/components/model/openai"
	einoschema "github.com/cloudwego/eino/schema"

	"github.com/HappyLadySauceM/Nagi/internal/memory/domain"
	models "github.com/HappyLadySauceM/Nagi/internal/models/contracts"
	"github.com/HappyLadySauceM/Nagi/pkg/modelhttp"
)

// ModelExtractor implements Extractor with one OpenAI-compatible chat call.
// ModelExtractor 使用一次 OpenAI 兼容聊天调用实现 Extractor。
type ModelExtractor struct {
	resolver models.Resolver
}

// NewModelExtractor builds an extractor around the live model resolver.
// NewModelExtractor 围绕实时模型解析器构建抽取器。
func NewModelExtractor(resolver models.Resolver) *ModelExtractor {
	return &ModelExtractor{resolver: resolver}
}

const extractionSystemPrompt = `你是长期记忆候选抽取器。请只从给定聊天消息中提取值得长期保存的事实或偏好候选。
只输出一个 JSON 对象，格式为：
{"candidates":[{"content":"候选内容","confidence":0.0,"source_message_id":123,"subject":"sender|group|unknown","subject_label":"可选主体名称","review_reason":"可选取值"}]}

规则：
1. source_message_id 必须来自输入消息，不得编造。
2. subject 是记忆事实的主体关系：第一人称或明确关于发送者本人时用 sender；只有明确是群体级事实（如“本群每周五活动”）时才用 group；不要因为来源是群聊就默认使用 group；转述、猜测或无法确定主体时用 unknown。
3. 绝不要把“别人说的事”归到发送者；subject=unknown 时正文保持原意，不要把发送者写进正文。
4. 不提取密码、令牌、验证码、临时聊天、一次性任务或无长期价值内容。
5. content 使用原文语言，最多 500 个字符；confidence 在 0 到 1 之间。
6. 没有合适候选时输出 {"candidates":[]}。不要输出 Markdown 代码块。`

type extractionInputMessage struct {
	MessageID int64  `json:"message_id"`
	SenderID  int64  `json:"sender_id"`
	Text      string `json:"text"`
}

type extractionInput struct {
	BotID          int64                    `json:"bot_id"`
	ConversationID string                   `json:"conversation_id"`
	ScopeType      string                   `json:"scope_type"`
	ScopeID        int64                    `json:"scope_id"`
	Messages       []extractionInputMessage `json:"messages"`
}

type rawExtractionOutput struct {
	Candidates *[]rawExtractionCandidate `json:"candidates"`
}

type rawExtractionCandidate struct {
	Content         string  `json:"content"`
	Confidence      float64 `json:"confidence"`
	SourceMessageID int64   `json:"source_message_id"`
	Subject         string  `json:"subject"`
	SubjectLabel    string  `json:"subject_label"`
	ReviewReason    string  `json:"review_reason"`
}

// Extract performs one bounded model call and validates the structured result.
// Extract 执行一次有界模型调用并校验结构化结果。
func (e *ModelExtractor) Extract(ctx context.Context, request domain.ExtractionRequest) ([]domain.ExtractionCandidate, error) {
	if e == nil || e.resolver == nil {
		return nil, fmt.Errorf("memory extraction: model resolver unavailable")
	}
	if len(request.Messages) == 0 {
		return nil, fmt.Errorf("memory extraction: messages required")
	}
	resolved, err := e.resolver.ResolveModel(ctx, "generation", request.Scope.BotID, request.Scope.ScopeType, request.Scope.ScopeID)
	if err != nil {
		return nil, err
	}
	input := extractionInput{
		BotID:          request.Scope.BotID,
		ConversationID: request.Scope.ConversationID,
		ScopeType:      request.Scope.ScopeType,
		ScopeID:        request.Scope.ScopeID,
		Messages:       make([]extractionInputMessage, 0, len(request.Messages)),
	}
	for _, message := range request.Messages {
		input.Messages = append(input.Messages, extractionInputMessage{
			MessageID: message.ID,
			SenderID:  message.SenderID,
			Text:      message.Text,
		})
	}
	input = fitExtractionInput(input, request.MaxRunes)
	payload, err := json.Marshal(input)
	if err != nil {
		return nil, fmt.Errorf("memory extraction: marshal input: %w", err)
	}
	timeout := time.Duration(resolved.Profile.TimeoutSeconds) * time.Second
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	maxTokens := 800
	temperature := float32(0.1)
	cfg := &openai.ChatModelConfig{
		BaseURL:     resolved.Profile.BaseURL,
		APIKey:      resolved.APIKey,
		Model:       resolved.Profile.Model,
		Timeout:     timeout,
		HTTPClient:  modelhttp.Client(&http.Client{Timeout: timeout}, resolved.Profile.HTTPHeaders),
		MaxTokens:   &maxTokens,
		Temperature: &temperature,
	}
	if resolved.Profile.Thinking && strings.TrimSpace(resolved.Profile.ReasoningEffort) != "" {
		cfg.ReasoningEffort = openai.ReasoningEffortLevel(resolved.Profile.ReasoningEffort)
	}
	chatModel, err := openai.NewChatModel(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("memory extraction: build model: %w", err)
	}
	response, err := chatModel.Generate(ctx, []*einoschema.Message{
		einoschema.SystemMessage(extractionSystemPrompt),
		einoschema.UserMessage("聊天消息：\n" + string(payload)),
	})
	if err != nil {
		return nil, fmt.Errorf("memory extraction: model call: %w", err)
	}
	if response == nil {
		return nil, fmt.Errorf("memory extraction: empty model response")
	}
	return parseExtractionCandidates(response.Content)
}

func parseExtractionCandidates(raw string) ([]domain.ExtractionCandidate, error) {
	trimmed := strings.TrimSpace(raw)
	if !strings.HasPrefix(trimmed, "{") || !strings.HasSuffix(trimmed, "}") {
		return nil, fmt.Errorf("memory extraction: invalid JSON output")
	}
	var output rawExtractionOutput
	if err := json.Unmarshal([]byte(trimmed), &output); err != nil {
		return nil, fmt.Errorf("memory extraction: decode output: %w", err)
	}
	if output.Candidates == nil {
		return nil, fmt.Errorf("memory extraction: candidates field required")
	}
	out := make([]domain.ExtractionCandidate, 0, len(*output.Candidates))
	for index, item := range *output.Candidates {
		content := strings.TrimSpace(item.Content)
		if content == "" || utf8.RuneCountInString(content) > 500 {
			return nil, fmt.Errorf("memory extraction: candidate %d has invalid content", index)
		}
		if item.Confidence < 0 || item.Confidence > 1 {
			return nil, fmt.Errorf("memory extraction: candidate %d has invalid confidence", index)
		}
		if item.SourceMessageID <= 0 {
			return nil, fmt.Errorf("memory extraction: candidate %d has invalid source_message_id", index)
		}
		subject := strings.ToLower(strings.TrimSpace(item.Subject))
		switch subject {
		case "sender", "user":
			subject = "sender"
		case "group", "unknown":
		case "":
			subject = "unknown"
		default:
			return nil, fmt.Errorf("memory extraction: candidate %d has invalid subject", index)
		}
		out = append(out, domain.ExtractionCandidate{
			Content:         content,
			Confidence:      item.Confidence,
			SourceMessageID: item.SourceMessageID,
			Subject:         subject,
			SubjectLabel:    boundRunes(strings.TrimSpace(item.SubjectLabel), 100),
			ReviewReason:    strings.TrimSpace(item.ReviewReason),
		})
	}
	return out, nil
}

const extractionTruncationMarker = "\n[内容已截断]"

// fitExtractionInput trims message text until the serialized model input fits
// the configured rune budget. Labels, message ids and sender ids stay in the
// serialized input and therefore count against the budget.
// fitExtractionInput 缩小消息正文直到序列化模型输入满足字符预算；来源标签、
// 消息 ID 与发送者 ID 始终保留并计入预算。
func fitExtractionInput(input extractionInput, maxRunes int) extractionInput {
	if maxRunes <= 0 {
		maxRunes = 4000
	}
	for {
		payload, err := json.Marshal(input)
		if err != nil {
			return input
		}
		excess := utf8.RuneCount(payload) - maxRunes
		if excess <= 0 {
			return input
		}
		reduced := false
		for index := range input.Messages {
			text := strings.TrimSuffix(input.Messages[index].Text, extractionTruncationMarker)
			runes := []rune(text)
			if len(runes) <= 1 {
				continue
			}
			cut := excess
			if cut > len(runes)-1 {
				cut = len(runes) - 1
			}
			input.Messages[index].Text = string(runes[:len(runes)-cut]) + extractionTruncationMarker
			excess -= cut
			reduced = true
			if excess <= 0 {
				break
			}
		}
		if !reduced {
			return input
		}
	}
}

func boundRunes(text string, limit int) string {
	if limit <= 0 {
		return ""
	}
	runes := []rune(text)
	if len(runes) <= limit {
		return text
	}
	return string(runes[:limit])
}
