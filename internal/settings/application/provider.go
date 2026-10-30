// Package application exposes the live settings snapshot and the management
// service behind the console.
// Package application 暴露实时配置快照与控制台背后的管理服务。
package application

import (
	"sync/atomic"
	"time"

	settingsdomain "github.com/HappyLadySauceM/Nagi/internal/settings/domain"
)

// Snapshot is the immutable value every component reads per run.
// Snapshot 是所有组件按次读取的不可变值。
type Snapshot struct {
	Settings  settingsdomain.Settings
	Version   int64
	UpdatedAt time.Time
	UpdatedBy string
	// Persisted is false when the process runs without a database, so the
	// console can explain that changes will not survive a restart.
	// Persisted 为 false 表示进程没有数据库，控制台据此提示改动不会跨重启保留。
	Persisted bool
}

// Provider stores the current snapshot so readers never take a lock and never
// observe a half-written configuration.
// Provider 持有当前快照，读者因此无需加锁，也不会看到写了一半的配置。
type Provider struct {
	current atomic.Pointer[Snapshot]
}

// NewProvider builds a provider that always holds a usable snapshot.
// NewProvider 构建始终持有可用快照的提供者。
func NewProvider(initial Snapshot) *Provider {
	provider := &Provider{}
	initial.Settings.Normalize()
	provider.current.Store(&initial)
	return provider
}

// Current returns the current snapshot; it is safe for concurrent readers.
// Current 返回当前快照；并发读取安全。
func (p *Provider) Current() Snapshot {
	if p == nil || p.current.Load() == nil {
		return Snapshot{}
	}
	return *p.current.Load()
}

// Settings returns just the configuration of the current snapshot.
// Settings 仅返回当前快照中的配置。
func (p *Provider) Settings() settingsdomain.Settings {
	return p.Current().Settings
}

// Store replaces the snapshot atomically.
// Store 原子替换快照。
func (p *Provider) Store(snapshot Snapshot) {
	snapshot.Settings.Normalize()
	p.current.Store(&snapshot)
}
