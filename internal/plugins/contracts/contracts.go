// Package contracts defines the tool provider boundary; helpers live in
// application and framework adapters in infrastructure.
// contracts 定义工具提供方边界；应用逻辑在 application，
// 框架适配在 infrastructure。
package contracts

import (
	"context"
	"time"
)

// Manifest identifies one provider and groups the tools it exposes.
// Manifest 标识一个提供方并归组其暴露的工具。
type Manifest struct {
	ID          string
	Version     string
	Description string
}

// ToolDescriptor describes one tool without leaking framework types.
// ToolDescriptor 描述单个工具，不泄露框架类型。
type ToolDescriptor struct {
	// ID is the namespaced identifier, e.g. qq.get_group_info.
	// ID 是带命名空间的标识，例如 qq.get_group_info。
	ID          string
	Description string
	// ReadOnly marks tools without side effects; only these are exposed today.
	// ReadOnly 标记无副作用的工具；当前只暴露这些。
	ReadOnly   bool
	Permission string
	SideEffect bool
}

// InvokeFunc executes one tool call with JSON-encoded arguments.
// InvokeFunc 以 JSON 参数执行一次工具调用。
type InvokeFunc func(ctx context.Context, argumentsInJSON string) (string, error)

// RegisteredTool is one tool ready to be exposed to the model.
// RegisteredTool 是可供模型调用的单个工具。
type RegisteredTool struct {
	// ProviderID is the manifest id of the provider that supplied the tool; the
	// registry fills it so callers can group tools by provider.
	// ProviderID 是提供该工具的提供方清单 ID；由注册表填充，
	// 调用方可以据此按提供方归组。
	ProviderID string
	Descriptor ToolDescriptor
	Invoke     InvokeFunc
}

// ToolRun summarizes one tool execution for tracing and logs.
// ToolRun 汇总一次工具执行，供追踪与日志使用。
type ToolRun struct {
	ToolID   string
	Status   string
	Duration time.Duration
	Bytes    int
}

// RunRecorder receives the terminal state of every tool execution in one run.
// RunRecorder 接收单次运行中每次工具执行的终态。
type RunRecorder func(ToolRun)

// ToolProvider contributes tools at startup and owns their lifecycle.
// ToolProvider 在启动时提供工具，并负责其生命周期。
type ToolProvider interface {
	Manifest() Manifest
	Tools(ctx context.Context) ([]RegisteredTool, error)
	// Close releases provider resources; implementations must be idempotent.
	// Close 释放提供方资源；实现必须幂等。
	Close(ctx context.Context) error
}
