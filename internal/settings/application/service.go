// Service coordinates persistence, validation and the live snapshot.
// Service 协调持久化、校验与实时快照。
package application

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	settingsdomain "github.com/HappyLadySauceM/Nagi/internal/settings/domain"
)

// ErrConflict reports that another writer saved a newer revision first.
// ErrConflict 表示另一个写入者已先保存了更新的修订。
var ErrConflict = errors.New("settings: revision conflict")

// Repository is the persistence port of the settings row.
// Repository 是配置行的持久化端口。
type Repository interface {
	Load(ctx context.Context) (settingsdomain.Revision, bool, error)
	Insert(ctx context.Context, settings settingsdomain.Settings, actor string) (settingsdomain.Revision, error)
	Update(ctx context.Context, settings settingsdomain.Settings, expected int64, actor string) (settingsdomain.Revision, bool, error)
	Version(ctx context.Context) (int64, bool, error)
}

// Credentials reports which secret-bearing sections are configured in the
// config file; the console only shows presence, never the values.
// Credentials 表示哪些含密钥的分区已在配置文件中就绪；
// 控制台只展示是否存在，绝不展示取值。
type Credentials struct {
	AI     bool
	Intent bool
}

// View is the console payload of one settings revision.
// View 是单个配置修订的控制台载荷。
type View struct {
	Settings    settingsdomain.Settings
	Defaults    settingsdomain.Settings
	Version     int64
	UpdatedAt   time.Time
	UpdatedBy   string
	Persisted   bool
	Credentials Credentials
	Providers   []string
}

// Options wires the service dependencies.
// Options 装配服务依赖。
type Options struct {
	Repository Repository
	Provider   *Provider
	Defaults   settingsdomain.Settings
	// Providers lists the registered tool providers for read-only display.
	// Providers 列出已注册的工具提供方，仅用于只读展示。
	Providers        []string
	KnownPermissions map[string]bool
	Credentials      Credentials
	Logger           *slog.Logger
}

// Service exposes reads, writes and the reload loop of the settings row.
// Service 提供配置行的读取、写入与重载循环。
type Service struct {
	repo             Repository
	provider         *Provider
	defaults         settingsdomain.Settings
	providers        []string
	knownPermissions map[string]bool
	credentials      Credentials
	logger           *slog.Logger
}

// NewService validates dependencies and builds the service.
// NewService 校验依赖并构建服务。
func NewService(opts Options) (*Service, error) {
	if opts.Provider == nil {
		return nil, fmt.Errorf("settings: provider required")
	}
	if opts.Logger == nil {
		return nil, fmt.Errorf("settings: logger required")
	}
	opts.Defaults.Normalize()
	return &Service{
		repo:             opts.Repository,
		provider:         opts.Provider,
		defaults:         opts.Defaults,
		providers:        append([]string{}, opts.Providers...),
		knownPermissions: opts.KnownPermissions,
		credentials:      opts.Credentials,
		logger:           opts.Logger,
	}, nil
}

// Ensure loads the stored revision or seeds the row with the YAML baseline.
// Ensure 读取已存储的修订；缺失时用 YAML 基线写入初值。
func (s *Service) Ensure(ctx context.Context) error {
	if s.repo == nil {
		s.provider.Store(Snapshot{Settings: s.defaults.Clone(), Persisted: false})
		return nil
	}
	revision, found, err := s.repo.Load(ctx)
	if err != nil {
		return err
	}
	if !found {
		revision, err = s.repo.Insert(ctx, s.defaults.Clone(), "bootstrap")
		if err != nil {
			return err
		}
		s.logger.Info("system settings seeded from config", "version", revision.Version)
	}
	s.provider.Store(snapshotOf(revision))
	return nil
}

// View returns the current revision together with the defaults baseline.
// View 返回当前修订以及默认基线。
func (s *Service) View() View {
	current := s.provider.Current()
	return View{
		Settings:    current.Settings.Clone(),
		Defaults:    s.defaults.Clone(),
		Version:     current.Version,
		UpdatedAt:   current.UpdatedAt,
		UpdatedBy:   current.UpdatedBy,
		Persisted:   current.Persisted,
		Credentials: s.credentials,
		Providers:   append([]string{}, s.providers...),
	}
}

// Update validates and persists a new revision, then swaps the live snapshot so
// the next conversation sees it without a restart.
// Update 校验并持久化新修订，随后替换实时快照，
// 下一次会话无需重启即可使用新配置。
func (s *Service) Update(ctx context.Context, incoming settingsdomain.Settings, expected int64, actor string) (View, error) {
	incoming.Normalize()
	if err := incoming.Validate(s.knownPermissions); err != nil {
		return View{}, err
	}
	// Enabling generation without a key would fail on the first reply; reject it
	// while the operator is still looking at the switch.
	// 没有密钥就启用生成会在第一次回复时失败；趁运维还在开关前直接拒绝。
	if incoming.AI.Enabled && !s.credentials.AI {
		invalid := &settingsdomain.ValidationError{}
		invalid.Add("AI.Enabled", "配置文件缺少 ai.api-key，无法启用生成模型")
		return View{}, invalid
	}
	if s.repo == nil {
		current := s.provider.Current()
		s.provider.Store(Snapshot{
			Settings:  incoming.Clone(),
			Version:   current.Version + 1,
			UpdatedAt: time.Now().UTC(),
			UpdatedBy: actor,
			Persisted: false,
		})
		return s.View(), nil
	}
	revision, applied, err := s.repo.Update(ctx, incoming.Clone(), expected, actor)
	if err != nil {
		return View{}, err
	}
	if !applied {
		return View{}, ErrConflict
	}
	s.provider.Store(snapshotOf(revision))
	s.logger.Info("system settings updated",
		"version", revision.Version, "actor", actor,
		"ai_enabled", revision.Settings.AI.Enabled,
		"intent_enabled", revision.Settings.Intent.Enabled,
		"tools_providers", len(s.providers))
	return s.View(), nil
}

// Reset restores the YAML baseline as a new revision.
// Reset 把 YAML 基线作为新修订恢复。
func (s *Service) Reset(ctx context.Context, actor string) (View, error) {
	current := s.provider.Current()
	return s.Update(ctx, s.defaults.Clone(), current.Version, actor)
}

// Watch polls the stored revision so another replica's save becomes visible
// here without waiting for a restart.
// Watch 轮询已存储的修订，使其它副本的保存无需重启即可在此生效。
func (s *Service) Watch(ctx context.Context, interval time.Duration) {
	if s.repo == nil || interval <= 0 {
		return
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := s.reloadIfChanged(ctx); err != nil {
				s.logger.Warn("system settings reload failed", "error", err)
			}
		}
	}
}

func (s *Service) reloadIfChanged(ctx context.Context) error {
	version, found, err := s.repo.Version(ctx)
	if err != nil {
		return err
	}
	if !found || version == s.provider.Current().Version {
		return nil
	}
	revision, exists, err := s.repo.Load(ctx)
	if err != nil {
		return err
	}
	if !exists {
		return nil
	}
	s.provider.Store(snapshotOf(revision))
	s.logger.Info("system settings reloaded from database", "version", revision.Version)
	return nil
}

func snapshotOf(revision settingsdomain.Revision) Snapshot {
	settings := revision.Settings.Clone()
	settings.Normalize()
	return Snapshot{
		Settings:  settings,
		Version:   revision.Version,
		UpdatedAt: revision.UpdatedAt,
		UpdatedBy: revision.UpdatedBy,
		Persisted: true,
	}
}
