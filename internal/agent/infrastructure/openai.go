// OpenAI-compatible chat/completions adapter.
// OpenAI 兼容的 chat/completions 适配器。
package infrastructure

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	agentdomain "github.com/HappyLadySauceM/QQBot/internal/agent/domain"
)

// OpenAIConfig configures the adapter; BaseURL usually ends with /v1.
// OpenAIConfig 配置适配器；BaseURL 通常以 /v1 结尾。
type OpenAIConfig struct {
	BaseURL         string
	APIKey          string
	Model           string
	Timeout         time.Duration
	MaxOutputTokens int
}

// OpenAI implements the ChatModel port against any OpenAI-compatible endpoint.
// OpenAI 面向任意 OpenAI 兼容端点实现 ChatModel 端口。
type OpenAI struct {
	cfg    OpenAIConfig
	client *http.Client
}

// NewOpenAI builds the adapter.
// NewOpenAI 构建适配器。
func NewOpenAI(cfg OpenAIConfig) (*OpenAI, error) {
	if cfg.BaseURL == "" || cfg.Model == "" {
		return nil, fmt.Errorf("agent: openai base-url and model are required")
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = 25 * time.Second
	}
	if cfg.MaxOutputTokens <= 0 {
		cfg.MaxOutputTokens = 400
	}
	return &OpenAI{cfg: cfg, client: &http.Client{Timeout: cfg.Timeout}}, nil
}

type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type chatRequest struct {
	Model     string        `json:"model"`
	Messages  []chatMessage `json:"messages"`
	MaxTokens int           `json:"max_tokens"`
}

type chatResponse struct {
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	} `json:"choices"`
	Usage struct {
		PromptTokens     int64 `json:"prompt_tokens"`
		CompletionTokens int64 `json:"completion_tokens"`
	} `json:"usage"`
}

// Generate calls chat/completions and extracts the first choice.
// Generate 调用 chat/completions 并提取第一个选择。
func (o *OpenAI) Generate(ctx context.Context, req agentdomain.ChatRequest) (agentdomain.ChatResult, error) {
	maxTokens := req.MaxTokens
	if maxTokens <= 0 {
		maxTokens = o.cfg.MaxOutputTokens
	}
	payload := chatRequest{Model: o.cfg.Model, MaxTokens: maxTokens}
	for _, m := range req.Messages {
		payload.Messages = append(payload.Messages, chatMessage{Role: m.Role, Content: m.Content})
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return agentdomain.ChatResult{}, fmt.Errorf("agent: encode request: %w", err)
	}
	url := strings.TrimRight(o.cfg.BaseURL, "/") + "/chat/completions"
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return agentdomain.ChatResult{}, fmt.Errorf("agent: build request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	if o.cfg.APIKey != "" {
		httpReq.Header.Set("Authorization", "Bearer "+o.cfg.APIKey)
	}
	resp, err := o.client.Do(httpReq)
	if err != nil {
		return agentdomain.ChatResult{}, fmt.Errorf("agent: call llm: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return agentdomain.ChatResult{}, fmt.Errorf("agent: llm status %d", resp.StatusCode)
	}
	var parsed chatResponse
	if err := json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(&parsed); err != nil {
		return agentdomain.ChatResult{}, fmt.Errorf("agent: decode llm response: %w", err)
	}
	if len(parsed.Choices) == 0 {
		return agentdomain.ChatResult{}, fmt.Errorf("agent: llm returned no choices")
	}
	return agentdomain.ChatResult{
		Text:             parsed.Choices[0].Message.Content,
		Model:            o.cfg.Model,
		PromptTokens:     parsed.Usage.PromptTokens,
		CompletionTokens: parsed.Usage.CompletionTokens,
	}, nil
}
