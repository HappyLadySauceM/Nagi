// Package assembly is the composition root binding ports to adapters.
// Package assembly 是组合根：将端口与具体适配器显式绑定，不含业务规则。
package assembly

import (
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	svc "github.com/HappyLadySauceM/QQBot/cmd/app/svc"
	auditcontracts "github.com/HappyLadySauceM/QQBot/internal/audit/contracts"
	auditinfra "github.com/HappyLadySauceM/QQBot/internal/audit/infrastructure"
	botapp "github.com/HappyLadySauceM/QQBot/internal/bot/application"
	botcontracts "github.com/HappyLadySauceM/QQBot/internal/bot/contracts"
	conversationapp "github.com/HappyLadySauceM/QQBot/internal/conversation/application"
	conversationinfra "github.com/HappyLadySauceM/QQBot/internal/conversation/infrastructure"
	directoryapp "github.com/HappyLadySauceM/QQBot/internal/directory/application"
	directoryinfra "github.com/HappyLadySauceM/QQBot/internal/directory/infrastructure"
	echoapp "github.com/HappyLadySauceM/QQBot/internal/echo/application"
	echoinfra "github.com/HappyLadySauceM/QQBot/internal/echo/infrastructure"
	memoryapp "github.com/HappyLadySauceM/QQBot/internal/memory/application"
	memoryinfra "github.com/HappyLadySauceM/QQBot/internal/memory/infrastructure"
	observabilityinfra "github.com/HappyLadySauceM/QQBot/internal/observability/infrastructure"
	obapp "github.com/HappyLadySauceM/QQBot/internal/onebot/application"
	obcontracts "github.com/HappyLadySauceM/QQBot/internal/onebot/contracts"
	obinfra "github.com/HappyLadySauceM/QQBot/internal/onebot/infrastructure"
	scopeapp "github.com/HappyLadySauceM/QQBot/internal/scope/application"
	scopeinfra "github.com/HappyLadySauceM/QQBot/internal/scope/infrastructure"
	"github.com/HappyLadySauceM/QQBot/pkg/config"
)

// handshakeTimeout bounds each dial attempt.
// handshakeTimeout 限定单次拨号时限。
const handshakeTimeout = 10 * time.Second

// Runtime holds the assembled runtime components.
// Runtime 持有已装配的运行时组件。
type Runtime struct {
	Transport  *obinfra.Transport
	Onebot     *obapp.ClientService
	Dispatcher *botapp.Dispatcher
	Handlers   []botcontracts.Handler

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
	// Stats may be nil when pool is nil.
	// pool 为 nil 时 Stats 可为 nil。
	Stats svc.StatsProvider
}

// Assemble wires the transport, client service, dispatcher, business handlers and,
// when a database pool is provided, the directory and scope services.
// Assemble 装配传输、客户端服务、分发器与业务处理器；提供数据库连接池时
// 同时装配目录与作用域服务。
func Assemble(cfg *config.Config, logger *slog.Logger, pool *pgxpool.Pool) (*Runtime, error) {
	transport := obinfra.NewTransport(obinfra.Options{
		URL:              cfg.Onebot.URL,
		AccessToken:      cfg.Onebot.AccessToken,
		HandshakeTimeout: handshakeTimeout,
		ReconnectInitial: cfg.Onebot.Reconnect.Initial.D(),
		ReconnectMax:     cfg.Onebot.Reconnect.Max.D(),
		HeartbeatTimeout: cfg.Onebot.HeartbeatTimeout.D(),
	}, logger)
	client := obapp.NewClientService(transport, cfg.Onebot.CallTimeout.D(), logger)
	transport.Attach(client, client)

	dispatcher := botapp.NewDispatcher(client, logger)
	handlers, err := registerBuiltins(cfg, client, dispatcher)
	if err != nil {
		return nil, err
	}
	rt := &Runtime{Transport: transport, Onebot: client, Dispatcher: dispatcher, Handlers: handlers}
	if pool != nil {
		if rt.Directory, err = buildDirectory(client, pool, logger); err != nil {
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
		ingress, ingressErr := conversationinfra.NewIngress(rt.Conversation, rt.Scope, client, logger)
		if ingressErr != nil {
			return nil, ingressErr
		}
		if err := dispatcher.Register(ingress); err != nil {
			return nil, fmt.Errorf("register handler %s: %w", ingress.Name(), err)
		}
		rt.Handlers = append(rt.Handlers, ingress)
		executor, executorErr := buildReplyExecutor(cfg, logger, pool, rt.Scope, rt.Conversation, rt.Memory, client)
		if executorErr != nil {
			return nil, executorErr
		}
		if rt.Worker, err = buildWorker(cfg, logger, pool, rt.Scope, rt.Conversation, executor); err != nil {
			return nil, err
		}
	}
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

// buildDirectory binds the pgx repository to the directory service.
// buildDirectory 将 pgx 仓库绑定到目录服务。
func buildDirectory(client *obapp.ClientService, pool *pgxpool.Pool, logger *slog.Logger) (*directoryapp.Service, error) {
	repo, err := directoryinfra.NewRepo(pool)
	if err != nil {
		return nil, err
	}
	return directoryapp.NewService(repo, client, logger)
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

// registerBuiltins mounts built-in sample contexts; new contexts follow this path.
// registerBuiltins 挂载内置示例上下文；新增业务上下文遵循相同路径。
func registerBuiltins(cfg *config.Config, sender obcontracts.Sender, dispatcher *botapp.Dispatcher) ([]botcontracts.Handler, error) {
	var handlers []botcontracts.Handler
	if cfg.Echo.Enabled {
		service, err := echoapp.NewService(sender, cfg.Echo.Prefix)
		if err != nil {
			return nil, fmt.Errorf("assemble echo context: %w", err)
		}
		handlers = append(handlers, echoinfra.NewHandler(service, cfg.Echo.Prefix))
	}
	for _, h := range handlers {
		if err := dispatcher.Register(h); err != nil {
			return nil, fmt.Errorf("register handler %s: %w", h.Name(), err)
		}
	}
	return handlers, nil
}
