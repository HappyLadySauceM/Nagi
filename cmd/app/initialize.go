// initialize.go loads configuration and orchestrates the lifecycle.
// initialize.go 装载配置并编排应用生命周期。
package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"

	"github.com/HappyLadySauceM/Nagi/cmd/app/assembly"
	"github.com/HappyLadySauceM/Nagi/cmd/app/options"
	"github.com/HappyLadySauceM/Nagi/cmd/app/router"
	"github.com/HappyLadySauceM/Nagi/cmd/app/svc"
	botapp "github.com/HappyLadySauceM/Nagi/internal/bot/application"
	"github.com/HappyLadySauceM/Nagi/internal/observability"
	obdomain "github.com/HappyLadySauceM/Nagi/internal/onebot/domain"
	"github.com/HappyLadySauceM/Nagi/migrations"
	"github.com/HappyLadySauceM/Nagi/pkg/config"
	"github.com/HappyLadySauceM/Nagi/pkg/db"
	"github.com/HappyLadySauceM/Nagi/pkg/logging"
	"github.com/HappyLadySauceM/Nagi/pkg/version"
)

// defaultConfigFile is tried when -config is not provided.
// defaultConfigFile 是未提供 -config 时的默认路径。
const defaultConfigFile = "configs/config.yaml"

// Run loads config, assembles the runtime and blocks until graceful shutdown.
// Run 装载配置、装配运行时并阻塞直至优雅退出。
func Run(ctx context.Context, opts options.Options) error {
	cfg, logger, err := bootstrap(opts)
	if err != nil {
		return err
	}
	logger.Info("nagi starting",
		"version", version.Version,
		"config_file", opts.ConfigFile,
		"listen", cfg.Server.Listen,
		"onebot_endpoints", len(cfg.Onebot.Endpoints),
		"echo_enabled", cfg.Echo.Enabled,
		"ai_enabled", cfg.AI.Enabled,
		"intent_enabled", cfg.Intent.Enabled,
		"database_configured", strings.TrimSpace(cfg.Database.DSN) != "",
		"admin_auth_enabled", strings.TrimSpace(cfg.Server.AdminToken) != "")
	// Both contexts may answer the same conversation, so surface the risk loudly
	// instead of relying on the operator remembering the docs.
	// 两套上下文可能回应同一会话，因此显式告警，不依赖运维人员记住文档。
	if cfg.Echo.Enabled && cfg.AI.Enabled {
		logger.Warn("echo and ai are both enabled; one conversation may be answered twice",
			"hint", "set echo.enabled=false when ai.enabled=true")
	}

	// Telemetry setup happens first so early spans are captured; its shutdown is
	// deferred and never fails the app. The provider is handed to the assembly
	// so Eino callbacks export through the same pipeline.
	// 观测初始化先执行以捕获早期 span；其关闭被 defer 且不影响应用退出。
	// Provider 传给组装根，使 Eino 回调复用同一条管线。
	var tracerProvider *sdktrace.TracerProvider
	if cfg.Observability.Enabled {
		telemetry, telemetryErr := observability.Setup(ctx, observability.Config{
			BaseURL:          cfg.Observability.BaseURL,
			PublicKey:        cfg.Observability.PublicKey,
			SecretKey:        cfg.Observability.SecretKey,
			IngestionVersion: cfg.Observability.IngestionVersion,
			ExportRawContent: cfg.Observability.ExportRawContent,
			Environment:      cfg.Observability.Environment,
		}, logger)
		if telemetryErr != nil {
			return telemetryErr
		}
		tracerProvider = telemetry.Provider
		// Raw content is exported by default; the warning makes the resulting
		// access-control requirement explicit at every start.
		// 正文默认导出；该告警在每次启动时明确随之而来的访问控制要求。
		if cfg.Observability.ExportRawContent {
			logger.Warn("observability exports raw message content; restrict Langfuse access accordingly",
				"hint", "set observability.export-raw-content=false to redact content")
		}
		defer func() {
			shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if err := telemetry.Shutdown(shutdownCtx); err != nil {
				logger.Warn("telemetry shutdown failed", "error", err)
			}
		}()
	}

	pool, err := connectDatabase(ctx, cfg, logger)
	if err != nil {
		return err
	}
	if pool != nil {
		defer pool.Close()
	}
	rt, err := assembly.Assemble(cfg, logger, pool, tracerProvider)
	if err != nil {
		return err
	}
	// The Eino callback must flush before the shared provider shuts down.
	// Eino 回调必须先于共享 Provider 关闭完成刷新。
	if rt.CloseTools != nil {
		defer func() {
			shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if err := rt.CloseTools(shutdownCtx); err != nil {
				logger.Warn("tool provider shutdown failed", "error", err)
			}
		}()
	}
	if rt.ShutdownLangfuse != nil {
		defer func() {
			shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if err := rt.ShutdownLangfuse(shutdownCtx); err != nil {
				logger.Warn("langfuse callback shutdown failed", "error", err)
			}
		}()
	}
	dispatchers := make([]*botapp.Dispatcher, 0, len(rt.Accounts))
	for _, account := range rt.Accounts {
		dispatchers = append(dispatchers, account.Dispatcher)
	}
	svcCtx, err := svc.New(cfg, logger, rt.Registry, dispatchers, rt.Directory, rt.Scope, rt.Memory, rt.Audit)
	if err != nil {
		return err
	}
	svcCtx.Stats = rt.Stats
	svcCtx.Tools = rt.Tools
	svcCtx.Decisions = rt.Decisions
	svcCtx.Rulesets = rt.Rulesets
	svcCtx.Outbound = rt.Outbound

	// runCtx isolates internal loops from the caller's signal context.
	// runCtx 将内部循环与调用方的信号上下文隔离。
	runCtx, cancelRun := context.WithCancel(ctx)
	defer cancelRun()

	apiDone := make(chan struct{})
	apiErrs := make(chan error, 1)
	if cfg.Server.Enabled {
		server := NewAPIServer(cfg.Server.Listen, router.New(svcCtx, logger), logger)
		go func() {
			defer close(apiDone)
			if err := server.Run(runCtx); err != nil {
				apiErrs <- err
			}
		}()
	}

	var loops sync.WaitGroup
	for _, account := range rt.Accounts {
		loops.Add(3)
		go func(a *assembly.AccountRuntime) {
			defer loops.Done()
			a.Transport.Run(runCtx)
		}(account)
		go func(a *assembly.AccountRuntime) {
			defer loops.Done()
			if err := a.Dispatcher.Run(runCtx); err != nil {
				logger.Error("bot dispatcher exited with error", "endpoint", a.Endpoint, "error", err)
			}
		}(account)
		go func(a *assembly.AccountRuntime) {
			defer loops.Done()
			identifyAccount(runCtx, logger, a)
		}(account)
	}

	// Worker is nil until the AI decision pipeline is wired.
	// AI 决策流水线接入前 Worker 为 nil。
	var workerDone chan struct{}
	if rt.Worker != nil {
		workerDone = make(chan struct{})
		go func() {
			defer close(workerDone)
			if err := rt.Worker.Run(runCtx); err != nil {
				logger.Error("conversation worker exited with error", "error", err)
			}
		}()
	}

	var runErr error
	select {
	case err := <-apiErrs:
		runErr = fmt.Errorf("management api exited: %w", err)
		logger.Info("internal failure detected, shutting down gracefully")
	case <-ctx.Done():
		logger.Info("shutdown signal received")
	}
	cancelRun()

	// Shutdown order: WebSocket → HTTP (inside its goroutine) → wait in-flight goroutines.
	// 关闭顺序：WS → HTTP（在其 goroutine 内）→ 等待在途协程。
	for _, account := range rt.Accounts {
		_ = account.Client.Close()
	}
	loops.Wait()
	if workerDone != nil {
		<-workerDone
	}
	if cfg.Server.Enabled {
		<-apiDone
		select {
		case err := <-apiErrs:
			if runErr == nil {
				runErr = fmt.Errorf("management api exited: %w", err)
			}
		default:
		}
	}
	logger.Info("nagi stopped")
	return runErr
}

// identifyAccount discovers which QQ account one endpoint serves. It waits for
// the connection, binds the identity and rejects the endpoint when the reported
// QQ number differs from expect-qq or conflicts with an existing binding.
// identifyAccount 识别单个端点服务的 QQ 账号：等待连接、绑定身份，并在
// 实际 QQ 号与 expect-qq 不一致或与既有绑定冲突时拒绝该端点。
func identifyAccount(ctx context.Context, logger *slog.Logger, account *assembly.AccountRuntime) {
	logger = logger.With("endpoint", account.Endpoint)
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		if botID, known := account.Client.SelfID(); known {
			verifyIdentity(logger, account, int64(botID), "")
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		if !account.Client.Connected() {
			continue
		}
		callCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		login, err := account.Client.GetLoginInfo(callCtx)
		cancel()
		if err != nil {
			logger.Debug("account identity pending: get_login_info failed", "error", err)
			continue
		}
		if err := account.Client.BindIdentity(obdomain.BotID(login.UserID)); err != nil {
			account.Connection.Reject(err.Error())
			logger.Error("account identity conflict; connection rejected", "bot_id", int64(login.UserID), "error", err)
			_ = account.Client.Close()
			return
		}
		verifyIdentity(logger, account, int64(login.UserID), login.Nickname)
		return
	}
}

// verifyIdentity enforces expect-qq and logs the discovered account.
// verifyIdentity 校验 expect-qq 并记录识别到的账号。
func verifyIdentity(logger *slog.Logger, account *assembly.AccountRuntime, botID int64, nickname string) {
	if account.ExpectedQQ != 0 && botID != account.ExpectedQQ {
		reason := fmt.Sprintf("endpoint reports QQ %d, expected %d", botID, account.ExpectedQQ)
		account.Connection.Reject(reason)
		logger.Error("account identity mismatch; connection rejected", "bot_id", botID, "reason", reason)
		_ = account.Client.Close()
		return
	}
	logger.Info("account identified", "bot_id", botID, "nickname", nickname)
}

// connectDatabase opens the pool and applies migrations when a DSN is configured;
// it returns nil when the DSN is empty, keeping the bot usable without a database.
// connectDatabase 在配置了 DSN 时打开连接池并应用迁移；DSN 为空时返回 nil，
// 机器人可在无数据库的情况下继续运行。
func connectDatabase(ctx context.Context, cfg *config.Config, logger *slog.Logger) (*pgxpool.Pool, error) {
	dsn := strings.TrimSpace(cfg.Database.DSN)
	if dsn == "" {
		return nil, nil
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	pool, err := db.Connect(ctx, dsn, cfg.Database.MaxConns)
	if err != nil {
		return nil, fmt.Errorf("database: %w", err)
	}
	applied, err := db.Migrate(ctx, pool, migrations.FS)
	if err != nil {
		pool.Close()
		return nil, fmt.Errorf("database: %w", err)
	}
	// Automatic migration is the only schema entry point; log what this start
	// actually applied so operators never have to guess.
	// 启动自动迁移是唯一的 schema 入口；记录本次实际应用的版本，运维无需猜测。
	logger.Info("database ready",
		"max_conns", cfg.Database.MaxConns,
		"migrations_applied", len(applied),
		"migration_versions", applied)
	return pool, nil
}

// bootstrap loads config and builds the logger, tolerating a missing default file.
// bootstrap 装载配置并构建日志器；默认配置文件缺失时容忍并以默认值启动。
func bootstrap(opts options.Options) (*config.Config, *slog.Logger, error) {
	file := opts.ConfigFile
	if file == "" {
		file = defaultConfigFile
	}
	if _, err := os.Stat(file); err != nil {
		if opts.ConfigFile == "" && errors.Is(err, os.ErrNotExist) {
			return loadAndStart(config.Options{Overrides: toOverrides(opts)})
		}
		return nil, nil, fmt.Errorf("config file unavailable: %w", err)
	}
	return loadAndStart(config.Options{File: file, Overrides: toOverrides(opts)})
}

// loadAndStart loads config then creates the JSON logger for the configured level.
// loadAndStart 装载配置，再按级别创建 JSON 日志器。
func loadAndStart(loadOpts config.Options) (*config.Config, *slog.Logger, error) {
	cfg, err := config.Load(loadOpts)
	if err != nil {
		return nil, nil, err
	}
	logger, err := logging.New(cfg.Log.Level)
	if err != nil {
		return nil, nil, err
	}
	return cfg, logger, nil
}

// toOverrides maps CLI flags onto config overrides.
// toOverrides 将命令行 Flag 映射为配置覆盖。
func toOverrides(opts options.Options) config.Overrides {
	return config.Overrides{
		Listen:   opts.Listen,
		LogLevel: opts.LogLevel,
	}
}
