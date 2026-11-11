// Integration test for the built-in memory provider through the real tool
// resolver, guard and scope reauthorization path.
// 内置记忆提供方经过真实工具解析、守卫与作用域重新授权的集成测试。
package assembly

import (
	"context"
	"strings"
	"testing"

	"github.com/cloudwego/eino/components/tool"

	agentcontracts "github.com/HappyLadySauceM/Nagi/internal/agent/contracts"
	memorycontracts "github.com/HappyLadySauceM/Nagi/internal/memory/contracts"
	pluginsapp "github.com/HappyLadySauceM/Nagi/internal/plugins/application"
	plugincontracts "github.com/HappyLadySauceM/Nagi/internal/plugins/contracts"
	builtin "github.com/HappyLadySauceM/Nagi/internal/plugins/infrastructure/builtin"
	scopeapp "github.com/HappyLadySauceM/Nagi/internal/scope/application"
	scopedomain "github.com/HappyLadySauceM/Nagi/internal/scope/domain"
	"github.com/HappyLadySauceM/Nagi/pkg/config"
)

type assemblyMemoryDetailReader struct {
	item  memorycontracts.Memory
	found bool
}

func (f assemblyMemoryDetailReader) RecallDetail(context.Context, memorycontracts.DetailRequest) (memorycontracts.Memory, bool, error) {
	return f.item, f.found, nil
}

type assemblyMemorySourceReader struct {
	conversationID string
	text           string
	found          bool
}

func (f assemblyMemorySourceReader) MessageSource(context.Context, int64, int64) (string, string, bool, error) {
	return f.conversationID, f.text, f.found, nil
}

func invokableMemoryTool(t *testing.T, ctx context.Context, tools []tool.BaseTool, name string) tool.InvokableTool {
	t.Helper()
	for _, base := range tools {
		info, err := base.Info(ctx)
		if err != nil {
			t.Fatalf("tool info: %v", err)
		}
		if info.Name != name {
			continue
		}
		invokable, ok := base.(tool.InvokableTool)
		if !ok {
			t.Fatalf("tool %s is not invokable: %T", name, base)
		}
		return invokable
	}
	t.Fatalf("tool %s not exposed", name)
	return nil
}

// TestBuiltinMemoryProviderThroughResolverGuard verifies the real provider,
// resolver, guard and scope service together enforce memory/history grants at
// invocation time. It covers successful detail/source, memory-read revocation
// blocking both tools, and history-only revocation blocking only source.
// TestBuiltinMemoryProviderThroughResolverGuard 验证真实提供方、解析器、守卫和
// 作用域服务共同在调用时执行记忆/历史授权：成功详情与来源、撤销记忆读取后两者
// 都拒绝、仅撤销历史读取后来源拒绝而详情元数据仍允许。
func TestBuiltinMemoryProviderThroughResolverGuard(t *testing.T) {
	ctx := context.Background()
	registry := pluginsapp.NewRegistry()
	provider := builtin.NewMemoryProvider(
		assemblyMemoryDetailReader{
			found: true,
			item: memorycontracts.Memory{
				ID: "00000000-0000-4000-8000-000000000001", Content: "群友喜欢晚上聊天",
				Visibility: "group", Source: "群聊来源", SourceMessageID: 9001,
			},
		},
		assemblyMemorySourceReader{conversationID: "conv-1", text: "原始来源", found: true},
	)
	if err := registry.Register(ctx, provider); err != nil {
		t.Fatalf("register provider: %v", err)
	}
	t.Cleanup(func() { _ = registry.Close(context.Background()) })

	repo := &mutableScopeRepo{policy: scopedomain.Policy{
		ScopeType: scopedomain.ScopeGroup, ScopeID: 700, Enabled: true,
		ReplyMode: scopedomain.ModeIntentBased, AllowMemoryRead: true, AllowHistoryRead: true,
	}}
	scopeService, err := scopeapp.NewService(repo)
	if err != nil {
		t.Fatalf("new scope service: %v", err)
	}
	resolver := buildToolsResolver(settingsForTest(&config.Config{}), registry, scopeService)
	tools := resolver(ctx, agentcontracts.Request{
		BotID: 1, Kind: plugincontracts.ScopeKindGroup, TargetID: 700, ConversationID: "conv-1",
		AllowMemoryRead: true, AllowHistoryRead: true,
	})
	detail := invokableMemoryTool(t, ctx, tools, "memory_detail")
	source := invokableMemoryTool(t, ctx, tools, "memory_source")

	if out, err := detail.InvokableRun(ctx, `{"memory_id":"00000000-0000-4000-8000-000000000001"}`); err != nil || !strings.Contains(out, "群友喜欢晚上聊天") {
		t.Fatalf("detail success = %q, %v", out, err)
	}
	if out, err := source.InvokableRun(ctx, `{"memory_id":"00000000-0000-4000-8000-000000000001"}`); err != nil || out != "原始来源" {
		t.Fatalf("source success = %q, %v", out, err)
	}

	repo.policy.AllowMemoryRead = false
	for name, invokable := range map[string]tool.InvokableTool{"detail": detail, "source": source} {
		out, err := invokable.InvokableRun(ctx, `{"memory_id":"00000000-0000-4000-8000-000000000001"}`)
		if err != nil || !strings.Contains(out, "authorization denied") {
			t.Fatalf("%s after memory revocation = %q, %v", name, out, err)
		}
	}

	repo.policy.AllowMemoryRead = true
	repo.policy.AllowHistoryRead = false
	if out, err := detail.InvokableRun(ctx, `{"memory_id":"00000000-0000-4000-8000-000000000001"}`); err != nil || !strings.Contains(out, "群友喜欢晚上聊天") {
		t.Fatalf("detail after history revocation = %q, %v", out, err)
	}
	if out, err := source.InvokableRun(ctx, `{"memory_id":"00000000-0000-4000-8000-000000000001"}`); err != nil || !strings.Contains(out, "authorization denied") {
		t.Fatalf("source after history revocation = %q, %v", out, err)
	}
}
