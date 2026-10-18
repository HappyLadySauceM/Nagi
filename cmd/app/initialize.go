// initialize.go loads configuration and orchestrates the lifecycle.
// initialize.go 装载配置并编排应用生命周期。
package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"

	"github.com/HappyLadySauceM/QQBot/cmd/app/assembly"
	"github.com/HappyLadySauceM/QQBot/cmd/app/options"
	"github.com/HappyLadySauceM/QQBot/cmd/app/router"
	"github.com/HappyLadySauceM/QQBot/cmd/app/svc"
	"github.com/HappyLadySauceM/QQBot/pkg/config"
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

	rt, err := assembly.Assemble(cfg, logger)
	if err != nil {
		return err
	}
	svcCtx, err := svc.New(cfg, logger, rt.Onebot, rt.Dispatcher)
	if err != nil {
		return err
	}

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
