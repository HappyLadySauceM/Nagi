// Settings wiring: the live snapshot service plus the lazy runtime proxies that
// rebuild model clients when the console changes them.
// 配置装配：实时快照服务，以及控制台改动后按需重建客户端的两层代理。
package assembly

import (
	"context"
	"errors"
	"fmt"
	"github.com/HappyLadySauceM/Nagi/cmd/app/svc"
	mediainfra "github.com/HappyLadySauceM/Nagi/internal/media/infrastructure"
	models "github.com/HappyLadySauceM/Nagi/internal/models/contracts"
	modelinfra "github.com/HappyLadySauceM/Nagi/internal/models/infrastructure"
	"log/slog"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/HappyLadySauceM/Nagi/pkg/db"
	"github.com/cloudwego/eino/callbacks"
	"github.com/cloudwego/eino/components/tool"

	agentcontracts "github.com/HappyLadySauceM/Nagi/internal/agent/contracts"
	einoinfra "github.com/HappyLadySauceM/Nagi/internal/agent/infrastructure/eino"
	intentapp "github.com/HappyLadySauceM/Nagi/internal/intent/application"
	intentdomain "github.com/HappyLadySauceM/Nagi/internal/intent/domain"
	intentinfra "github.com/HappyLadySauceM/Nagi/internal/intent/infrastructure"
	pluginsapp "github.com/HappyLadySauceM/Nagi/internal/plugins/application"
	settingsapp "github.com/HappyLadySauceM/Nagi/internal/settings/application"
	settingsdomain "github.com/HappyLadySauceM/Nagi/internal/settings/domain"
	settingsinfra "github.com/HappyLadySauceM/Nagi/internal/settings/infrastructure"
	"github.com/HappyLadySauceM/Nagi/pkg/config"
)

// errGenerationDisabled is defensive: the decider already skips replies when
// the model is off, so reaching it means the settings changed mid-run.
// errGenerationDisabled 属于防御性错误：关闭生成模型时决策器已跳过回复，
// 走到这里说明配置在一次运行中被关闭。
var errGenerationDisabled = errors.New("agent: generation model is disabled")

// buildSettings assembles the settings service and its live snapshot. Without
// a database the snapshot still carries the config-file baseline so every
// consumer can read it unconditionally.
// buildSettings 装配配置服务与实时快照。没有数据库时快照仍承载配置文件基线，
// 各消费方因此可以无条件读取。
func buildSettings(cfg *config.Config, logger *slog.Logger, pool *db.Pool,
	tools *pluginsapp.Registry,
) (*settingsapp.Service, *settingsapp.Provider, error) {
	providers := make([]string, 0)
	knownPermissions := make(map[string]bool)
	if tools != nil {
		for _, manifest := range tools.Manifests() {
			providers = append(providers, manifest.ID)
		}
		sort.Strings(providers)
		for _, registered := range tools.Snapshot() {
			for _, permission := range registered.Descriptor.AdditionalPermissions {
				knownPermissions[permission] = true
			}
			if permission := registered.Descriptor.Permission; permission != "" {
				knownPermissions[permission] = true
			}
		}
	}
	knownPermissions["media.voice.transcribe"] = true
	defaults := settingsapp.FromConfig(cfg)
	provider := settingsapp.NewProvider(settingsapp.Snapshot{Settings: defaults.Clone()})

	var repo settingsapp.Repository
	if pool != nil {
		repository, err := settingsinfra.NewRepo(pool)
		if err != nil {
			return nil, nil, err
		}
		repo = repository
	}
	vault, err := modelinfra.NewVault()
	if err != nil {
		return nil, nil, err
	}
	service, err := settingsapp.NewService(settingsapp.Options{
		ModelTester: modelTester{}, Cipher: vault, LegacyAIKey: cfg.AI.APIKey, LegacyIntentKey: cfg.Intent.APIKey,
		Repository:       repo,
		Provider:         provider,
		Defaults:         defaults,
		Providers:        providers,
		KnownPermissions: knownPermissions,
		Credentials: settingsapp.Credentials{
			AI:     cfg.AI.APIKey != "",
			Intent: cfg.Intent.APIKey != "",
		},
		Logger: logger,
	})
	if err != nil {
		return nil, nil, err
	}
	if err := service.Ensure(context.Background()); err != nil {
		return nil, nil, err
	}
	return service, provider, nil
}

// modelRuntime rebuilds the Eino runtime when the model-related settings
// change, so switching the model or its endpoint needs no restart. Building is
// lazy: a process that never generates never constructs a client.
// modelRuntime 在模型相关配置变化时重建 Eino 运行时，
// 切换模型或端点因此无需重启。构建是惰性的：从不生成的进程不会创建客户端。
type modelRuntime struct {
	mu                sync.Mutex
	settings          *settingsapp.Provider
	models            models.Resolver
	tools             func(context.Context, agentcontracts.Request) []tool.BaseTool
	handlers          []callbacks.Handler
	sensitiveHandlers []callbacks.Handler
	logger            *slog.Logger
	runtimes          map[string]agentcontracts.Runtime
}

// newModelRuntime builds the proxy around one lazily created runtime.
// newModelRuntime 构建按需创建运行时的代理。
func newModelRuntime(settings *settingsapp.Provider, resolver models.Resolver,
	tools func(context.Context, agentcontracts.Request) []tool.BaseTool,
	handlers []callbacks.Handler, logger *slog.Logger,
) *modelRuntime {
	return &modelRuntime{settings: settings, models: resolver, tools: tools, handlers: handlers, logger: logger, runtimes: map[string]agentcontracts.Runtime{}}
}

// Run resolves the current model configuration, rebuilding once per change.
// Run 解析当前模型配置，每次变化只重建一次。
func (r *modelRuntime) Run(ctx context.Context, req agentcontracts.Request) (agentcontracts.Result, error) {
	if snapshots, ok := r.models.(models.Snapshotter); ok {
		ctx = snapshots.ModelSnapshot(ctx)
	}
	live := executionSettings(ctx, r.settings)
	resolved, err := r.models.ResolveModel(ctx, "generation", req.BotID, req.Kind, req.TargetID)
	if err != nil {
		return agentcontracts.Result{}, err
	}
	p := resolved.Profile
	live.AI.BaseURL = p.BaseURL
	live.AI.Model = p.Model
	live.AI.MaxOutputTokens = p.MaxOutputTokens
	live.AI.GenerationTimeoutSeconds = p.TimeoutSeconds
	req.MaxOutputTokens = p.MaxOutputTokens
	req.CapabilitiesKnown = true
	req.ModelImages = p.Images
	req.ModelTools = p.Tools
	r.mu.Lock()
	fingerprint := fmt.Sprintf("%s:%d:%s", p.ID, p.Version, modelFingerprint(live))
	runtime := r.runtimes[fingerprint]
	if runtime == nil {
		runtime, err = einoinfra.NewRuntime(ctx, einoinfra.Config{HTTPHeaders: p.HTTPHeaders, ProviderDefaultOutput: p.MaxOutputTokens == 0, BaseURL: p.BaseURL, APIKey: resolved.APIKey, Model: p.Model, Thinking: p.Thinking, ReasoningEffort: p.ReasoningEffort, ContextWindow: p.ContextWindow, MaxOutputTokens: p.MaxOutputTokens, Timeout: time.Duration(live.Agent.MaxRunDurationSeconds) * time.Second, HTTPTimeout: time.Duration(p.TimeoutSeconds) * time.Second, MaxSteps: live.Agent.MaxSteps, Tools: r.tools, Handlers: r.handlers, SensitiveHandlers: r.sensitiveHandlers})
		if err != nil {
			r.mu.Unlock()
			return agentcontracts.Result{}, err
		}
		for key := range r.runtimes {
			if strings.HasPrefix(key, p.ID+":") {
				delete(r.runtimes, key)
			}
		}
		if len(r.runtimes) >= 32 {
			clear(r.runtimes)
		}
		r.runtimes[fingerprint] = runtime
	}
	r.mu.Unlock()
	return runtime.Run(ctx, req)
}

// modelFingerprint covers every value baked into the Eino runtime.
// modelFingerprint 覆盖所有被固化进 Eino 运行时的取值。
func modelFingerprint(settings settingsdomain.Settings) string {
	return fmt.Sprintf("%s|%s|%d|%d|%d|%d",
		settings.AI.BaseURL, settings.AI.Model, settings.AI.MaxOutputTokens,
		settings.AI.GenerationTimeoutSeconds, settings.Agent.MaxRunDurationSeconds,
		settings.Agent.MaxSteps)
}

// semanticRouter chooses the jev or openai client from the live settings and
// rebuilds it when the endpoint, model or timeout changes.
// semanticRouter 依据实时配置选择 jev 或 openai 客户端，
// 端点、模型或超时变化时重建。
type semanticRouter struct {
	transport   *http.Transport
	mu          sync.Mutex
	settings    *settingsapp.Provider
	models      models.Resolver
	logger      *slog.Logger
	current     intentapp.SemanticDecider
	fingerprint string
}

// newSemanticRouter builds the proxy around the semantic clients.
// newSemanticRouter 构建语义客户端代理。
func newSemanticRouter(settings *settingsapp.Provider, resolver models.Resolver, logger *slog.Logger) *semanticRouter {
	return &semanticRouter{settings: settings, models: resolver, logger: logger, transport: &http.Transport{Proxy: nil}}
}

// Decide forwards one semantic question to the currently configured client.
// Decide 把一次语义判定转发给当前配置的客户端。
func (r *semanticRouter) Decide(ctx context.Context, in intentdomain.Input) (intentdomain.SemanticAnswer, error) {
	decider, err := r.resolve(ctx)
	if err != nil {
		return intentdomain.SemanticAnswer{}, err
	}
	return decider.Decide(ctx, in)
}

func (r *semanticRouter) resolve(ctx context.Context) (intentapp.SemanticDecider, error) {
	resolved, err := r.models.ResolveModel(ctx, "intent", 0, "", 0)
	if err != nil {
		return nil, err
	}
	p := resolved.Profile
	botName := executionSettings(ctx, r.settings).Intent.BotName
	fingerprint := fmt.Sprintf("%s:%d:%s", p.ID, p.Version, botName)
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.current != nil && r.fingerprint == fingerprint {
		return r.current, nil
	}
	timeout := time.Duration(p.TimeoutSeconds) * time.Second
	cfg := intentinfra.JevConfig{HTTPHeaders: p.HTTPHeaders, MaxOutputTokens: p.MaxOutputTokens, Thinking: p.Thinking, ReasoningEffort: p.ReasoningEffort, ContextWindow: p.ContextWindow, Endpoint: p.BaseURL, APIKey: resolved.APIKey, Model: p.Model, BotName: botName, Timeout: timeout}
	var decider intentapp.SemanticDecider
	if p.Protocol == "openai" {
		cfg.Endpoint = strings.TrimRight(p.BaseURL, "/") + "/chat/completions"
		decider = &intentinfra.LocalClient{Config: cfg, Client: &http.Client{Timeout: timeout, Transport: r.transport}}
	} else {
		decider, err = intentinfra.NewJevClient(cfg, r.logger)
		if err != nil {
			return nil, err
		}
	}
	r.current = decider
	r.fingerprint = fingerprint
	return decider, nil
}

// mediaAccounts binds media lookups to the account that received the message.
// mediaAccounts 将媒体查询绑定到接收消息的账号。
type mediaAccounts struct{ accounts *svc.Accounts }

func (a mediaAccounts) MediaReader(botID int64) (mediainfra.Reader, bool) {
	r, ok := a.accounts.InfoReader(botID)
	if !ok {
		return nil, false
	}
	return r, true
}
