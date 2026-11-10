// Tests for the built-in memory detail/source tools.
// 内置记忆详情/来源工具测试。
package builtin

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	memorycontracts "github.com/HappyLadySauceM/Nagi/internal/memory/contracts"
	pluginsapp "github.com/HappyLadySauceM/Nagi/internal/plugins/application"
	"github.com/HappyLadySauceM/Nagi/internal/plugins/contracts"
)

type fakeMemoryDetailReader struct {
	item  memorycontracts.Memory
	found bool
	err   error
	req   memorycontracts.DetailRequest
}

func (f *fakeMemoryDetailReader) RecallDetail(_ context.Context, req memorycontracts.DetailRequest) (memorycontracts.Memory, bool, error) {
	f.req = req
	return f.item, f.found, f.err
}

type fakeSourceMessageReader struct {
	conversationID string
	text           string
	found          bool
	err            error
	botID          int64
	messageID      int64
}

func (f *fakeSourceMessageReader) MessageSource(_ context.Context, botID, messageID int64) (string, string, bool, error) {
	f.botID = botID
	f.messageID = messageID
	return f.conversationID, f.text, f.found, f.err
}

func memoryTools(t *testing.T) map[string]contracts.RegisteredTool {
	t.Helper()
	provider := NewMemoryProvider(&fakeMemoryDetailReader{}, &fakeSourceMessageReader{})
	tools, err := provider.Tools(context.Background())
	if err != nil {
		t.Fatalf("tools: %v", err)
	}
	out := make(map[string]contracts.RegisteredTool, len(tools))
	for _, tool := range tools {
		out[tool.Descriptor.ID] = tool
	}
	return out
}

func memoryScope() pluginsapp.Scope {
	return pluginsapp.Scope{
		BotID: 1, Kind: "group", TargetID: 700, ConversationID: "conv-1",
		AllowMemoryRead: true, AllowHistoryRead: true,
	}
}

// TestMemoryDetailReturnsStoredSourceMetadata verifies the detail tool returns
// the memory body and only stored source metadata, never an invented message.
// TestMemoryDetailReturnsStoredSourceMetadata 验证详情工具只返回记忆正文与已存来源元数据，
// 不编造消息。
func TestMemoryDetailReturnsStoredSourceMetadata(t *testing.T) {
	reader := &fakeMemoryDetailReader{
		found: true,
		item: memorycontracts.Memory{
			ID: "m1", Content: "群友喜欢晚上聊天", Visibility: "bot_shared",
			Source: "群聊 700", SourceMessageID: 9001,
			UpdatedAt: time.Date(2026, 11, 10, 8, 0, 0, 0, time.UTC),
		},
	}
	provider := NewMemoryProvider(reader, &fakeSourceMessageReader{})
	tools, _ := provider.Tools(context.Background())
	var detail contracts.RegisteredTool
	for _, candidate := range tools {
		if candidate.Descriptor.ID == "memory.detail" {
			detail = candidate
		}
	}
	ctx := pluginsapp.WithScope(context.Background(), memoryScope())
	out, err := detail.Invoke(ctx, `{"memory_id":"m1"}`)
	if err != nil {
		t.Fatalf("invoke: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("decode output: %v", err)
	}
	if got["id"] != "m1" || got["content"] != "群友喜欢晚上聊天" || got["source"] != "群聊 700" || got["source_message_id"] != float64(9001) {
		t.Fatalf("detail output = %#v", got)
	}
	if reader.req.BotID != 1 || reader.req.Kind != "group" || reader.req.TargetID != 700 || reader.req.ConversationID != "conv-1" {
		t.Fatalf("detail request = %+v", reader.req)
	}
}

// TestMemoryDetailDeniedWithoutMemoryRead verifies the separate memory grant is
// enforced before the detail reader is called.
// TestMemoryDetailDeniedWithoutMemoryRead 验证独立记忆授权在调用详情读取前被强制检查。
func TestMemoryDetailDeniedWithoutMemoryRead(t *testing.T) {
	provider := NewMemoryProvider(&fakeMemoryDetailReader{}, &fakeSourceMessageReader{})
	tools, _ := provider.Tools(context.Background())
	for _, tool := range tools {
		if tool.Descriptor.ID == "memory.detail" {
			ctx := pluginsapp.WithScope(context.Background(), pluginsapp.Scope{BotID: 1, Kind: "group", TargetID: 700})
			if out, err := tool.Invoke(ctx, `{"memory_id":"m1"}`); err != nil || !strings.Contains(out, "未授权") {
				t.Fatalf("denied detail = %q, %v", out, err)
			}
		}
	}
}

// TestMemorySourceRequiresHistoryRead verifies source text is gated by the
// independent allow_history_read grant even when memory read is allowed.
// TestMemorySourceRequiresHistoryRead 验证即使允许记忆读取，原文仍受独立的
// allow_history_read 授权约束。
func TestMemorySourceRequiresHistoryRead(t *testing.T) {
	provider := NewMemoryProvider(&fakeMemoryDetailReader{found: true, item: memorycontracts.Memory{ID: "m1", SourceMessageID: 9001}}, &fakeSourceMessageReader{})
	tools, _ := provider.Tools(context.Background())
	for _, tool := range tools {
		if tool.Descriptor.ID == "memory.source" {
			ctx := pluginsapp.WithScope(context.Background(), pluginsapp.Scope{BotID: 1, Kind: "group", TargetID: 700, AllowMemoryRead: true, AllowHistoryRead: false})
			out, err := tool.Invoke(ctx, `{"memory_id":"m1"}`)
			if err != nil || !strings.Contains(out, "未授权读取历史原文") {
				t.Fatalf("source without history = %q, %v", out, err)
			}
		}
	}
}

// TestMemorySourceReturnsExactStoredMessage verifies source text is returned
// only when the exact message exists in the same conversation.
// TestMemorySourceReturnsExactStoredMessage 验证仅当确切消息存在于同一会话时返回原文。
func TestMemorySourceReturnsExactStoredMessage(t *testing.T) {
	source := &fakeSourceMessageReader{conversationID: "conv-1", text: "原始消息", found: true}
	provider := NewMemoryProvider(&fakeMemoryDetailReader{found: true, item: memorycontracts.Memory{ID: "m1", SourceMessageID: 9001}}, source)
	tools, _ := provider.Tools(context.Background())
	for _, tool := range tools {
		if tool.Descriptor.ID == "memory.source" {
			ctx := pluginsapp.WithScope(context.Background(), memoryScope())
			out, err := tool.Invoke(ctx, `{"memory_id":"m1"}`)
			if err != nil || out != "原始消息" {
				t.Fatalf("source = %q, %v", out, err)
			}
			if source.botID != 1 || source.messageID != 9001 {
				t.Fatalf("source request = %d/%d", source.botID, source.messageID)
			}
		}
	}
}

// TestMemorySourceUnavailableWhenMessagePurged verifies retention deletion is
// reported as unavailable instead of fabricating an old message.
// TestMemorySourceUnavailableWhenMessagePurged 验证留存删除后报告不可用，而不是编造旧消息。
func TestMemorySourceUnavailableWhenMessagePurged(t *testing.T) {
	provider := NewMemoryProvider(&fakeMemoryDetailReader{found: true, item: memorycontracts.Memory{ID: "m1", SourceMessageID: 9001}}, &fakeSourceMessageReader{found: false})
	tools, _ := provider.Tools(context.Background())
	for _, tool := range tools {
		if tool.Descriptor.ID == "memory.source" {
			ctx := pluginsapp.WithScope(context.Background(), memoryScope())
			out, err := tool.Invoke(ctx, `{"memory_id":"m1"}`)
			if err != nil || !strings.Contains(out, "不可用") {
				t.Fatalf("purged source = %q, %v", out, err)
			}
		}
	}
}

// TestMemorySourceRejectsCrossConversation verifies a bot-shared memory's
// source from another conversation is not exposed in the current one.
// TestMemorySourceRejectsCrossConversation 验证其他会话来源的共享记忆不在当前会话暴露原文。
func TestMemorySourceRejectsCrossConversation(t *testing.T) {
	source := &fakeSourceMessageReader{conversationID: "other-conv", text: "其他会话原文", found: true}
	provider := NewMemoryProvider(&fakeMemoryDetailReader{found: true, item: memorycontracts.Memory{ID: "m1", SourceMessageID: 9001}}, source)
	tools, _ := provider.Tools(context.Background())
	for _, tool := range tools {
		if tool.Descriptor.ID == "memory.source" {
			ctx := pluginsapp.WithScope(context.Background(), memoryScope())
			out, err := tool.Invoke(ctx, `{"memory_id":"m1"}`)
			if err != nil || !strings.Contains(out, "不在当前会话") {
				t.Fatalf("cross-conversation source = %q, %v", out, err)
			}
		}
	}
}

// TestMemorySourceWithoutStoredIDIsUnavailable verifies no source is fabricated
// when the memory has no exact source id.
// TestMemorySourceWithoutStoredIDIsUnavailable 验证记忆没有确切来源 ID 时不编造来源。
func TestMemorySourceWithoutStoredIDIsUnavailable(t *testing.T) {
	provider := NewMemoryProvider(&fakeMemoryDetailReader{found: true, item: memorycontracts.Memory{ID: "m1"}}, &fakeSourceMessageReader{found: true, text: "不应返回"})
	tools, _ := provider.Tools(context.Background())
	for _, tool := range tools {
		if tool.Descriptor.ID == "memory.source" {
			ctx := pluginsapp.WithScope(context.Background(), memoryScope())
			out, err := tool.Invoke(ctx, `{"memory_id":"m1"}`)
			if err != nil || !strings.Contains(out, "没有确切来源") {
				t.Fatalf("missing source = %q, %v", out, err)
			}
		}
	}
}
