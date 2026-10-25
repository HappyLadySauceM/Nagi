// Registry owns the startup registration, enable list and immutable snapshots
// of tool providers.
// Registry 负责工具提供方的启动注册、启用列表与不可变快照。
package application

import (
	"context"
	"fmt"
	"strings"

	"github.com/HappyLadySauceM/QQBot/internal/plugins/contracts"
)

// Registry holds registered tools. It is not safe for concurrent mutation:
// registration happens once at startup, snapshots are read afterwards.
// Registry 持有已注册工具。它不支持并发修改：注册只在启动时发生，
// 之后只读取快照。
type Registry struct {
	enabled    map[string]bool
	registered map[string]bool
	providers  []contracts.ToolProvider
	tools      []contracts.RegisteredTool
	byID       map[string]contracts.RegisteredTool
}

// NewRegistry builds an empty registry; an empty enabled list exposes nothing.
// NewRegistry 构建空注册表；启用列表为空时不暴露任何工具。
func NewRegistry(enabled []string) *Registry {
	allow := make(map[string]bool, len(enabled))
	for _, id := range enabled {
		id = strings.TrimSpace(id)
		if id != "" {
			allow[id] = true
		}
	}
	return &Registry{
		enabled:    allow,
		registered: make(map[string]bool),
		byID:       make(map[string]contracts.RegisteredTool),
	}
}

// Register adds one provider. Duplicate provider or tool IDs fail the whole
// registration; the caller closes the registry on failure.
// Register 注册一个提供方。提供方或工具 ID 冲突会让整次注册失败；
// 调用方在失败时关闭注册表以回滚已初始化的资源。
func (r *Registry) Register(ctx context.Context, provider contracts.ToolProvider) error {
	if provider == nil {
		return fmt.Errorf("plugins: provider required")
	}
	manifest := provider.Manifest()
	if strings.TrimSpace(manifest.ID) == "" {
		return fmt.Errorf("plugins: provider id required")
	}
	if r.registered[manifest.ID] {
		return fmt.Errorf("plugins: duplicate provider %q", manifest.ID)
	}
	if !r.enabled[manifest.ID] {
		// A disabled provider is never opened, so there is nothing to close.
		// 未启用的提供方不会被打开，因此无需关闭。
		return nil
	}
	tools, err := provider.Tools(ctx)
	if err != nil {
		_ = provider.Close(ctx)
		return fmt.Errorf("plugins: load tools of %q: %w", manifest.ID, err)
	}
	for _, candidate := range tools {
		if err := validateDescriptor(candidate.Descriptor); err != nil {
			_ = provider.Close(ctx)
			return fmt.Errorf("plugins: provider %q: %w", manifest.ID, err)
		}
		if _, exists := r.byID[candidate.Descriptor.ID]; exists {
			_ = provider.Close(ctx)
			return fmt.Errorf("plugins: duplicate tool %q", candidate.Descriptor.ID)
		}
	}
	r.providers = append(r.providers, provider)
	r.registered[manifest.ID] = true
	for _, candidate := range tools {
		r.tools = append(r.tools, candidate)
		r.byID[candidate.Descriptor.ID] = candidate
	}
	return nil
}

// validateDescriptor enforces the namespace and read-only contract.
// validateDescriptor 强制命名空间与只读契约。
func validateDescriptor(descriptor contracts.ToolDescriptor) error {
	if !strings.Contains(descriptor.ID, ".") {
		return fmt.Errorf("tool id %q must be namespaced", descriptor.ID)
	}
	if strings.TrimSpace(descriptor.Description) == "" {
		return fmt.Errorf("tool %q needs a description", descriptor.ID)
	}
	if descriptor.SideEffect && descriptor.ReadOnly {
		return fmt.Errorf("tool %q cannot be read-only and have side effects", descriptor.ID)
	}
	return nil
}

// Snapshot returns the registered tools; callers receive their own slice so a
// run can never observe a later mutation.
// Snapshot 返回已注册工具；调用方得到独立切片，运行期间不会看到后续修改。
func (r *Registry) Snapshot() []contracts.RegisteredTool {
	out := make([]contracts.RegisteredTool, len(r.tools))
	copy(out, r.tools)
	return out
}

// Close releases providers in reverse registration order.
// Close 按注册的逆序释放提供方。
func (r *Registry) Close(ctx context.Context) error {
	var firstErr error
	for i := len(r.providers) - 1; i >= 0; i-- {
		if err := r.providers[i].Close(ctx); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	r.providers = nil
	r.tools = nil
	r.byID = make(map[string]contracts.RegisteredTool)
	return firstErr
}
