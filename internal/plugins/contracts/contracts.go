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

// RiskLevel grades what a tool can do to the conversation it runs in.
// RiskLevel 划分工具对其所在会话的影响级别。
type RiskLevel string

// Supported risk levels; an omitted value is derived from ReadOnly.
// 支持的风险级别；省略时按 ReadOnly 推导。
const (
	RiskRead  RiskLevel = "read"
	RiskWrite RiskLevel = "write"
	RiskAdmin RiskLevel = "admin"
)

// Conversation kinds a tool may declare in ScopeKinds.
// 工具可在 ScopeKinds 中声明的会话类型。
const (
	ScopeKindPrivate = "private"
	ScopeKindGroup   = "group"
)

// Tool categories reserved for a later catalog API and frontend grouping.
// 预留给后续目录 API 与前端分组的工具分类。
const (
	CategoryGroup  = "group"
	CategorySystem = "system"
	CategoryReply  = "reply"
)

// ToolDescriptor describes one tool without leaking framework types.
// ToolDescriptor 描述单个工具，不泄露框架类型。
type ToolDescriptor struct {
	// ID is the namespaced identifier, e.g. qq.get_group_info.
	// ID 是带命名空间的标识，例如 qq.get_group_info。
	ID           string
	Description  string
	Parameters   map[string]Parameter
	ReplyCurrent bool
	// Category groups tools for a later catalog API and the frontend.
	// Category 为后续目录 API 与前端分组归类工具。
	Category string
	// ScopeKinds lists the conversation kinds the tool applies to; empty means
	// every kind, so descriptors that omit it keep their previous visibility.
	// ScopeKinds 列出工具适用的会话类型；为空表示全部类型，
	// 省略该字段的描述符保持原有可见性。
	ScopeKinds []string
	// RiskLevel grades what the tool can do; when empty it is derived from
	// ReadOnly.
	// RiskLevel 标注工具的影响级别；为空时按 ReadOnly 推导。
	RiskLevel RiskLevel
	// ReadOnly marks tools without side effects; only these are exposed today.
	// ReadOnly 标记无副作用的工具；当前只暴露这些。
	ReadOnly   bool
	Permission string
	SideEffect bool
}

// AppliesTo reports whether the tool is usable in the given conversation kind.
// An empty ScopeKinds applies everywhere; a kind-restricted tool never matches
// an empty kind, so a run without a known kind cannot reach it.
// AppliesTo 报告工具是否适用于给定会话类型。ScopeKinds 为空时适用于全部类型；
// 限定类型的工具不会匹配空的会话类型，因此类型未知的运行无法触达它。
func (d ToolDescriptor) AppliesTo(kind string) bool {
	if len(d.ScopeKinds) == 0 {
		return true
	}
	for _, candidate := range d.ScopeKinds {
		if candidate == kind {
			return true
		}
	}
	return false
}

// EffectiveRiskLevel derives the level from ReadOnly when RiskLevel is empty:
// read-only tools are RiskRead, the rest are RiskWrite. A forgotten field can
// therefore never downgrade a side-effecting tool to read-only.
// EffectiveRiskLevel 在 RiskLevel 为空时按 ReadOnly 推导：只读工具为 RiskRead，
// 其余为 RiskWrite，漏填字段因此不会把有副作用的工具降级为只读。
func (d ToolDescriptor) EffectiveRiskLevel() RiskLevel {
	if d.RiskLevel != "" {
		return d.RiskLevel
	}
	if d.ReadOnly {
		return RiskRead
	}
	return RiskWrite
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
	ErrorCode string
	ToolID    string
	Status    string
	Duration  time.Duration
	Bytes     int
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

// Parameter is a framework-independent tool input definition.
// Parameter 是与框架无关的工具参数定义。
type Parameter struct {
	Type        string
	Description string
	Required    bool
}
