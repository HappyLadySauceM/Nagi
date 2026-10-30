// Eino adapter for registered tools: the only place where plugin contracts meet
// the agent framework.
// 已注册工具的 Eino 适配层：插件契约与 Agent 框架唯一交汇的地方。
package eino

import (
	"context"
	pluginsapp "github.com/HappyLadySauceM/Nagi/internal/plugins/application"
	"github.com/cloudwego/eino/compose"
	"strings"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"

	"github.com/HappyLadySauceM/Nagi/internal/plugins/contracts"
)

// invokableTool adapts one registered tool to the Eino tool interface.
// invokableTool 把单个已注册工具适配为 Eino 工具接口。
type invokableTool struct {
	info   *schema.ToolInfo
	invoke contracts.InvokeFunc
}

// Info implements tool.BaseTool.
// Info 实现 tool.BaseTool。
func (t invokableTool) Info(context.Context) (*schema.ToolInfo, error) { return t.info, nil }

// InvokableRun implements tool.InvokableTool; arguments stay JSON text so the
// guard can bound and log them without schema coupling.
// InvokableRun 实现 tool.InvokableTool；参数保持 JSON 文本，
// 守卫可在不解耦 schema 的情况下限流与记录。
func (t invokableTool) InvokableRun(ctx context.Context, argumentsInJSON string, _ ...tool.Option) (string, error) {
	return t.invoke(pluginsapp.WithToolCallID(ctx, compose.GetToolCallID(ctx)), argumentsInJSON)
}

// ToBaseTools converts registered tools into Eino tools. Dots are not allowed in
// model-facing tool names, so the namespace separator becomes an underscore.
// ToBaseTools 把已注册工具转换为 Eino 工具。模型侧工具名不允许点号，
// 因此命名空间分隔符改为下划线。
func ToBaseTools(tools []contracts.RegisteredTool) []tool.BaseTool {
	out := make([]tool.BaseTool, 0, len(tools))
	for _, registered := range tools {
		params := make(map[string]*schema.ParameterInfo)
		for name, p := range registered.Descriptor.Parameters {
			params[name] = &schema.ParameterInfo{Type: schema.DataType(p.Type), Desc: p.Description, Required: p.Required}
		}
		out = append(out, invokableTool{
			info: &schema.ToolInfo{
				Name:        toolName(registered.Descriptor.ID),
				Desc:        registered.Descriptor.Description,
				ParamsOneOf: schema.NewParamsOneOfByParams(params),
			},
			invoke: registered.Invoke,
		})
	}
	return out
}

// toolName maps a namespaced id to a model-safe tool name.
// toolName 把带命名空间的 ID 映射为模型安全的工具名。
func toolName(id string) string {
	return strings.ReplaceAll(id, ".", "_")
}
