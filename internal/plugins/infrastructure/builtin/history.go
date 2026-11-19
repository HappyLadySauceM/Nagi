package builtin

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	conversationapp "github.com/HappyLadySauceM/Nagi/internal/conversation/application"
	conversation "github.com/HappyLadySauceM/Nagi/internal/conversation/domain"
	pluginsapp "github.com/HappyLadySauceM/Nagi/internal/plugins/application"
	"github.com/HappyLadySauceM/Nagi/internal/plugins/contracts"
)

type historyProvider struct {
	search *conversationapp.HistorySearch
}

// NewHistoryProvider exposes search for the current authorized conversation.
// NewHistoryProvider 暴露当前已授权会话的历史检索。
func NewHistoryProvider(search *conversationapp.HistorySearch) contracts.ToolProvider {
	return historyProvider{search: search}
}

func (historyProvider) Manifest() contracts.Manifest {
	return contracts.Manifest{ID: "builtin.history", Version: "1", Description: "当前会话历史检索"}
}

func (p historyProvider) Tools(context.Context) ([]contracts.RegisteredTool, error) {
	descriptor := contracts.ToolDescriptor{
		ID: contracts.HistorySearchToolID, Permission: "history.search",
		Description: "在当前会话中搜索过去的聊天。使用简短话题或关键词，返回少量消息摘录和来源 ID。用于当前上下文缺少的早期讨论。",
		Category:    contracts.CategoryMemory, RiskLevel: contracts.RiskRead, ReadOnly: true,
		Parameters: map[string]contracts.Parameter{
			"query":     {Type: "string", Description: "话题或关键词，最多 160 字", Required: true},
			"sender_id": {Type: "string", Description: "可选发送人 QQ 号"},
			"since":     {Type: "string", Description: "可选起始时间，RFC3339 格式"},
		},
	}
	return []contracts.RegisteredTool{{
		Descriptor: descriptor,
		Invoke: func(ctx context.Context, args string) (string, error) {
			return p.invoke(ctx, args, descriptor)
		},
	}}, nil
}

func (historyProvider) Close(context.Context) error { return nil }

func (p historyProvider) invoke(ctx context.Context, arguments string, descriptor contracts.ToolDescriptor) (string, error) {
	scope, ok := pluginsapp.ScopeFrom(ctx)
	if !ok || !scope.AllowHistoryRead || scope.AuthorizeHistory == nil || scope.Reauthorize == nil {
		return "", fmt.Errorf("history search is not authorized")
	}
	query, err := historyArguments(arguments)
	if err != nil {
		return "", err
	}
	if err := scope.AuthorizeHistory(ctx); err != nil {
		return "", err
	}
	messages, err := p.search.Search(ctx, scope.BotID, scope.TargetID, scope.JobID, conversation.Kind(scope.Kind), query)
	if err != nil {
		return "", err
	}
	if err := scope.Reauthorize(ctx, descriptor); err != nil {
		return "", err
	}
	type hit struct {
		MessageID string    `json:"message_id"`
		SenderID  string    `json:"sender_id"`
		CreatedAt time.Time `json:"created_at"`
		Text      string    `json:"text"`
	}
	hits := make([]hit, 0, len(messages))
	for _, message := range messages {
		text := conversationapp.SearchableMessageText(message)
		if text == "" {
			continue
		}
		hits = append(hits, hit{
			MessageID: strconv.FormatInt(message.ID, 10),
			SenderID:  strconv.FormatInt(message.SenderID, 10),
			CreatedAt: message.CreatedAt, Text: boundToolText(text, 220),
		})
	}
	body, err := json.Marshal(hits)
	return string(body), err
}

func historyArguments(arguments string) (conversation.HistorySearch, error) {
	var input struct {
		Query    string `json:"query"`
		SenderID string `json:"sender_id"`
		Since    string `json:"since"`
	}
	if err := json.Unmarshal([]byte(arguments), &input); err != nil {
		return conversation.HistorySearch{}, fmt.Errorf("历史查询参数无效")
	}
	query := conversation.HistorySearch{Query: strings.TrimSpace(input.Query), Limit: 5}
	if input.SenderID != "" {
		id, err := strconv.ParseInt(input.SenderID, 10, 64)
		if err != nil || id <= 0 {
			return query, fmt.Errorf("发送人 QQ 号无效")
		}
		query.SenderID = id
	}
	if input.Since != "" {
		since, err := time.Parse(time.RFC3339, input.Since)
		if err != nil {
			return query, fmt.Errorf("历史查询起始时间无效")
		}
		query.After = since
	}
	return query, nil
}
