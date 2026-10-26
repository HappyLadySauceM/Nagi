// Jev (TypeSafe SystemOne) HTTP adapter for semantic intent judgment.
// 用于语义意图判定的 Jev（TypeSafe SystemOne）HTTP 适配器。
package infrastructure

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"math"
	"net/http"
	"time"

	"github.com/HappyLadySauceM/Nagi/internal/intent/domain"
)

// JevConfig configures the Jev HTTP adapter.
// JevConfig 配置 Jev HTTP 适配器。
type JevConfig struct {
	Endpoint string // 形如 https://api.typesafe.ai/v1/systemone
	APIKey   string // Bearer 凭据；来自环境变量
	Model    string
	BotName  string
	Timeout  time.Duration
}

// JevClient calls the Jev SystemOne API; any malformed or out-of-range response
// is an error so the pipeline can degrade to rules-only.
// JevClient 调用 Jev SystemOne API；任何畸形或越界响应都视为错误，
// 让流水线降级为仅规则决策。
type JevClient struct {
	cfg    JevConfig
	client *http.Client
	logger *slog.Logger
}

// NewJevClient builds the client.
// NewJevClient 构建客户端。
func NewJevClient(cfg JevConfig, logger *slog.Logger) (*JevClient, error) {
	if cfg.Endpoint == "" || cfg.Model == "" || logger == nil {
		return nil, fmt.Errorf("intent: jev endpoint and model are required")
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = 3 * time.Second
	}
	return &JevClient{cfg: cfg, client: &http.Client{Timeout: cfg.Timeout}, logger: logger}, nil
}

// jevQuestion is one question entry of the SystemOne contract.
// jevQuestion 是 SystemOne 契约中的一个提问。
type jevQuestion struct {
	Type         string            `json:"type"`
	Instructions string            `json:"instructions"`
	Criteria     map[string]string `json:"criteria,omitempty"`
}

// defaultQuestions follows the documented question set for chat participation.
// defaultQuestions 遵循文档定义的聊天参与问题集。
func defaultQuestions() map[string]jevQuestion {
	return map[string]jevQuestion{
		"directed_to_bot":      {Type: "noul", Instructions: "当前发言是否明确是在向机器人说话？"},
		"continues_bot_thread": {Type: "noul", Instructions: "这是否是对机器人最近发言的自然追问？"},
		"addressed_to_other":   {Type: "noul", Instructions: "这是否主要是在和另一位真人群友说话？"},
		"appropriate_to_join":  {Type: "noul", Instructions: "在没有被点名的情况下，机器人此时插话是否自然且有帮助？"},
		"intent": {
			Type:         "choice",
			Instructions: "当前消息的主要聊天功能是什么？",
			Criteria: map[string]string{
				"question": "求助或提问",
				"chat":     "普通闲聊或讨论",
				"command":  "明确提出执行动作",
				"closing":  "告别、结束或不再期待回复",
				"other":    "无法归入以上类型",
			},
		},
	}
}

// Decide builds the state payload, calls the API and parses answers defensively.
// Decide 构建状态负载、调用 API 并防御性地解析回答。
func (c *JevClient) Decide(ctx context.Context, in domain.Input) (domain.SemanticAnswer, error) {
	payload := map[string]any{
		"model":     c.cfg.Model,
		"state":     c.buildState(in),
		"questions": defaultQuestions(),
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return domain.SemanticAnswer{}, fmt.Errorf("jev: encode request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.cfg.Endpoint, bytes.NewReader(body))
	if err != nil {
		return domain.SemanticAnswer{}, fmt.Errorf("jev: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if c.cfg.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.cfg.APIKey)
	}
	resp, err := c.client.Do(req)
	if err != nil {
		return domain.SemanticAnswer{}, fmt.Errorf("jev: call: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		// Read a bounded body slice for logging, never the full payload.
		// 仅读取有上限的内容用于日志，不记录完整载荷。
		snippet, _ := io.ReadAll(io.LimitReader(resp.Body, 256))
		c.logger.Warn("jev request failed", "status", resp.StatusCode, "body_snippet", string(snippet))
		return domain.SemanticAnswer{}, fmt.Errorf("jev: unexpected status %d", resp.StatusCode)
	}
	var parsed struct {
		Answers map[string]json.RawMessage `json:"answers"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&parsed); err != nil {
		return domain.SemanticAnswer{}, fmt.Errorf("jev: decode response: %w", err)
	}
	return parseAnswers(parsed.Answers)
}

// buildState maps the decision input into the documented state shape.
// buildState 将决策输入映射为文档定义的状态形态。
func (c *JevClient) buildState(in domain.Input) map[string]any {
	recent := make([]map[string]any, 0, len(in.Recent))
	for _, m := range in.Recent {
		sender := "bot"
		if !m.IsBot {
			sender = fmt.Sprintf("user_%d", m.SenderID)
		}
		recent = append(recent, map[string]any{"sender": sender, "text": m.Text})
	}
	sender := "bot"
	if !in.Task.Message.IsBot {
		sender = fmt.Sprintf("user_%d", in.Task.Message.SenderID)
	}
	botLastSpoke := in.BotLastSpoke()
	if botLastSpoke < 0 {
		botLastSpoke = -1
	}
	return map[string]any{
		"bot_name":                   c.cfg.BotName,
		"channel":                    in.Task.Kind,
		"current_message":            map[string]any{"sender": sender, "text": in.Task.Message.Text},
		"recent_messages":            recent,
		"bot_last_spoke_seconds_ago": botLastSpoke,
		"mentions_bot":               in.MentionsBot(),
		"replies_to_bot":             in.RepliesToBot(),
	}
}

// parseAnswers extracts noul scores and the choice category; missing, malformed
// or out-of-range values make the whole answer unusable.
// parseAnswers 提取 noul 分数与 choice 分类；缺失、畸形或越界的值使整个回答不可用。
func parseAnswers(answers map[string]json.RawMessage) (domain.SemanticAnswer, error) {
	out := domain.SemanticAnswer{}
	if len(answers) == 0 {
		return out, fmt.Errorf("jev: empty answers")
	}
	noul := func(name string) (float64, error) {
		raw, ok := answers[name]
		if !ok {
			return 0, fmt.Errorf("jev: answer %s missing", name)
		}
		var v struct {
			Noul *float64 `json:"noul"`
		}
		if err := json.Unmarshal(raw, &v); err != nil || v.Noul == nil {
			return 0, fmt.Errorf("jev: answer %s is not a noul value", name)
		}
		if math.IsNaN(*v.Noul) || *v.Noul < 0 || *v.Noul > 1 {
			return 0, fmt.Errorf("jev: answer %s out of range: %v", name, *v.Noul)
		}
		return *v.Noul, nil
	}
	var err error
	if out.DirectedToBot, err = noul("directed_to_bot"); err != nil {
		return out, err
	}
	if out.ContinuesBotThread, err = noul("continues_bot_thread"); err != nil {
		return out, err
	}
	if out.AddressedToOther, err = noul("addressed_to_other"); err != nil {
		return out, err
	}
	if out.AppropriateToJoin, err = noul("appropriate_to_join"); err != nil {
		return out, err
	}
	raw, ok := answers["intent"]
	if !ok {
		return out, fmt.Errorf("jev: answer intent missing")
	}
	var choice struct {
		Choice   string `json:"choice"`
		Label    string `json:"label"`
		Category string `json:"category"`
	}
	if err := json.Unmarshal(raw, &choice); err != nil {
		return out, fmt.Errorf("jev: answer intent is not a choice value")
	}
	out.IntentCategory = choice.Choice
	if out.IntentCategory == "" {
		out.IntentCategory = choice.Label
	}
	if out.IntentCategory == "" {
		out.IntentCategory = choice.Category
	}
	if out.IntentCategory == "" {
		// A choice answer with no resolvable category is a malformed response,
		// not a usable one.
		// choice 回答解析不出任何分类即视为畸形响应，而非可用回答。
		return out, fmt.Errorf("jev: answer intent has no category")
	}
	out.Available = true
	return out, nil
}
