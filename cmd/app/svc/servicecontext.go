// Package svc composes the ServiceContext dependency container.
// Package svc 组合 ServiceContext：cmd/app 的依赖容器。
package svc

import (
	"fmt"
	"log/slog"
	"time"

	botapp "github.com/HappyLadySauceM/QQBot/internal/bot/application"
	obapp "github.com/HappyLadySauceM/QQBot/internal/onebot/application"
	"github.com/HappyLadySauceM/QQBot/pkg/config"
	"github.com/HappyLadySauceM/QQBot/pkg/version"
)

// ServiceContext holds runtime dependencies for the management API.
// ServiceContext 持有管理 API 所需的运行时依赖。
type ServiceContext struct {
	Config     *config.Config
	Logger     *slog.Logger
	StartedAt  time.Time
	Onebot     *obapp.ClientService
	Dispatcher *botapp.Dispatcher
}

// New validates inputs and builds the context.
// New 校验输入并构建依赖容器。
func New(cfg *config.Config, logger *slog.Logger, onebot *obapp.ClientService, dispatcher *botapp.Dispatcher) (*ServiceContext, error) {
	if cfg == nil || logger == nil || onebot == nil || dispatcher == nil {
		return nil, fmt.Errorf("svc: dependencies required")
	}
	return &ServiceContext{
		Config:     cfg,
		Logger:     logger,
		StartedAt:  time.Now(),
		Onebot:     onebot,
		Dispatcher: dispatcher,
	}, nil
}

// Uptime returns how long the process has been running.
// Uptime 返回进程运行时长。
func (s *ServiceContext) Uptime() time.Duration { return time.Since(s.StartedAt) }

// StatusSnapshot builds the data for /api/v1/status; it never contains message bodies.
// StatusSnapshot 生成 /api/v1/status 数据；不包含任何消息内容。
func (s *ServiceContext) StatusSnapshot() map[string]any {
	snapshot := map[string]any{
		"version":        version.Version,
		"uptime_seconds": int64(s.Uptime().Seconds()),
		"echo_enabled":   s.Config.Echo.Enabled,
	}
	status := s.Onebot.Status()
	onebot := map[string]any{"connected": status.Connected}
	if status.SelfIDKnown {
		onebot["self_id"] = int64(status.SelfID)
	}
	snapshot["onebot"] = onebot
	snapshot["handlers"] = s.Dispatcher.Names()
	snapshot["handled_events"] = s.Dispatcher.HandledCount()
	return snapshot
}
