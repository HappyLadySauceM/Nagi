// Integration test for the memory tools against real PostgreSQL.
// 记忆工具针对真实 PostgreSQL 的集成测试。
package builtin

import (
	"context"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	conversationapp "github.com/HappyLadySauceM/Nagi/internal/conversation/application"
	conversationdomain "github.com/HappyLadySauceM/Nagi/internal/conversation/domain"
	conversationinfra "github.com/HappyLadySauceM/Nagi/internal/conversation/infrastructure"
	memoryapp "github.com/HappyLadySauceM/Nagi/internal/memory/application"
	memorydomain "github.com/HappyLadySauceM/Nagi/internal/memory/domain"
	memoryinfra "github.com/HappyLadySauceM/Nagi/internal/memory/infrastructure"
	pluginsapp "github.com/HappyLadySauceM/Nagi/internal/plugins/application"
	plugincontracts "github.com/HappyLadySauceM/Nagi/internal/plugins/contracts"
	"github.com/HappyLadySauceM/Nagi/pkg/db/dbtest"
)

type conversationSourceTestAdapter struct{ svc *conversationapp.Service }

func (a conversationSourceTestAdapter) MessageSource(ctx context.Context, botID, messageID int64) (string, string, bool, error) {
	message, found, err := a.svc.MessageSource(ctx, botID, messageID)
	return message.ConversationID, message.Text, found, err
}

// TestMemoryToolsReadStoredSourceWithRealDatabase verifies memory detail and
// source reads through the actual persistence layer, including the separate
// history grant and cross-conversation source binding.
// TestMemoryToolsReadStoredSourceWithRealDatabase 验证记忆详情与来源读取经过真实
// 持久化层，包括独立历史授权与跨会话来源约束。
func TestMemoryToolsReadStoredSourceWithRealDatabase(t *testing.T) {
	pool := dbtest.Open(t)
	ctx := context.Background()
	conversationRepo, err := conversationinfra.NewRepo(pool)
	if err != nil {
		t.Fatalf("conversation repo: %v", err)
	}
	memoryRepo, err := memoryinfra.NewRepo(pool)
	if err != nil {
		t.Fatalf("memory repo: %v", err)
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	conversationService, err := conversationapp.NewService(conversationRepo, logger)
	if err != nil {
		t.Fatalf("conversation service: %v", err)
	}
	memoryService, err := memoryapp.NewService(memoryRepo)
	if err != nil {
		t.Fatalf("memory service: %v", err)
	}

	const botID, groupID = 995001, 995002
	const otherGroupID = 995003
	base := time.Now().UnixNano()
	ingested, err := conversationService.Ingest(ctx, conversationdomain.IngestInput{
		BotID: botID, Kind: conversationdomain.KindGroup, TargetID: groupID,
		PlatformMessageID: base, SenderID: 995004, Text: "这是记忆的原始来源消息",
	})
	if err != nil {
		t.Fatalf("ingest: %v", err)
	}
	memoryID, err := memoryRepo.Insert(ctx, memorydomain.Memory{
		BotID: botID, Visibility: memorydomain.VisibilityBotShared,
		Content: "群友喜欢晚上聊天", Source: "群聊来源", SourceMessageID: ingested.MessageID,
		Status: memorydomain.StatusActive, Confidence: 0.9,
	})
	if err != nil {
		t.Fatalf("insert memory: %v", err)
	}
	if _, err := pool.Exec(ctx, `UPDATE messages SET created_at = NOW() WHERE id = $1`, ingested.MessageID); err != nil {
		t.Fatalf("age source message: %v", err)
	}

	provider := NewMemoryProvider(memoryService, conversationSourceTestAdapter{svc: conversationService})
	tools, err := provider.Tools(ctx)
	if err != nil {
		t.Fatalf("tools: %v", err)
	}
	var detail, source plugincontracts.RegisteredTool
	for _, registered := range tools {
		switch registered.Descriptor.ID {
		case "memory.detail":
			detail = registered
		case "memory.source":
			source = registered
		}
	}
	if detail.Invoke == nil || source.Invoke == nil {
		t.Fatalf("memory tools missing")
	}
	detailCtx := pluginsapp.WithScope(ctx, pluginsapp.Scope{
		BotID: botID, Kind: "group", TargetID: groupID, ConversationID: ingested.ConversationID,
		AllowMemoryRead: true, AllowHistoryRead: false,
	})
	detailOut, err := detail.Invoke(detailCtx, `{"memory_id":"`+memoryID+`"}`)
	if err != nil || !strings.Contains(detailOut, "群友喜欢晚上聊天") || !strings.Contains(detailOut, "群聊来源") {
		t.Fatalf("detail = %q, %v", detailOut, err)
	}

	sourceCtx := pluginsapp.WithScope(ctx, pluginsapp.Scope{
		BotID: botID, Kind: "group", TargetID: groupID, ConversationID: ingested.ConversationID,
		AllowMemoryRead: true, AllowHistoryRead: true,
	})
	sourceOut, err := source.Invoke(sourceCtx, `{"memory_id":"`+memoryID+`"}`)
	if err != nil || sourceOut != "这是记忆的原始来源消息" {
		t.Fatalf("source = %q, %v", sourceOut, err)
	}

	noHistoryCtx := pluginsapp.WithScope(ctx, pluginsapp.Scope{
		BotID: botID, Kind: "group", TargetID: groupID, ConversationID: ingested.ConversationID,
		AllowMemoryRead: true, AllowHistoryRead: false,
	})
	if out, err := source.Invoke(noHistoryCtx, `{"memory_id":"`+memoryID+`"}`); err != nil || !strings.Contains(out, "未授权读取历史原文") {
		t.Fatalf("source without history = %q, %v", out, err)
	}

	otherGroupCtx := pluginsapp.WithScope(ctx, pluginsapp.Scope{
		BotID: botID, Kind: "group", TargetID: otherGroupID, ConversationID: "other-conversation",
		AllowMemoryRead: true, AllowHistoryRead: true,
	})
	if out, err := source.Invoke(otherGroupCtx, `{"memory_id":"`+memoryID+`"}`); err != nil || !strings.Contains(out, "不在当前会话") {
		t.Fatalf("cross-conversation source = %q, %v", out, err)
	}
}
