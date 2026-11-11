// Tests for the memory ID exposed to the model and consumed by the real
// memory provider tools.
// 注入模型的记忆 ID 与真实记忆工具消费链路的测试。
package application

import (
	"context"
	"strings"
	"testing"
	"time"

	memorycontracts "github.com/HappyLadySauceM/Nagi/internal/memory/contracts"
	pluginsapp "github.com/HappyLadySauceM/Nagi/internal/plugins/application"
	plugincontracts "github.com/HappyLadySauceM/Nagi/internal/plugins/contracts"
	builtin "github.com/HappyLadySauceM/Nagi/internal/plugins/infrastructure/builtin"
	einoinfra "github.com/HappyLadySauceM/Nagi/internal/plugins/infrastructure/eino"
)

type replierMemoryDetailReader struct{ item memorycontracts.Memory }

func (r replierMemoryDetailReader) RecallDetail(context.Context, memorycontracts.DetailRequest) (memorycontracts.Memory, bool, error) {
	return r.item, true, nil
}

type replierMemorySourceReader struct {
	conversationID string
	text           string
}

func (r replierMemorySourceReader) MessageSource(context.Context, int64, int64) (string, string, bool, error) {
	return r.conversationID, r.text, true, nil
}

func extractMemoryID(t *testing.T, prompt string) string {
	t.Helper()
	const prefix = "记忆ID="
	start := strings.Index(prompt, prefix)
	if start < 0 {
		t.Fatalf("memory id missing from prompt: %q", prompt)
	}
	rest := prompt[start+len(prefix):]
	end := strings.IndexAny(rest, "|\n")
	if end < 0 {
		t.Fatalf("memory id has no terminator: %q", rest)
	}
	return rest[:end]
}

// TestReplierInjectedIDReachesActualProvider verifies an ID extracted from the
// actual runtime prompt can be passed to the real memory detail/source tools.
// TestReplierInjectedIDReachesActualProvider 验证从实际运行请求的提示中提取的 ID
// 可直接传给真实记忆详情/来源工具并成功读取。
func TestReplierInjectedIDReachesActualProvider(t *testing.T) {
	const memoryID = "00000000-0000-4000-8000-000000000123"
	item := memorycontracts.Memory{
		ID: memoryID, Content: "项目部署使用 Docker", Visibility: "group",
		Source: "群文件", SourceMessageID: 42,
		UpdatedAt: time.Date(2026, 11, 10, 8, 0, 0, 0, time.UTC),
	}
	fake := &fakeRuntime{text: "好的"}
	r := newReplier(t, fake, &fakeSender{}, &fakeRecorder{}, memoryReadGroupPolicy())
	r.memory = &fakeMemoryReader{items: []memorycontracts.Memory{item}}
	if err := r.ExecuteReply(context.Background(), groupTaskForAgent(), nil); err != nil {
		t.Fatalf("execute: %v", err)
	}
	system := fake.req.Messages[0].Content
	if got := extractMemoryID(t, system); got != memoryID {
		t.Fatalf("injected memory id = %q, want %q", got, memoryID)
	}
	if !strings.Contains(system, "memory_detail") || !strings.Contains(system, "memory_source") {
		t.Fatalf("on-demand tool guidance missing: %q", system)
	}

	provider := builtin.NewMemoryProvider(
		replierMemoryDetailReader{item: item},
		replierMemorySourceReader{conversationID: "conv-1", text: "原始来源"},
	)
	tools, err := provider.Tools(context.Background())
	if err != nil {
		t.Fatalf("tools: %v", err)
	}
	// The model-facing Eino names are the underscore form; dotted descriptor
	// IDs are only the registry identity.
	// 模型侧 Eino 名称是下划线形式；点号 descriptor ID 只是注册表标识。
	modelToolNames := map[string]bool{}
	for _, base := range einoinfra.ToBaseTools(tools) {
		info, infoErr := base.Info(context.Background())
		if infoErr != nil {
			t.Fatalf("tool info: %v", infoErr)
		}
		modelToolNames[info.Name] = true
	}
	if !modelToolNames["memory_detail"] || !modelToolNames["memory_source"] {
		t.Fatalf("model tool names = %v, want memory_detail/memory_source", modelToolNames)
	}
	var detail, source plugincontracts.RegisteredTool
	for _, candidate := range tools {
		switch candidate.Descriptor.ID {
		case "memory.detail":
			detail = candidate
		case "memory.source":
			source = candidate
		}
	}
	if detail.Invoke == nil || source.Invoke == nil {
		t.Fatalf("memory tools missing")
	}
	scope := pluginsapp.WithScope(context.Background(), pluginsapp.Scope{
		BotID: 100, Kind: "group", TargetID: 700, ConversationID: "conv-1",
		AllowMemoryRead: true, AllowHistoryRead: true,
	})
	detailOut, err := detail.Invoke(scope, `{"memory_id":"`+memoryID+`"}`)
	if err != nil || !strings.Contains(detailOut, "项目部署使用 Docker") {
		t.Fatalf("detail with extracted id = %q, %v", detailOut, err)
	}
	sourceOut, err := source.Invoke(scope, `{"memory_id":"`+memoryID+`"}`)
	if err != nil || sourceOut != "原始来源" {
		t.Fatalf("source with extracted id = %q, %v", sourceOut, err)
	}
}

// TestBoundMemoryLineKeepsFullIDUnderSourceBudget verifies a long body and
// source cannot truncate the memory ID, while the rendered line stays bounded.
// TestBoundMemoryLineKeepsFullIDUnderSourceBudget 验证长正文与来源不会截断记忆 ID，
// 且整行仍在预算内。
func TestBoundMemoryLineKeepsFullIDUnderSourceBudget(t *testing.T) {
	const memoryID = "00000000-0000-4000-8000-000000000789"
	item := memorycontracts.Memory{
		ID: memoryID, Content: strings.Repeat("正文", maxMemoryRunes),
		Visibility: "group", Source: strings.Repeat("来源", 1000), SourceMessageID: 42,
	}
	bounded := boundMemories([]memorycontracts.Memory{item})
	if len(bounded) != 1 || bounded[0].ID != memoryID {
		t.Fatalf("bounded = %+v", bounded)
	}
	line := memoryLine(bounded[0])
	if !strings.Contains(line, "记忆ID="+memoryID) {
		t.Fatalf("full memory id missing after budget trim: %q", line)
	}
	if runes := len([]rune(line)); runes > maxMemoryRunes {
		t.Fatalf("line runes = %d, want <= %d", runes, maxMemoryRunes)
	}
}

// TestBoundMemoriesOmitsEntryWhenMetadataCannotFit verifies the budget drops
// an entire entry instead of ever emitting a truncated memory ID.
// TestBoundMemoriesOmitsEntryWhenMetadataCannotFit 验证预算不足时整条省略，
// 绝不输出截断的记忆 ID。
func TestBoundMemoriesOmitsEntryWhenMetadataCannotFit(t *testing.T) {
	const memoryID = "00000000-0000-4000-8000-000000000790"
	item := memorycontracts.Memory{
		ID: memoryID, Content: "正文", Visibility: strings.Repeat("可", maxMemoryRunes),
	}
	if bounded := boundMemories([]memorycontracts.Memory{item}); len(bounded) != 0 {
		t.Fatalf("over-budget entry was emitted: %+v", bounded)
	}
}
