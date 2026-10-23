// initialize.go loads configuration and orchestrates the lifecycle.
// initialize.go 装载配置并编排应用生命周期。
package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/HappyLadySauceM/QQBot/cmd/app/assembly"
	"github.com/HappyLadySauceM/QQBot/cmd/app/options"
	"github.com/HappyLadySauceM/QQBot/cmd/app/router"
	"github.com/HappyLadySauceM/QQBot/cmd/app/svc"
	"github.com/HappyLadySauceM/QQBot/internal/observability"
	"github.com/HappyLadySauceM/QQBot/migrations"
	"github.com/HappyLadySauceM/QQBot/pkg/config"
	"github.com/HappyLadySauceM/QQBot/pkg/db"
	"github.com/HappyLadySauceM/QQBot/pkg/logging"
	"github.com/HappyLadySauceM/QQBot/pkg/version"
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
	logger.Info("qqbot starting",
		"version", version.Version,
		"config_file", opts.ConfigFile,
		"listen", cfg.Server.Listen,
		"echo_enabled", cfg.Echo.Enabled)

	// Telemetry setup happens first so early spans are captured; its shutdown is
	// deferred and never fails the app.
	// 观测初始化先执行以捕获早期 span；其关闭被 defer 且不影响应用退出。
	if cfg.Observability.Enabled {
		telemetryShutdown, telemetryErr := observability.Setup(ctx, observability.Resolve(
			cfg.Observability.BaseURLEnv, cfg.Observability.PublicKeyEnv,
			cfg.Observability.SecretKeyEnv, cfg.Observability.IngestionVersion,
			cfg.Observability.ExportRawContent, observability.ResolveEnv,
		), logger)
		if telemetryErr != nil {
			return telemetryErr
		}
		defer func() {
			shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if err := telemetryShutdown(shutdownCtx); err != nil {
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
	rt, err := assembly.Assemble(cfg, logger, pool)
	if err != nil {
		return err
	}
	svcCtx, err := svc.New(cfg, logger, rt.Onebot, rt.Dispatcher, rt.Directory, rt.Scope, rt.Memory, rt.Audit)
	if err != nil {
		return err
	}
	svcCtx.Stats = rt.Stats

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

	transportDone := make(chan struct{})
	go func() {
		defer close(transportDone)
		rt.Transport.Run(runCtx)
	}()

	dispatcherDone := make(chan struct{})
	go func() {
		defer close(dispatcherDone)
		if err := rt.Dispatcher.Run(runCtx); err != nil {
			logger.Error("bot dispatcher exited with error", "error", err)
		}
	}()

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
	_ = rt.Onebot.Close()
	<-transportDone
	<-dispatcherDone
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
	logger.Info("qqbot stopped")
	return runErr
}

// connectDatabase opens the pool and applies migrations when a DSN is configured;
// it returns nil when no DSN resolves, keeping the bot usable without a database.
// connectDatabase 在配置了 DSN 时打开连接池并应用迁移；未解析出 DSN 时返回 nil，
// 机器人可在无数据库的情况下继续运行。
func connectDatabase(ctx context.Context, cfg *config.Config, logger *slog.Logger) (*pgxpool.Pool, error) {
	dsn := cfg.Database.EffectiveDSN(os.LookupEnv)
	if dsn == "" {
		return nil, nil
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	pool, err := db.Connect(ctx, dsn, cfg.Database.MaxConns)
	if err != nil {
		return nil, fmt.Errorf("database: %w", err)
	}
	if err := db.Migrate(ctx, pool, migrations.FS); err != nil {
		pool.Close()
		return nil, fmt.Errorf("database: %w", err)
	}
	logger.Info("database ready", "max_conns", cfg.Database.MaxConns)
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
		OnebotURL:   opts.OnebotURL,
		AccessToken: opts.AccessToken,
		Listen:      opts.Listen,
		LogLevel:    opts.LogLevel,
	}
}
