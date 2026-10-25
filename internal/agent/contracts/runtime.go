// Package contracts defines the business boundary between the reply pipeline
// and the agent runtime; implementations live in infrastructure so this package
// never imports a specific agent framework.
// contracts 定义回复流水线与 Agent 运行时之间的业务边界；实现放在
// infrastructure，本包不导入任何具体 Agent 框架。
package contracts

import (
	"context"
	"time"
)

// Message is one chat turn handed to the runtime, including the system prompt.
// Message 是交给运行时的一轮对话，包含系统提示。
type Message struct {
	Role    string
	Content string
}

// Request is one reply generation request scoped to a single message.
// Request 是一次回复生成请求，作用域绑定单条消息。
type Request struct {
	BotID          int64
	ConversationID string
	Kind           string
	TargetID       int64
	SenderID       int64
	MessageID      int64
	JobID          int64
	// Messages already contains the system prompt, authorized history and the
	// current message; the runtime must not add business context of its own.
	// Messages 已包含系统提示、授权历史与当前消息；运行时不自行补充业务上下文。
	Messages []Message
	// PermissionOverrides carries the scope policy overrides of tool
	// permissions; the tool resolver merges them with the configured baseline.
	// PermissionOverrides 携带作用域策略中的工具权限覆盖；
	// 工具解析器把它与配置基线合并。
	PermissionOverrides map[string]bool
	// MaxOutputTokens, MaxSteps and Timeout bound one run; zero falls back to
	// the configured defaults.
	// MaxOutputTokens、MaxSteps 与 Timeout 限定单次运行；零值使用配置默认。
	MaxOutputTokens int
	MaxSteps        int
	Timeout         time.Duration
	// OnToolRun receives one completed tool run; nil disables reporting.
	// OnToolRun 接收一次已完成的工具运行；nil 表示不上报。
	OnToolRun func(ToolRun)
}

// ToolRun summarizes one tool execution for tracing and logs.
// ToolRun 汇总一次工具执行，供追踪与日志使用。
type ToolRun struct {
	ToolID   string
	Status   string
	Duration time.Duration
	Bytes    int
}

// Result is the generated reply plus the metadata worth persisting or tracing.
// Result 是生成的回复以及值得持久化或追踪的元数据。
type Result struct {
	Text        string
	Model       string
	InputUsage  int64
	OutputUsage int64
	ToolRuns    []ToolRun
	// ToolsExposed is how many tools the model could choose from in this run.
	// ToolsExposed 是本次运行中模型可选择的工具数量。
	ToolsExposed int
}

// Runtime generates one reply; implementations must honor ctx cancellation and
// return a terminal error rather than a partial reply.
// Runtime 生成一条回复；实现必须尊重 ctx 取消，并返回终态错误而不是半截回复。
type Runtime interface {
	Run(ctx context.Context, req Request) (Result, error)
}
