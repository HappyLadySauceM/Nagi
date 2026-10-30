// Registry owns the startup registration and immutable snapshots of tool
// providers. Every provider supplied at startup is loaded: availability is
// decided per conversation by the capability grant, never by a process switch.
// Registry 负责工具提供方的启动注册与不可变快照。
// 启动时提供的每个提供方都会被加载：可用性按会话的能力授权判定，
// 不存在进程级开关。
package application

import (
	"context"
	"fmt"
	"strings"

	"github.com/HappyLadySauceM/Nagi/internal/plugins/contracts"
)

// Registry holds registered tools. It is not safe for concurrent mutation:
// registration happens once at startup, snapshots are read afterwards.
// Registry 持有已注册工具。它不支持并发修改：注册只在启动时发生，
// 之后只读取快照。
type Registry struct {
	registered map[string]bool
	providers  []contracts.ToolProvider
	manifests  []contracts.Manifest
	tools      []contracts.RegisteredTool
	byID       map[string]contracts.RegisteredTool
}

// NewRegistry builds an empty registry; every provider registered afterwards is
// immediately available.
// NewRegistry 构建空注册表；此后注册的每个提供方都会立即可用。
func NewRegistry() *Registry {
	return &Registry{
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
	r.registered[manifest.ID] = true
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
	r.manifests = append(r.manifests, manifest)
	for _, candidate := range tools {
		candidate.ProviderID = manifest.ID
		r.tools = append(r.tools, candidate)
		r.byID[candidate.Descriptor.ID] = candidate
	}
	return nil
}

// validateDescriptor enforces the namespace, read-only, risk level and scope
// kind contract.
// validateDescriptor 强制命名空间、只读、风险级别与会话类型契约。
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
	// An unknown risk level would bypass the read/write policy checks below;
	// reject it so a typo cannot weaken the declared impact.
	// 未知风险级别会绕过下面的读写策略校验；
	// 直接拒绝，避免拼写错误削弱声明的影响级别。
	switch descriptor.RiskLevel {
	case "", contracts.RiskRead, contracts.RiskWrite, contracts.RiskAdmin:
	default:
		return fmt.Errorf("tool %q has invalid risk level %q", descriptor.ID, descriptor.RiskLevel)
	}
	// A read risk level on a side-effecting tool would let the policy treat it
	// as safe; reject the contradiction instead of guessing.
	// 有副作用的工具标注只读风险会被策略当成安全工具；
	// 直接拒绝矛盾描述符，而不是猜测意图。
	if descriptor.RiskLevel == contracts.RiskRead && !descriptor.ReadOnly {
		return fmt.Errorf("tool %q declares read risk but is not read-only", descriptor.ID)
	}
	for _, kind := range descriptor.ScopeKinds {
		if kind != contracts.ScopeKindPrivate && kind != contracts.ScopeKindGroup {
			return fmt.Errorf("tool %q has invalid scope kind %q", descriptor.ID, kind)
		}
	}
	return nil
}

// Snapshot returns the registered tools. The returned slice is independent, so
// replacing entries cannot affect the registry, but each descriptor still shares
// its ScopeKinds backing array with the registered copy.
// Snapshot 返回已注册工具。返回的切片本身独立，替换元素不会影响注册表，
// 但每个描述符的 ScopeKinds 底层数组仍与注册时的副本共享。
func (r *Registry) Snapshot() []contracts.RegisteredTool {
	out := make([]contracts.RegisteredTool, len(r.tools))
	copy(out, r.tools)
	return out
}

// Manifests returns the manifests of providers that were actually registered.
// Manifests 返回实际注册成功的提供方清单。
func (r *Registry) Manifests() []contracts.Manifest {
	out := make([]contracts.Manifest, len(r.manifests))
	copy(out, r.manifests)
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
	r.manifests = nil
	r.tools = nil
	r.byID = make(map[string]contracts.RegisteredTool)
	return firstErr
}
