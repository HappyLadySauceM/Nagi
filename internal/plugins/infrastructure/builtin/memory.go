// Built-in memory read tools. They are exposed only when the run's scope has
// allow_memory_read; original source text additionally requires
// allow_history_read and an exact stored source message in the same conversation.
// 内置记忆读取工具。仅当运行作用域具备 allow_memory_read 时暴露；
// 原始来源文本还要求 allow_history_read，并且来源消息必须位于同一会话。
package builtin

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	memorycontracts "github.com/HappyLadySauceM/Nagi/internal/memory/contracts"
	pluginsapp "github.com/HappyLadySauceM/Nagi/internal/plugins/application"
	"github.com/HappyLadySauceM/Nagi/internal/plugins/contracts"
)

// MemoryReader is the narrow memory-detail surface this provider needs.
// MemoryReader 是本提供方需要的窄记忆详情接口。
type MemoryReader interface {
	RecallDetail(ctx context.Context, req memorycontracts.DetailRequest) (memorycontracts.Memory, bool, error)
}

// SourceMessageReader returns one exact source message only for its own bot.
// SourceMessageReader 仅返回属于自身机器人的一条确切来源消息。
type SourceMessageReader interface {
	MessageSource(ctx context.Context, botID, messageID int64) (conversationID string, text string, found bool, err error)
}

type memoryProvider struct {
	memories MemoryReader
	sources  SourceMessageReader
}

// NewMemoryProvider builds the provider.
// NewMemoryProvider 构建提供方。
func NewMemoryProvider(memories MemoryReader, sources SourceMessageReader) contracts.ToolProvider {
	return memoryProvider{memories: memories, sources: sources}
}

// Manifest identifies the provider.
// Manifest 标识提供方。
func (memoryProvider) Manifest() contracts.Manifest {
	return contracts.Manifest{ID: "builtin.memory", Version: "1", Description: "当前会话可见的长期记忆详情与来源"}
}

// Tools returns the read-only memory detail and source tools.
// Tools 返回只读的记忆详情与来源工具。
func (p memoryProvider) Tools(context.Context) ([]contracts.RegisteredTool, error) {
	return []contracts.RegisteredTool{
		{
			Descriptor: contracts.ToolDescriptor{
				ID:          contracts.MemoryDetailToolID,
				Description: "读取一条当前会话可见的长期记忆详情和已存来源信息；参数 memory_id",
				Category:    contracts.CategoryMemory,
				RiskLevel:   contracts.RiskRead,
				ReadOnly:    true,
				Parameters: map[string]contracts.Parameter{
					"memory_id": {Type: "string", Description: "记忆 ID", Required: true},
				},
			},
			Invoke: p.detail,
		},
		{
			Descriptor: contracts.ToolDescriptor{
				ID:          contracts.MemorySourceToolID,
				Description: "读取一条长期记忆的原始来源文本；仅在同一会话且已授权历史读取时可用；参数 memory_id",
				Category:    contracts.CategoryMemory,
				RiskLevel:   contracts.RiskRead,
				ReadOnly:    true,
				Parameters: map[string]contracts.Parameter{
					"memory_id": {Type: "string", Description: "记忆 ID", Required: true},
				},
			},
			Invoke: p.source,
		},
	}, nil
}

// Close is a no-op for the built-in provider.
// Close 对内置提供方是空操作。
func (memoryProvider) Close(context.Context) error { return nil }

func (p memoryProvider) detail(ctx context.Context, argumentsInJSON string) (string, error) {
	scope, ok := pluginsapp.ScopeFrom(ctx)
	if !ok {
		return "", fmt.Errorf("memory tool: missing run scope")
	}
	if !scope.AllowMemoryRead {
		return "error: 未授权读取长期记忆", nil
	}
	item, found, err := p.lookup(ctx, scope, argumentsInJSON)
	if err != nil {
		return "", err
	}
	if !found {
		return "error: 记忆不可见或已失效", nil
	}
	out := map[string]any{
		"id":         item.ID,
		"content":    item.Content,
		"visibility": item.Visibility,
	}
	if !item.UpdatedAt.IsZero() {
		out["updated_at"] = item.UpdatedAt.UTC().Format("2006-01-02T15:04:05Z07:00")
	}
	if strings.TrimSpace(item.Source) != "" {
		out["source"] = item.Source
	}
	if item.SourceMessageID != 0 {
		out["source_message_id"] = item.SourceMessageID
	}
	payload, err := json.Marshal(out)
	if err != nil {
		return "", fmt.Errorf("memory tool: marshal detail: %w", err)
	}
	return string(payload), nil
}

func (p memoryProvider) source(ctx context.Context, argumentsInJSON string) (string, error) {
	scope, ok := pluginsapp.ScopeFrom(ctx)
	if !ok {
		return "", fmt.Errorf("memory tool: missing run scope")
	}
	if !scope.AllowMemoryRead {
		return "error: 未授权读取长期记忆", nil
	}
	if !scope.AllowHistoryRead {
		return "error: 未授权读取历史原文", nil
	}
	item, found, err := p.lookup(ctx, scope, argumentsInJSON)
	if err != nil {
		return "", err
	}
	if !found {
		return "error: 记忆不可见或已失效", nil
	}
	if item.SourceMessageID == 0 {
		return "error: 该记忆没有确切来源", nil
	}
	if p.sources == nil {
		return "error: 来源读取不可用", nil
	}
	conversationID, text, found, err := p.sources.MessageSource(ctx, scope.BotID, item.SourceMessageID)
	if err != nil {
		return "", fmt.Errorf("memory tool: read source: %w", err)
	}
	if !found {
		return "error: 来源原文不可用（可能已删除或超过留存期）", nil
	}
	if conversationID != scope.ConversationID {
		return "error: 来源原文不在当前会话的历史读取范围", nil
	}
	return text, nil
}

func (p memoryProvider) lookup(ctx context.Context, scope pluginsapp.Scope, argumentsInJSON string) (memorycontracts.Memory, bool, error) {
	if p.memories == nil {
		return memorycontracts.Memory{}, false, nil
	}
	var args struct {
		MemoryID string `json:"memory_id"`
	}
	if err := json.Unmarshal([]byte(argumentsInJSON), &args); err != nil {
		return memorycontracts.Memory{}, false, fmt.Errorf("memory tool: invalid arguments: %w", err)
	}
	args.MemoryID = strings.TrimSpace(args.MemoryID)
	if args.MemoryID == "" {
		return memorycontracts.Memory{}, false, fmt.Errorf("memory tool: memory_id is required")
	}
	return p.memories.RecallDetail(ctx, memorycontracts.DetailRequest{
		BotID: scope.BotID, Kind: scope.Kind, TargetID: scope.TargetID,
		ConversationID: scope.ConversationID, MemoryID: args.MemoryID,
	})
}
