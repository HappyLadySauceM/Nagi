// Tests for the Eino tool adapter naming and invocation bridge.
// Eino 工具适配层的命名与调用桥接测试。
package eino

import (
	"context"
	"strings"
	"testing"

	"github.com/cloudwego/eino/components/tool"

	"github.com/HappyLadySauceM/QQBot/internal/plugins/contracts"
)

// TestToBaseToolsMapsNames verifies dots become underscores and descriptions
// survive the conversion.
// TestToBaseToolsMapsNames 验证点号转为下划线且描述保留。
func TestToBaseToolsMapsNames(t *testing.T) {
	tools := ToBaseTools([]contracts.RegisteredTool{{
		Descriptor: contracts.ToolDescriptor{
			ID: "qq.get_group_info", Description: "查询当前群", ReadOnly: true,
		},
		Invoke: func(context.Context, string) (string, error) { return "ok", nil },
	}})
	if len(tools) != 1 {
		t.Fatalf("tools = %d", len(tools))
	}
	info, err := tools[0].Info(context.Background())
	if err != nil {
		t.Fatalf("info: %v", err)
	}
	if info.Name != "qq_get_group_info" {
		t.Fatalf("name = %q", info.Name)
	}
	if !strings.Contains(info.Desc, "当前群") {
		t.Fatalf("desc = %q", info.Desc)
	}

	invokable, ok := tools[0].(tool.InvokableTool)
	if !ok {
		t.Fatal("adapter must implement InvokableTool")
	}
	out, err := invokable.InvokableRun(context.Background(), `{"ignored":true}`)
	if err != nil || out != "ok" {
		t.Fatalf("invoke = %q, %v", out, err)
	}
}
