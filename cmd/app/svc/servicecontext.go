// Package svc composes the ServiceContext dependency container.
// Package svc 组合 ServiceContext：cmd/app 的依赖容器。
package svc

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"

	auditcontracts "github.com/HappyLadySauceM/Nagi/internal/audit/contracts"
	botapp "github.com/HappyLadySauceM/Nagi/internal/bot/application"
	directoryapp "github.com/HappyLadySauceM/Nagi/internal/directory/application"
	memoryapp "github.com/HappyLadySauceM/Nagi/internal/memory/application"
	pluginsapp "github.com/HappyLadySauceM/Nagi/internal/plugins/application"
	scopeapp "github.com/HappyLadySauceM/Nagi/internal/scope/application"
	"github.com/HappyLadySauceM/Nagi/pkg/config"
	"github.com/HappyLadySauceM/Nagi/pkg/version"
)

// ServiceContext holds runtime dependencies for the management API.
// ServiceContext 持有管理 API 所需的运行时依赖。
type ServiceContext struct {
	Config    *config.Config
	Logger    *slog.Logger
	StartedAt time.Time
	// Accounts routes every account-scoped operation; one entry per endpoint.
	// Accounts 路由所有按账号隔离的操作；每个端点一条记录。
	Accounts *Accounts
	// Dispatchers holds one event loop per connection.
	// Dispatchers 为每条连接持有一个事件循环。
	Dispatchers []*botapp.Dispatcher

	// Directory, Scope, Memory and Audit are nil when the database is not configured.
	// 未配置数据库时 Directory、Scope、Memory 与 Audit 为 nil。
	Directory *directoryapp.Service
	Scope     *scopeapp.Service
	Memory    *memoryapp.Service
	Audit     auditcontracts.Recorder
	// Stats may be nil when the database is not configured.
	// 未配置数据库时 Stats 可为 nil。
	Stats StatsProvider
	// Tools is the startup tool registry; nil when AI generation is disabled.
	// Tools 是启动时的工具注册表；AI 生成关闭时为 nil。
	Tools *pluginsapp.Registry
}

// KnownToolPermissions returns the permission identifiers declared by the
// registered tools; callers reject overrides for anything outside this set.
// KnownToolPermissions 返回已注册工具声明的权限标识；
// 调用方据此拒绝集合之外的覆盖项。
func (s *ServiceContext) KnownToolPermissions() map[string]bool {
	if s.Tools == nil {
		return nil
	}
	known := make(map[string]bool)
	for _, registered := range s.Tools.Snapshot() {
		if permission := registered.Descriptor.Permission; permission != "" {
			known[permission] = true
		}
	}
	return known
}

// StatsProvider supplies aggregate counters for the metrics endpoint.
// StatsProvider 为指标接口提供聚合计数。
type StatsProvider interface {
	Stats(ctx context.Context, botID int64) (map[string]any, error)
}

// New validates inputs and builds the context; database-backed services may be nil.
// New 校验输入并构建依赖容器；依赖数据库的服务可为 nil。
func New(cfg *config.Config, logger *slog.Logger, accounts *Accounts, dispatchers []*botapp.Dispatcher,
	directory *directoryapp.Service, scope *scopeapp.Service, memory *memoryapp.Service, audit auditcontracts.Recorder,
) (*ServiceContext, error) {
	if cfg == nil || logger == nil || accounts == nil || len(dispatchers) == 0 {
		return nil, fmt.Errorf("svc: dependencies required")
	}
	return &ServiceContext{
		Config:      cfg,
		Logger:      logger,
		StartedAt:   time.Now(),
		Accounts:    accounts,
		Dispatchers: dispatchers,
		Directory:   directory,
		Scope:       scope,
		Memory:      memory,
		Audit:       audit,
	}, nil
}

// ErrBotIDRequired reports an ambiguous request when several accounts exist.
// ErrBotIDRequired 表示多账号场景下请求未指定账号。
var ErrBotIDRequired = errors.New("svc: bot_id is required when several accounts are configured")

// ErrAccountUnknown reports that no account identity is available yet.
// ErrAccountUnknown 表示账号身份尚不可用。
var ErrAccountUnknown = errors.New("svc: bot account identity is not available yet")

// ErrInvalidBotID reports a malformed bot_id parameter.
// ErrInvalidBotID 表示 bot_id 参数格式非法。
var ErrInvalidBotID = errors.New("svc: invalid bot_id")

// ResolveBotID resolves the account of a request: an explicit bot_id wins,
// otherwise the single configured account is used. Several accounts without an
// explicit id is an error, never a guess.
// ResolveBotID 解析请求所属账号：显式 bot_id 优先，否则使用唯一配置的账号。
// 多账号且未显式指定时返回错误，绝不做猜测。
func (s *ServiceContext) ResolveBotID(raw string) (int64, error) {
	if trimmed := strings.TrimSpace(raw); trimmed != "" {
		botID, err := strconv.ParseInt(trimmed, 10, 64)
		if err != nil || botID <= 0 {
			return 0, fmt.Errorf("%w: %q", ErrInvalidBotID, trimmed)
		}
		return botID, nil
	}
	conn, ok := s.Accounts.Default()
	if !ok {
		return 0, ErrBotIDRequired
	}
	botID, known := conn.BotID()
	if !known {
		return 0, ErrAccountUnknown
	}
	return botID, nil
}

// Uptime returns how long the process has been running.
// Uptime 返回进程运行时长。
func (s *ServiceContext) Uptime() time.Duration { return time.Since(s.StartedAt) }

// HandlerNames returns the union of registered handler names across connections.
// HandlerNames 返回所有连接已注册处理器名的并集。
func (s *ServiceContext) HandlerNames() []string {
	seen := make(map[string]struct{})
	names := make([]string, 0, 4)
	for _, dispatcher := range s.Dispatchers {
		for _, name := range dispatcher.Names() {
			if _, ok := seen[name]; ok {
				continue
			}
			seen[name] = struct{}{}
			names = append(names, name)
		}
	}
	return names
}

// HandledCount sums successful handler executions across connections.
// HandledCount 汇总所有连接成功执行的处理器次数。
func (s *ServiceContext) HandledCount() uint64 {
	var total uint64
	for _, dispatcher := range s.Dispatchers {
		total += dispatcher.HandledCount()
	}
	return total
}

// StatusSnapshot builds the data for /api/v1/status; it never contains message bodies.
// StatusSnapshot 生成 /api/v1/status 数据；不包含任何消息内容。
func (s *ServiceContext) StatusSnapshot() map[string]any {
	snapshot := map[string]any{
		"version":        version.Version,
		"uptime_seconds": int64(s.Uptime().Seconds()),
		"echo_enabled":   s.Config.Echo.Enabled,
	}

	snapshot["handlers"] = s.HandlerNames()
	snapshot["handled_events"] = s.HandledCount()
	return snapshot
}
