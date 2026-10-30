// Built-in time tool: proves the Agent → Tool → result loop without touching
// any external system.
// 内置时间工具：不接触任何外部系统即可验证 Agent → Tool → 结果闭环。
package builtin

import (
	"context"
	"time"

	"github.com/HappyLadySauceM/Nagi/internal/plugins/contracts"
)

// timeProvider exposes system.current_time.
// timeProvider 暴露 system.current_time。
type timeProvider struct{}

// NewTimeProvider builds the provider.
// NewTimeProvider 构建提供方。
func NewTimeProvider() contracts.ToolProvider { return timeProvider{} }

// Manifest identifies the provider.
// Manifest 标识提供方。
func (timeProvider) Manifest() contracts.Manifest {
	return contracts.Manifest{ID: "builtin.time", Version: "1", Description: "受控时间信息"}
}

// Tools returns the current-time tool.
// Tools 返回当前时间工具。
func (timeProvider) Tools(context.Context) ([]contracts.RegisteredTool, error) {
	return []contracts.RegisteredTool{{
		Descriptor: contracts.ToolDescriptor{
			ID:          "system.current_time",
			Permission:  "system.time.read",
			Description: "返回服务器当前时间（RFC3339，含时区），不需要任何参数",
			Category:    contracts.CategorySystem,
			RiskLevel:   contracts.RiskRead,
			ReadOnly:    true,
		},
		Invoke: func(context.Context, string) (string, error) {
			now := time.Now()
			return now.Format(time.RFC3339) + " (" + now.Format("MST") + ")", nil
		},
	}}, nil
}

// Close is a no-op for the built-in provider.
// Close 对内置提供方是空操作。
func (timeProvider) Close(context.Context) error { return nil }
