// Package contracts defines the tool provider boundary; helpers live in
// application and framework adapters in infrastructure.
// contracts 定义工具提供方边界；应用逻辑在 application，
// 框架适配在 infrastructure。
package contracts

import (
	"context"
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
	Descriptor ToolDescriptor
	Invoke     InvokeFunc
}

// ToolProvider contributes tools at startup and owns their lifecycle.
// ToolProvider 在启动时提供工具，并负责其生命周期。
type ToolProvider interface {
	Manifest() Manifest
	Tools(ctx context.Context) ([]RegisteredTool, error)
	// Close releases provider resources; implementations must be idempotent.
	// Close 释放提供方资源；实现必须幂等。
	Close(ctx context.Context) error
}
