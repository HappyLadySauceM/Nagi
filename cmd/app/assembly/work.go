// Package assembly is the composition root binding ports to adapters.
// Package assembly 是组合根：将端口与具体适配器显式绑定，不含业务规则。
package assembly

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	sdktrace "go.opentelemetry.io/otel/sdk/trace"

	"github.com/cloudwego/eino/components/tool"

	svc "github.com/HappyLadySauceM/Nagi/cmd/app/svc"
	agentcontracts "github.com/HappyLadySauceM/Nagi/internal/agent/contracts"
	replytool "github.com/HappyLadySauceM/Nagi/internal/agent/infrastructure/replytool"
	auditcontracts "github.com/HappyLadySauceM/Nagi/internal/audit/contracts"
	auditinfra "github.com/HappyLadySauceM/Nagi/internal/audit/infrastructure"
	botapp "github.com/HappyLadySauceM/Nagi/internal/bot/application"
	botcontracts "github.com/HappyLadySauceM/Nagi/internal/bot/contracts"
	conversationapp "github.com/HappyLadySauceM/Nagi/internal/conversation/application"
	conversationinfra "github.com/HappyLadySauceM/Nagi/internal/conversation/infrastructure"
	directoryapp "github.com/HappyLadySauceM/Nagi/internal/directory/application"
	directoryinfra "github.com/HappyLadySauceM/Nagi/internal/directory/infrastructure"
	echoapp "github.com/HappyLadySauceM/Nagi/internal/echo/application"
	echoinfra "github.com/HappyLadySauceM/Nagi/internal/echo/infrastructure"
	intentcontracts "github.com/HappyLadySauceM/Nagi/internal/intent/contracts"
	memoryapp "github.com/HappyLadySauceM/Nagi/internal/memory/application"
	memoryinfra "github.com/HappyLadySauceM/Nagi/internal/memory/infrastructure"
	"github.com/HappyLadySauceM/Nagi/internal/observability"
	observabilityinfra "github.com/HappyLadySauceM/Nagi/internal/observability/infrastructure"
	obapp "github.com/HappyLadySauceM/Nagi/internal/onebot/application"
	obinfra "github.com/HappyLadySauceM/Nagi/internal/onebot/infrastructure"
	outboundapp "github.com/HappyLadySauceM/Nagi/internal/outbound/application"
	outbounddomain "github.com/HappyLadySauceM/Nagi/internal/outbound/domain"
	outboundinfra "github.com/HappyLadySauceM/Nagi/internal/outbound/infrastructure"
	pluginsapp "github.com/HappyLadySauceM/Nagi/internal/plugins/application"
	pluginscontracts "github.com/HappyLadySauceM/Nagi/internal/plugins/contracts"
	"github.com/HappyLadySauceM/Nagi/internal/plugins/infrastructure/builtin"
	scopeapp "github.com/HappyLadySauceM/Nagi/internal/scope/application"
	scopeinfra "github.com/HappyLadySauceM/Nagi/internal/scope/infrastructure"
	"github.com/HappyLadySauceM/Nagi/pkg/config"
)

// handshakeTimeout bounds each dial attempt.
// handshakeTimeout 限定单次拨号时限。
const handshakeTimeout = 10 * time.Second

// AccountRuntime holds one connection's transport, client and dispatcher.
// AccountRuntime 持有单条连接的传输、客户端与分发器。
type AccountRuntime struct {
	Endpoint   string
	ExpectedQQ int64
	Transport  *obinfra.Transport
	Client     *obapp.ClientService
	Dispatcher *botapp.Dispatcher
	// Connection is the registry entry used to reject a mismatched identity.
	// Connection 是注册表条目，用于拒绝身份不匹配的连接。
	Connection *svc.AccountConnection
}

// Runtime holds the assembled runtime components.
// Runtime 持有已装配的运行时组件。
type Runtime struct {
	// Accounts is one runtime per configured endpoint.
	// Accounts 是每个已配置端点对应的运行时。
	Accounts []*AccountRuntime
	// Registry routes account-scoped operations by bot id.
	// Registry 按 bot id 路由账号级操作。
	Registry *svc.Accounts
	Handlers []botcontracts.Handler

	// Directory, Scope, Conversation, Memory and Audit are nil when pool is nil
	// (no database configured); Worker is nil until intent is enabled.
	// pool 为 nil（未配置数据库）时 Directory、Scope、Conversation、Memory 与 Audit 为 nil；
	// intent 未启用时 Worker 为 nil。
	Directory    *directoryapp.Service
	Scope        *scopeapp.Service
	Conversation *conversationapp.Service
	Memory       *memoryapp.Service
	Audit        auditcontracts.Recorder
	Worker       *conversationinfra.Worker
	// Decisions reads recorded intent decisions; nil until intent is enabled.
	// Decisions 读取已记录的意图决策；intent 未启用时为 nil。
	Decisions intentcontracts.DecisionReader
	// Stats may be nil when pool is nil.
	// pool 为 nil 时 Stats 可为 nil。
	Stats svc.StatsProvider
	// ShutdownLangfuse flushes the Eino callback; nil when it is not wired.
	// ShutdownLangfuse 刷新 Eino 回调；未接入时为 nil。
	ShutdownLangfuse func(context.Context) error
	// CloseTools releases registered tool providers; nil when none are wired.
	// CloseTools 释放已注册的工具提供方；未接入时为 nil。
	CloseTools func(context.Context) error
	// Tools is the startup tool registry; nil when AI generation is disabled.
	// Tools 是启动时的工具注册表；AI 生成关闭时为 nil。
	Tools *pluginsapp.Registry
}

// Assemble wires one connection per configured endpoint, registers the shared
// business handlers on each dispatcher and, when a database pool is provided,
// builds the directory, scope, conversation and memory services.
// Assemble 为每个已配置端点装配一条连接，把共享处理器注册到每个分发器，
// 并在提供数据库连接池时装配目录、作用域、会话与记忆服务。
func Assemble(cfg *config.Config, logger *slog.Logger, pool *pgxpool.Pool, provider *sdktrace.TracerProvider) (*Runtime, error) {
	registry := svc.NewAccounts()
	accounts := make([]*AccountRuntime, 0, len(cfg.Onebot.Endpoints))
	for _, endpoint := range cfg.Onebot.Endpoints {
		connLogger := logger.With("endpoint", endpoint.URL)
		transport := obinfra.NewTransport(obinfra.Options{
			URL:              endpoint.URL,
			AccessToken:      endpoint.AccessToken,
			HandshakeTimeout: handshakeTimeout,
			ReconnectInitial: cfg.Onebot.Reconnect.Initial.D(),
			ReconnectMax:     cfg.Onebot.Reconnect.Max.D(),
			HeartbeatTimeout: cfg.Onebot.HeartbeatTimeout.D(),
		}, connLogger)
		client := obapp.NewClientService(transport, cfg.Onebot.CallTimeout.D(), connLogger)
		transport.Attach(client, client)
		dispatcher := botapp.NewDispatcher(client, client, connLogger)
		connection := svc.NewAccountConnection(endpoint.URL, endpoint.ExpectQQ, client)
		if err := registry.Add(connection); err != nil {
			return nil, err
		}
		accounts = append(accounts, &AccountRuntime{
			Endpoint:   endpoint.URL,
			ExpectedQQ: endpoint.ExpectQQ,
			Transport:  transport,
			Client:     client,
			Dispatcher: dispatcher,
			Connection: connection,
		})
	}

	rt := &Runtime{Accounts: accounts, Registry: registry}
	handlers, err := buildBuiltins(cfg, registry)
	if err != nil {
		return nil, err
	}

	// Without raw identifiers or a usable secret the traces keep working but
	// expose no user dimension; the warning fires once at startup instead of per
	// message and always states the reason.
	// 既未启用原始标识也没有可用密钥时，追踪仍可用但不暴露用户维度；
	// 告警只在启动时输出一次，并始终说明原因。
	identities := observability.NewIdentities(cfg.Observability.UserIDSecret, cfg.Observability.RawIdentifiers)
	if cfg.Observability.Enabled {
		switch {
		case cfg.Observability.RawIdentifiers:
			logger.Warn("observability exports raw QQ identifiers; restrict Langfuse access accordingly")
		case identities == nil:
			logger.Warn("observability identity disabled; traces carry no user dimension",
				"reason", observability.ValidateSecret(cfg.Observability.UserIDSecret).Error(),
				"hint", "set observability.user-id-secret to `openssl rand -hex 32` or use NAGI_OBSERVABILITY_USER_ID_SECRET")
		}
	}

	if pool != nil {
		if rt.Directory, err = buildDirectory(registry, pool, logger); err != nil {
			return nil, err
		}
		if rt.Scope, err = buildScope(pool); err != nil {
			return nil, err
		}
		if rt.Conversation, err = buildConversation(pool, logger); err != nil {
			return nil, err
		}
		if rt.Memory, err = buildMemory(pool); err != nil {
			return nil, err
		}
		if rt.Audit, err = auditinfra.NewRecorder(pool); err != nil {
			return nil, err
		}
		if rt.Stats, err = observabilityinfra.NewStatsRepo(pool); err != nil {
			return nil, err
		}
		// The ingress only persists messages for scopes the administrator
		// explicitly enabled, so it is safe to register whenever the database
		// is configured; default-deny keeps everything off until then.
		// 入站处理器仅为管理员显式开启的作用域持久化消息，因此配置了数据库
		// 即可注册；默认拒绝保证在授权前一切关闭。
		ingress, ingressErr := conversationinfra.NewIngress(rt.Conversation, rt.Scope, identities, logger)
		if ingressErr != nil {
			return nil, ingressErr
		}
		handlers = append(handlers, ingress)
		// Membership notices keep group_members current without polling OneBot.
		// 成员变更通知让群成员目录保持最新，无需轮询 OneBot。
		membership, membershipErr := directoryinfra.NewMembershipHandler(rt.Directory, logger)
		if membershipErr != nil {
			return nil, membershipErr
		}
		handlers = append(handlers, membership)
		callbacks := buildLangfuseCallback(cfg, logger, provider)
		outboundRepo, outboundErr := outboundinfra.NewRepo(pool)
		if outboundErr != nil {
			return nil, outboundErr
		}
		ledger, ledgerErr := outboundapp.NewCoordinator(outboundRepo)
		if ledgerErr != nil {
			return nil, ledgerErr
		}
		var toolsResolver func(context.Context, agentcontracts.Request) []tool.BaseTool
		if cfg.AI.Enabled {
			toolRegistry := pluginsapp.NewRegistry(cfg.Tools.Enabled)
			providers := []pluginscontracts.ToolProvider{
				builtin.NewTimeProvider(),
				builtin.NewQQProvider(groupInfoResolver{accounts: rt.Registry}),
			}
			if cfg.Tools.AllowSendCurrent {
				providers = append(providers, replytool.NewProvider(replytool.Options{
					Senders:  replySenders{accounts: rt.Registry},
					Ledger:   ledger,
					Recorder: rt.Conversation,
					Limits: outbounddomain.Limits{
						MaxMessages:        cfg.Agent.Sending.MaxMessagesPerRun,
						MaxCharsPerMessage: cfg.Agent.Sending.MaxCharsPerMessage,
						MaxTotalChars:      cfg.Agent.Sending.MaxTotalCharsPerRun,
					},
				}))
			}
			for _, providerImpl := range providers {
				if registerErr := toolRegistry.Register(context.Background(), providerImpl); registerErr != nil {
					_ = toolRegistry.Close(context.Background())
					return nil, registerErr
				}
			}
			// An enabled id that no provider matched is always a typo; failing
			// fast beats starting with silently missing capabilities.
			// 已启用但没有任何提供方匹配的 ID 一定是拼写错误；
			// 直接启动失败胜过带着静默缺失的能力运行。
			if unknown := toolRegistry.Unknown(); len(unknown) > 0 {
				_ = toolRegistry.Close(context.Background())
				return nil, fmt.Errorf("tools.enabled contains unknown providers %v; known providers: %v",
					unknown, toolRegistry.Known())
			}
			rt.Tools = toolRegistry
			rt.CloseTools = toolRegistry.Close
			logger.Info("tools registered",
				"count", len(toolRegistry.Snapshot()),
				"providers", len(toolRegistry.Manifests()),
				"enabled", cfg.Tools.Enabled)
			toolsResolver = buildToolsResolver(cfg, toolRegistry)
		}
		executor, executorErr := buildReplyExecutor(context.Background(), cfg, logger, pool, rt.Scope, rt.Conversation, registry, callbacks, ledger, toolsResolver)
		if executorErr != nil {
			return nil, executorErr
		}
		if callbacks != nil {
			handler := callbacks
			rt.ShutdownLangfuse = func(ctx context.Context) error { return handler.Shutdown(ctx) }
		}
		worker, decisions, workerErr := buildWorker(cfg, logger, pool, rt.Scope, rt.Conversation, executor, identities)
		if workerErr != nil {
			return nil, workerErr
		}
		rt.Worker = worker
		rt.Decisions = decisions
	}

	// Every connection shares the same handler set; the dispatcher supplies the
	// account identity so handlers never infer it from global state.
	// 所有连接共享同一套处理器；账号身份由分发器提供，处理器不推断全局状态。
	for _, account := range accounts {
		for _, handler := range handlers {
			if err := account.Dispatcher.Register(handler); err != nil {
				return nil, fmt.Errorf("register handler %s on %s: %w", handler.Name(), account.Endpoint, err)
			}
		}
	}
	rt.Handlers = handlers
	return rt, nil
}

// buildConversation binds the pgx repository to the conversation service.
// buildConversation 将 pgx 仓库绑定到会话服务。
func buildConversation(pool *pgxpool.Pool, logger *slog.Logger) (*conversationapp.Service, error) {
	repo, err := conversationinfra.NewRepo(pool)
	if err != nil {
		return nil, err
	}
	return conversationapp.NewService(repo, logger)
}

// buildMemory binds the pgx repository to the memory service.
// buildMemory 将 pgx 仓库绑定到记忆服务。
func buildMemory(pool *pgxpool.Pool) (*memoryapp.Service, error) {
	repo, err := memoryinfra.NewRepo(pool)
	if err != nil {
		return nil, err
	}
	return memoryapp.NewService(repo)
}

// buildDirectory binds the pgx repository and the account registry to the
// directory service, so every query is scoped to one account.
// buildDirectory 将 pgx 仓库与账号注册表绑定到目录服务，使每次查询都属于单个账号。
func buildDirectory(accounts *svc.Accounts, pool *pgxpool.Pool, logger *slog.Logger) (*directoryapp.Service, error) {
	repo, err := directoryinfra.NewRepo(pool)
	if err != nil {
		return nil, err
	}
	return directoryapp.NewService(repo, accounts, logger)
}

// buildScope binds the pgx repository to the scope service.
// buildScope 将 pgx 仓库绑定到作用域服务。
func buildScope(pool *pgxpool.Pool) (*scopeapp.Service, error) {
	repo, err := scopeinfra.NewRepo(pool)
	if err != nil {
		return nil, err
	}
	return scopeapp.NewService(repo)
}

// buildBuiltins mounts built-in sample contexts; new contexts follow this path.
// buildBuiltins 挂载内置示例上下文；新增业务上下文遵循相同路径。
func buildBuiltins(cfg *config.Config, accounts *svc.Accounts) ([]botcontracts.Handler, error) {
	var handlers []botcontracts.Handler
	if cfg.Echo.Enabled {
		service, err := echoapp.NewService(accounts, cfg.Echo.Prefix)
		if err != nil {
			return nil, fmt.Errorf("assemble echo context: %w", err)
		}
		handlers = append(handlers, echoinfra.NewHandler(service, cfg.Echo.Prefix))
	}
	return handlers, nil
}

// groupInfoResolver adapts the account registry to the tool's narrow port.
// groupInfoResolver 把账号注册表适配为工具所需的窄接口。
type groupInfoResolver struct {
	accounts *svc.Accounts
}

// GroupInfoReader resolves the query client of one account.
// GroupInfoReader 解析单个账号的查询客户端。
func (r groupInfoResolver) GroupInfoReader(botID int64) (builtin.GroupInfoReader, bool) {
	reader, ok := r.accounts.InfoReader(botID)
	if !ok {
		return nil, false
	}
	return reader, true
}

// replySenders adapts the account registry to the reply tool's narrow sender port.
// replySenders 把账号注册表适配为回复工具的窄发送端口。
type replySenders struct{ accounts *svc.Accounts }

// ReceiptSender returns the sender of one account; never another account's.
// ReceiptSender 返回某账号的发送能力，绝不借用其它账号。
func (s replySenders) ReceiptSender(botID int64) (replytool.Sender, bool) {
	sender, ok := s.accounts.ReceiptSender(botID)
	if !ok {
		return nil, false
	}
	return sender, true
}
