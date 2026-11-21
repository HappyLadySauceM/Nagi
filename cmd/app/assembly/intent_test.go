// Tests for the tool resolver wiring: scope and permission filtering, call
// budget and run reporting happen in one place, so the assembly is worth
// testing directly.
// 工具解析装配的测试：会话类型与权限过滤、调用预算与运行上报集中在一处，
// 因此直接测试装配层。
package assembly

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/cloudwego/eino/components/tool"

	"github.com/HappyLadySauceM/Nagi/cmd/app/svc"
	agentcontracts "github.com/HappyLadySauceM/Nagi/internal/agent/contracts"
	conversationdomain "github.com/HappyLadySauceM/Nagi/internal/conversation/domain"
	intentapp "github.com/HappyLadySauceM/Nagi/internal/intent/application"
	intentdomain "github.com/HappyLadySauceM/Nagi/internal/intent/domain"
	models "github.com/HappyLadySauceM/Nagi/internal/models/contracts"
	obapp "github.com/HappyLadySauceM/Nagi/internal/onebot/application"
	obdomain "github.com/HappyLadySauceM/Nagi/internal/onebot/domain"
	pluginsapp "github.com/HappyLadySauceM/Nagi/internal/plugins/application"
	plugincontracts "github.com/HappyLadySauceM/Nagi/internal/plugins/contracts"
	scopeapp "github.com/HappyLadySauceM/Nagi/internal/scope/application"
	scopedomain "github.com/HappyLadySauceM/Nagi/internal/scope/domain"
	settingsapp "github.com/HappyLadySauceM/Nagi/internal/settings/application"
	settingsdomain "github.com/HappyLadySauceM/Nagi/internal/settings/domain"
	"github.com/HappyLadySauceM/Nagi/pkg/config"
)

// fakeProvider exposes canned tools without touching any external system.
// fakeProvider 暴露预置工具，不接触任何外部系统。
type fakeProvider struct {
	tools []plugincontracts.RegisteredTool
}

func (fakeProvider) Manifest() plugincontracts.Manifest {
	return plugincontracts.Manifest{ID: "test.fake", Version: "1"}
}

func (p fakeProvider) Tools(context.Context) ([]plugincontracts.RegisteredTool, error) {
	return p.tools, nil
}

func (fakeProvider) Close(context.Context) error { return nil }

// TestBuildToolsResolverFiltersAndBudgets verifies that a revoked permission
// hides its tool, that an allowed tool reports its run, and that the per-run
// budget stops further calls.
// TestBuildToolsResolverFiltersAndBudgets 验证被撤销的权限隐藏对应工具、
// 允许的工具上报运行，且单次运行预算会阻止继续调用。
func TestBuildToolsResolverFiltersAndBudgets(t *testing.T) {
	registry := pluginsapp.NewRegistry()
	calls := 0
	provider := fakeProvider{tools: []plugincontracts.RegisteredTool{
		{
			Descriptor: plugincontracts.ToolDescriptor{
				ID: "test.read", Description: "读取", ReadOnly: true, Permission: "test.read",
			},
			Invoke: func(context.Context, string) (string, error) {
				calls++
				return "ok", nil
			},
		},
		{
			Descriptor: plugincontracts.ToolDescriptor{
				ID: "test.admin", Description: "管理", ReadOnly: true, Permission: "test.admin",
			},
			Invoke: func(context.Context, string) (string, error) {
				calls++
				return "admin", nil
			},
		},
	}}
	if err := registry.Register(context.Background(), provider); err != nil {
		t.Fatalf("register: %v", err)
	}
	t.Cleanup(func() { _ = registry.Close(context.Background()) })

	cfg := &config.Config{
		Agent: config.Agent{MaxToolCalls: 1},
		Tools: config.Tools{
			Timeout:        config.Duration(time.Second),
			MaxResultBytes: 1024,
		},
	}
	resolver := buildToolsResolver(settingsForTest(cfg), registry)

	var runs []agentcontracts.ToolRun
	tools := resolver(context.Background(), agentcontracts.Request{
		PermissionOverrides: map[string]bool{"test.admin": false},
		OnToolRun: func(run agentcontracts.ToolRun) {
			runs = append(runs, run)
		},
	})
	if len(tools) != 1 {
		t.Fatalf("exposed tools = %d, want 1 after revoking test.admin", len(tools))
	}
	invokable, ok := tools[0].(tool.InvokableTool)
	if !ok {
		t.Fatalf("tool does not implement InvokableTool: %T", tools[0])
	}

	if _, err := invokable.InvokableRun(context.Background(), "{}"); err != nil {
		t.Fatalf("first call: %v", err)
	}
	out, err := invokable.InvokableRun(context.Background(), "{}")
	if err != nil {
		t.Fatalf("budget call must not fail: %v", err)
	}
	if !strings.Contains(out, "上限") {
		t.Fatalf("budget call output = %q", out)
	}
	if calls != 1 {
		t.Fatalf("tool invoked %d times, want 1 within the budget", calls)
	}
	if len(runs) != 2 || runs[1].Status != pluginsapp.StatusDenied || runs[1].ErrorCode != "tool_call_budget" || runs[0].ToolID != "test.read" || runs[0].Status != pluginsapp.StatusOK {
		t.Fatalf("reported runs = %+v", runs)
	}
}

// exposedToolNames returns the model-facing names of the resolved tools.
// exposedToolNames 返回解析后工具在模型侧的命名。
func exposedToolNames(t *testing.T, exposed []tool.BaseTool) []string {
	t.Helper()
	names := make([]string, 0, len(exposed))
	for _, base := range exposed {
		info, err := base.Info(context.Background())
		if err != nil {
			t.Fatalf("tool info: %v", err)
		}
		names = append(names, info.Name)
	}
	sort.Strings(names)
	return names
}

// TestToolScopeKindsMatchDomainKinds pins the plugin scope kinds to the
// conversation and scope domain values they are compared against at runtime.
// TestToolScopeKindsMatchDomainKinds 固定插件会话类型与运行时比较的
// conversation/scope 领域取值一致。
func TestToolScopeKindsMatchDomainKinds(t *testing.T) {
	if plugincontracts.ScopeKindPrivate != string(conversationdomain.KindPrivate) ||
		plugincontracts.ScopeKindGroup != string(conversationdomain.KindGroup) {
		t.Fatalf("plugin kinds %q/%q do not match conversation kinds %q/%q",
			plugincontracts.ScopeKindPrivate, plugincontracts.ScopeKindGroup,
			conversationdomain.KindPrivate, conversationdomain.KindGroup)
	}
	if plugincontracts.ScopeKindPrivate != string(scopedomain.ScopePrivate) ||
		plugincontracts.ScopeKindGroup != string(scopedomain.ScopeGroup) {
		t.Fatalf("plugin kinds %q/%q do not match scope kinds %q/%q",
			plugincontracts.ScopeKindPrivate, plugincontracts.ScopeKindGroup,
			scopedomain.ScopePrivate, scopedomain.ScopeGroup)
	}
}

// TestBuildToolsResolverFiltersByConversationKind verifies a group-only tool is
// hidden from private runs while tools without ScopeKinds keep their previous
// visibility in both kinds. An empty kind stays fail-closed for restricted
// tools.
// TestBuildToolsResolverFiltersByConversationKind 验证仅群聊可用的工具在私聊运行中
// 被隐藏，而省略 ScopeKinds 的工具在两种会话中都保持原有可见性；
// 空会话类型对限定范围的工具保持关闭式失败。
func TestBuildToolsResolverFiltersByConversationKind(t *testing.T) {
	registry := pluginsapp.NewRegistry()
	provider := fakeProvider{tools: []plugincontracts.RegisteredTool{
		{
			Descriptor: plugincontracts.ToolDescriptor{
				ID: "system.current_time", Description: "时间", ReadOnly: true,
			},
			Invoke: func(context.Context, string) (string, error) { return "ok", nil },
		},
		{
			Descriptor: plugincontracts.ToolDescriptor{
				ID: "qq.get_group_info", Description: "群信息", ReadOnly: true,
				Permission: "qq.group.read",
				ScopeKinds: []string{plugincontracts.ScopeKindGroup},
			},
			Invoke: func(context.Context, string) (string, error) { return "group", nil },
		},
	}}
	if err := registry.Register(context.Background(), provider); err != nil {
		t.Fatalf("register: %v", err)
	}
	t.Cleanup(func() { _ = registry.Close(context.Background()) })

	cfg := &config.Config{}
	resolver := buildToolsResolver(settingsForTest(cfg), registry)

	cases := []struct {
		name string
		kind string
		want []string
	}{
		{
			name: "private hides group tools",
			kind: plugincontracts.ScopeKindPrivate,
			want: []string{"system_current_time"},
		},
		{
			name: "group exposes group tools",
			kind: plugincontracts.ScopeKindGroup,
			want: []string{"qq_get_group_info", "system_current_time"},
		},
		{
			name: "empty kind hides group tools",
			kind: "",
			want: []string{"system_current_time"},
		},
	}
	for _, tc := range cases {
		got := exposedToolNames(t, resolver(context.Background(), agentcontracts.Request{Kind: tc.kind}))
		if !reflect.DeepEqual(got, tc.want) {
			t.Fatalf("%s: exposed tools = %v, want %v", tc.name, got, tc.want)
		}
	}
}

// TestReplyToolFollowsScopeGrant verifies the send tool is visible exactly when
// the conversation granted its permission; there is no process-level switch and
// no provider enable list any more.
// TestReplyToolFollowsScopeGrant 验证发送工具是否可见只取决于会话是否授予了
// 对应权限；进程级开关与提供方启用列表都已移除。
func TestReplyToolFollowsScopeGrant(t *testing.T) {
	registry := pluginsapp.NewRegistry()
	provider := fakeProvider{tools: []plugincontracts.RegisteredTool{{
		Descriptor: plugincontracts.ToolDescriptor{
			ID: "qq.send_msg", Description: "发送", ReadOnly: false, SideEffect: true,
			RiskLevel: plugincontracts.RiskWrite, Permission: "qq.reply.current",
		},
		Invoke: func(context.Context, string) (string, error) { return "sent", nil },
	}}}
	if err := registry.Register(context.Background(), provider); err != nil {
		t.Fatalf("register: %v", err)
	}
	t.Cleanup(func() { _ = registry.Close(context.Background()) })

	cfg := &config.Config{}
	granted := agentcontracts.Request{
		Kind:                plugincontracts.ScopeKindGroup,
		PermissionOverrides: map[string]bool{"qq.reply.current": true},
	}
	if got := exposedToolNames(t, buildToolsResolver(settingsForTest(cfg), registry)(context.Background(), granted)); !reflect.DeepEqual(got, []string{"qq_send_msg"}) {
		t.Fatalf("grant exposed %v, want qq_send_msg", got)
	}
	// Without the scope grant the tool stays hidden.
	if got := exposedToolNames(t, buildToolsResolver(settingsForTest(cfg), registry)(context.Background(), agentcontracts.Request{Kind: plugincontracts.ScopeKindGroup})); len(got) != 0 {
		t.Fatalf("missing grant exposed %v, want none", got)
	}
}

// settingsForTest builds a live snapshot from the file-level configuration.
// settingsForTest 依据文件配置构建实时快照。
func settingsForTest(cfg *config.Config) *settingsapp.Provider {
	return settingsapp.NewProvider(settingsapp.Snapshot{Settings: settingsapp.FromConfig(cfg)})
}

type replyRefusedError struct{}

func (replyRefusedError) Error() string      { return "reply refused" }
func (replyRefusedError) ToolStatus() string { return "denied" }

func TestReplyRunStaysStoppedAfterRefusal(t *testing.T) {
	registry := pluginsapp.NewRegistry()
	calls := 0
	provider := fakeProvider{tools: []plugincontracts.RegisteredTool{{
		Descriptor: plugincontracts.ToolDescriptor{ID: "qq.send_msg", Description: "send", ReplyCurrent: true, SideEffect: true, RiskLevel: plugincontracts.RiskWrite, Permission: "qq.reply.current"},
		Invoke: func(context.Context, string) (string, error) {
			calls++
			if calls == 2 {
				return "", replyRefusedError{}
			}
			return "sent", nil
		},
	}}}
	if err := registry.Register(context.Background(), provider); err != nil {
		t.Fatal(err)
	}
	defer registry.Close(context.Background())
	cfg := &config.Config{}
	var runs []agentcontracts.ToolRun
	req := agentcontracts.Request{Kind: "private", PermissionOverrides: map[string]bool{"qq.reply.current": true}, OnToolRun: func(run agentcontracts.ToolRun) { runs = append(runs, run) }}
	tools := buildToolsResolver(settingsForTest(cfg), registry)(context.Background(), req)
	if len(tools) != 1 {
		t.Fatalf("reply tool missing: %d", len(tools))
	}
	invoke := tools[0].(tool.InvokableTool)
	for i := 0; i < 3; i++ {
		if _, err := invoke.InvokableRun(context.Background(), "{}"); err != nil {
			t.Fatal(err)
		}
	}
	if calls != 2 || len(runs) != 3 || runs[2].Status != "denied" || runs[2].ErrorCode != "authorization_revoked" {
		t.Fatalf("run resumed after refusal: calls=%d runs=%+v", calls, runs)
	}
}

// TestBuildToolsResolverGatesMemoryTools verifies memory tools are exposed only
// when their separate run grants are present, and source additionally requires
// history read.
// TestBuildToolsResolverGatesMemoryTools 验证记忆工具仅在独立运行授权存在时暴露，
// 来源工具还额外要求历史读取授权。
func TestBuildToolsResolverGatesMemoryTools(t *testing.T) {
	registry := pluginsapp.NewRegistry()
	provider := fakeProvider{tools: []plugincontracts.RegisteredTool{
		{
			Descriptor: plugincontracts.ToolDescriptor{ID: plugincontracts.MemoryDetailToolID, Description: "记忆详情", ReadOnly: true},
			Invoke:     func(context.Context, string) (string, error) { return "detail", nil },
		},
		{
			Descriptor: plugincontracts.ToolDescriptor{ID: plugincontracts.MemorySourceToolID, Description: "记忆来源", ReadOnly: true},
			Invoke:     func(context.Context, string) (string, error) { return "source", nil },
		},
	}}
	if err := registry.Register(context.Background(), provider); err != nil {
		t.Fatalf("register: %v", err)
	}
	t.Cleanup(func() { _ = registry.Close(context.Background()) })
	resolver := buildToolsResolver(settingsForTest(&config.Config{}), registry)
	cases := []struct {
		name        string
		memoryRead  bool
		historyRead bool
		want        []string
	}{
		{name: "memory_on_history_on", memoryRead: true, historyRead: true, want: []string{"memory_detail", "memory_source"}},
		{name: "memory_on_history_off", memoryRead: true, historyRead: false, want: []string{"memory_detail"}},
		{name: "memory_off_history_on", memoryRead: false, historyRead: true, want: []string{}},
		{name: "memory_off_history_off", memoryRead: false, historyRead: false, want: []string{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			exposed := resolver(context.Background(), agentcontracts.Request{
				Kind: plugincontracts.ScopeKindGroup, AllowMemoryRead: tc.memoryRead, AllowHistoryRead: tc.historyRead,
			})
			got := exposedToolNames(t, exposed)
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("tools = %v, want %v", got, tc.want)
			}
		})
	}
}

// mutableScopeRepo is a one-row scope repository used to simulate an
// administrator revoking a permission while a run is in progress.
// mutableScopeRepo 是单行作用域仓库，用于模拟运行中管理员撤销权限。
type mutableScopeRepo struct{ policy scopedomain.Policy }

func (r *mutableScopeRepo) Get(context.Context, int64, scopedomain.ScopeType, int64) (scopedomain.Policy, bool, error) {
	return r.policy, true, nil
}
func (r *mutableScopeRepo) List(context.Context, *int64) ([]scopedomain.Policy, error) {
	return []scopedomain.Policy{r.policy}, nil
}
func (r *mutableScopeRepo) Upsert(_ context.Context, _ int64, p scopedomain.Policy) error {
	r.policy = p
	return nil
}
func (r *mutableScopeRepo) Delete(context.Context, int64, scopedomain.ScopeType, int64) (bool, error) {
	return false, nil
}

// TestMemoryToolReauthorizationBlocksAfterRevocation verifies the tool guard
// re-reads the scope and denies a memory tool after allow_memory_read is
// revoked mid-run.
// TestMemoryToolReauthorizationBlocksAfterRevocation 验证工具守卫重新读取作用域，
// 运行中撤销 allow_memory_read 后拒绝记忆工具。
func TestMemoryToolReauthorizationBlocksAfterRevocation(t *testing.T) {
	registry := pluginsapp.NewRegistry()
	provider := fakeProvider{tools: []plugincontracts.RegisteredTool{{
		Descriptor: plugincontracts.ToolDescriptor{ID: plugincontracts.MemoryDetailToolID, Description: "记忆详情", ReadOnly: true},
		Invoke:     func(context.Context, string) (string, error) { return "detail", nil },
	}}}
	if err := registry.Register(context.Background(), provider); err != nil {
		t.Fatalf("register: %v", err)
	}
	t.Cleanup(func() { _ = registry.Close(context.Background()) })
	repo := &mutableScopeRepo{policy: scopedomain.Policy{
		ScopeType: scopedomain.ScopeGroup, ScopeID: 700, Enabled: true,
		ReplyMode: scopedomain.ModeIntentBased, AllowMemoryRead: true,
	}}
	scopeService, err := scopeapp.NewService(repo)
	if err != nil {
		t.Fatalf("new scope service: %v", err)
	}
	resolver := buildToolsResolver(settingsForTest(&config.Config{}), registry, scopeService)
	ctx := context.Background()
	tools := resolver(ctx, agentcontracts.Request{
		BotID: 1, Kind: plugincontracts.ScopeKindGroup, TargetID: 700,
		ConversationID: "conv-1", AllowMemoryRead: true,
	})
	if len(tools) != 1 {
		t.Fatalf("exposed tools = %d, want 1", len(tools))
	}
	invokable, ok := tools[0].(tool.InvokableTool)
	if !ok {
		t.Fatalf("tool type = %T", tools[0])
	}
	if out, err := invokable.InvokableRun(ctx, "{}"); err != nil || out != "detail" {
		t.Fatalf("first call = %q, %v", out, err)
	}
	repo.policy.AllowMemoryRead = false
	out, err := invokable.InvokableRun(ctx, "{}")
	if err != nil || !strings.Contains(out, "authorization denied") {
		t.Fatalf("revoked call = %q, %v", out, err)
	}
}

// TestMemorySourceReauthorizationBlocksAfterHistoryRevocation verifies history
// revocation blocks the source tool while leaving metadata detail available.
// TestMemorySourceReauthorizationBlocksAfterHistoryRevocation 验证撤销历史读取后
// 来源工具被拒绝，而元数据详情仍可用。
func TestMemorySourceReauthorizationBlocksAfterHistoryRevocation(t *testing.T) {
	registry := pluginsapp.NewRegistry()
	provider := fakeProvider{tools: []plugincontracts.RegisteredTool{
		{
			Descriptor: plugincontracts.ToolDescriptor{ID: plugincontracts.MemoryDetailToolID, Description: "记忆详情", ReadOnly: true},
			Invoke:     func(context.Context, string) (string, error) { return "detail", nil },
		},
		{
			Descriptor: plugincontracts.ToolDescriptor{ID: plugincontracts.MemorySourceToolID, Description: "记忆来源", ReadOnly: true},
			Invoke:     func(context.Context, string) (string, error) { return "source", nil },
		},
	}}
	if err := registry.Register(context.Background(), provider); err != nil {
		t.Fatalf("register: %v", err)
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
	ctx := context.Background()
	tools := resolver(ctx, agentcontracts.Request{
		BotID: 1, Kind: plugincontracts.ScopeKindGroup, TargetID: 700,
		ConversationID: "conv-1", AllowMemoryRead: true, AllowHistoryRead: true,
	})
	if len(tools) != 2 {
		t.Fatalf("exposed tools = %d, want 2", len(tools))
	}
	detail, ok := tools[0].(tool.InvokableTool)
	if !ok {
		t.Fatalf("detail tool type = %T", tools[0])
	}
	source, ok := tools[1].(tool.InvokableTool)
	if !ok {
		t.Fatalf("source tool type = %T", tools[1])
	}
	if out, err := source.InvokableRun(ctx, "{}"); err != nil || out != "source" {
		t.Fatalf("first source call = %q, %v", out, err)
	}
	repo.policy.AllowHistoryRead = false
	if out, err := source.InvokableRun(ctx, "{}"); err != nil || !strings.Contains(out, "authorization denied") {
		t.Fatalf("revoked source call = %q, %v", out, err)
	}
	if out, err := detail.InvokableRun(ctx, "{}"); err != nil || out != "detail" {
		t.Fatalf("detail after history revocation = %q, %v", out, err)
	}
}

type intentProcessorSnapshotKey struct{}

type intentProcessorProbe struct {
	resolveCalls int
	decisions    []intentdomain.Decision
	resolveErr   error
}

func (p *intentProcessorProbe) ModelSnapshot(ctx context.Context) context.Context {
	return context.WithValue(ctx, intentProcessorSnapshotKey{}, p)
}

func (p *intentProcessorProbe) ResolveModel(ctx context.Context, _ string, _ int64, _ string, _ int64) (models.Resolved, error) {
	p.resolveCalls++
	if ctx.Value(intentProcessorSnapshotKey{}) != p {
		return models.Resolved{}, errors.New("model snapshot missing")
	}
	return models.Resolved{}, p.resolveErr
}

func (*intentProcessorProbe) BilibiliCredential(context.Context) (string, error) { return "", nil }

func (p *intentProcessorProbe) Record(_ context.Context, _ int64, d intentdomain.FinalDecision, _ ...string) error {
	p.decisions = append(p.decisions, d.Decision)
	return nil
}

func (p *intentProcessorProbe) ExecuteReply(ctx context.Context, task intentdomain.Task, _ []intentdomain.Message) error {
	_, err := p.ResolveModel(ctx, "generation", task.BotID, task.Kind, task.TargetID)
	return err
}

func TestIntentProcessorDefersGenerationResolution(t *testing.T) {
	credentialErr := errors.New("synthetic credential failure")
	for _, tt := range []struct {
		name           string
		intentEnabled  bool
		aiEnabled      bool
		mode           scopedomain.ReplyMode
		wantDecision   intentdomain.Decision
		wantGeneration bool
	}{
		{"observe only", true, true, scopedomain.ModeObserveOnly, intentdomain.DecisionSilent, false},
		{"generation disabled", true, false, scopedomain.ModeAlwaysReply, intentdomain.DecisionReply, false},
		{"intent disabled", false, false, scopedomain.ModeAlwaysReply, "", false},
		{"reply requires generation", true, true, scopedomain.ModeAlwaysReply, intentdomain.DecisionReply, true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			probe := &intentProcessorProbe{resolveErr: credentialErr}
			scopes, err := scopeapp.NewService(&mutableScopeRepo{policy: scopedomain.Policy{
				ScopeType: scopedomain.ScopeGroup, ScopeID: 20, Enabled: true, StoreMessages: true, ReplyMode: tt.mode,
			}})
			if err != nil {
				t.Fatal(err)
			}
			decider, err := intentapp.NewDecider(intentapp.Options{
				Scopes: scopes, Recorder: probe, Executor: probe,
				Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
			})
			if err != nil {
				t.Fatal(err)
			}
			ctx := intentapp.WithRuntimeConfig(context.Background(), intentapp.RuntimeConfig{
				Enabled: tt.intentEnabled, AIEnabled: tt.aiEnabled,
			})
			processor := intentProcessor{decider: decider, models: probe}
			err = processor.Process(ctx, conversationdomain.Job{
				ID: 1, BotID: 1, Kind: "group", TargetID: 20,
				Message: conversationdomain.Message{PlatformMessageID: 1, SenderID: 2, Text: "synthetic"},
			})
			if tt.wantGeneration {
				if !errors.Is(err, credentialErr) || probe.resolveCalls != 1 {
					t.Fatalf("process error=%v model resolutions=%d; want one failed generation", err, probe.resolveCalls)
				}
			} else if err != nil || probe.resolveCalls != 0 {
				t.Fatalf("process error=%v model resolutions=%d; want no generation", err, probe.resolveCalls)
			}
			if tt.wantDecision == "" {
				if len(probe.decisions) != 0 {
					t.Fatalf("disabled pipeline recorded %v", probe.decisions)
				}
			} else if len(probe.decisions) != 1 || probe.decisions[0] != tt.wantDecision {
				t.Fatalf("decisions=%v; want [%s]", probe.decisions, tt.wantDecision)
			}
		})
	}
}

func TestAccountCallNameRequiresConfiguredOrVerifiedOwner(t *testing.T) {
	role := settingsdomain.Role{BotName: "RoleName", CallNames: map[int64]string{10: "AccountName"}}
	register := func(accounts *svc.Accounts, endpoint string, expected, bound int64) *svc.AccountConnection {
		t.Helper()
		client := obapp.NewClientService(nil, time.Second, nil)
		conn := svc.NewAccountConnection(endpoint, expected, client)
		if bound != 0 {
			if err := client.BindIdentity(obdomain.BotID(bound)); err != nil {
				t.Fatal(err)
			}
		}
		if err := accounts.Add(conn); err != nil {
			t.Fatal(err)
		}
		return conn
	}
	check := func(role settingsdomain.Role, accounts *svc.Accounts, id int64, name, source string) {
		t.Helper()
		if n, s := accountCallName(role, accounts, id); n != name || s != source {
			t.Fatalf("bot=%d got (%q, %q), want (%q, %q)", id, n, s, name, source)
		}
	}
	explicit := svc.NewAccounts()
	register(explicit, "expected", 1, 0)
	check(role, explicit, 1, "RoleName", "role")
	check(role, explicit, 10, "AccountName", "account_config")
	check(role, explicit, 20, "", "")
	register(explicit, "second", 2, 0)
	check(role, explicit, 1, "", "")

	changed := svc.NewAccounts()
	newAccount := register(changed, "replacement", 0, 2)
	check(role, changed, 1, "", "")
	check(role, changed, 2, "", "")
	newAccount.SetVerifiedNickname(2, "")
	check(role, changed, 2, "RoleName", "role")
	check(role, changed, 1, "", "")
	newAccount.SetVerifiedNickname(2, "FixtureNickname")
	if nickname := changed.BotNickname(2); nickname != "FixtureNickname" || changed.BotNickname(1) != "" {
		t.Fatal("verified nickname did not stay with its account")
	}
	newAccount.Reject("fixture rejection")
	check(role, changed, 2, "", "")
	conflict := svc.NewAccounts()
	register(conflict, "conflict", 1, 2)
	check(role, conflict, 1, "", "")

	frozenAccount := svc.NewAccounts()
	register(frozenAccount, "frozen", 1, 0)
	provider := settingsapp.NewProvider(settingsapp.Snapshot{Settings: settingsdomain.Settings{Role: role}})
	ctx := context.WithValue(context.Background(), runSettingsKey{}, provider.Settings())
	provider.Store(settingsapp.Snapshot{Settings: settingsdomain.Settings{Role: settingsdomain.Role{BotName: "Renamed"}}})
	check(executionSettings(ctx, provider).Role, frozenAccount, 1, "RoleName", "role")
}
