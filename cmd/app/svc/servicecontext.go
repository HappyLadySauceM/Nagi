// Package svc composes the ServiceContext dependency container.
// Package svc 组合 ServiceContext：cmd/app 的依赖容器。
package svc

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	auditcontracts "github.com/HappyLadySauceM/QQBot/internal/audit/contracts"
	botapp "github.com/HappyLadySauceM/QQBot/internal/bot/application"
	directoryapp "github.com/HappyLadySauceM/QQBot/internal/directory/application"
	memoryapp "github.com/HappyLadySauceM/QQBot/internal/memory/application"
	obapp "github.com/HappyLadySauceM/QQBot/internal/onebot/application"
	scopeapp "github.com/HappyLadySauceM/QQBot/internal/scope/application"
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

	// Directory, Scope, Memory and Audit are nil when the database is not configured.
	// 未配置数据库时 Directory、Scope、Memory 与 Audit 为 nil。
	Directory *directoryapp.Service
	Scope     *scopeapp.Service
	Memory    *memoryapp.Service
	Audit     auditcontracts.Recorder
	// Stats may be nil when the database is not configured.
	// 未配置数据库时 Stats 可为 nil。
	Stats StatsProvider
}

// StatsProvider supplies aggregate counters for the metrics endpoint.
// StatsProvider 为指标接口提供聚合计数。
type StatsProvider interface {
	Stats(ctx context.Context) (map[string]any, error)
}

// New validates inputs and builds the context; database-backed services may be nil.
// New 校验输入并构建依赖容器；依赖数据库的服务可为 nil。
func New(cfg *config.Config, logger *slog.Logger, onebot *obapp.ClientService, dispatcher *botapp.Dispatcher,
	directory *directoryapp.Service, scope *scopeapp.Service, memory *memoryapp.Service, audit auditcontracts.Recorder,
) (*ServiceContext, error) {
	if cfg == nil || logger == nil || onebot == nil || dispatcher == nil {
		return nil, fmt.Errorf("svc: dependencies required")
	}
	return &ServiceContext{
		Config:     cfg,
		Logger:     logger,
		StartedAt:  time.Now(),
		Onebot:     onebot,
		Dispatcher: dispatcher,
		Directory:  directory,
		Scope:      scope,
		Memory:     memory,
		Audit:      audit,
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
	if status.HeartbeatSeen {
		onebot["heartbeat_seen"] = true
		onebot["last_heartbeat"] = status.LastHeartbeatAt.Format(time.RFC3339)
		if status.HeartbeatIntervalMillis > 0 {
			onebot["heartbeat_interval_ms"] = status.HeartbeatIntervalMillis
		}
	}
	snapshot["onebot"] = onebot
	snapshot["handlers"] = s.Dispatcher.Names()
	snapshot["handled_events"] = s.Dispatcher.HandledCount()
	return snapshot
}
