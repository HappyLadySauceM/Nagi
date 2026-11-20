package eino

import (
	"context"
	"fmt"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/components/tool/utils"
	"github.com/cloudwego/eino/schema"

	"github.com/HappyLadySauceM/Nagi/internal/agent/contracts"
)

const (
	selectTopicTool = "select_group_topic"
	waitTool        = "wait"
)

type attentionTurn struct {
	adk.BaseChatModelAgentMiddleware
	request  contracts.Request
	model    string
	selected bool
	reply    bool
	ready    bool
	tools    []*schema.ToolInfo
}

type topicSelection struct {
	Action  string `json:"action" jsonschema:"enum=follow_up,enum=open"`
	TopicID int64  `json:"topic_id" jsonschema:"对应话题 ID，开启新话题时填 0"`
}

type waitSelection struct {
	Action  string `json:"action" jsonschema:"enum=wait,enum=close,enum=personal_exit"`
	TopicID int64  `json:"topic_id" jsonschema:"结束或离开的话题 ID，旁听时填 0"`
}

func (t *attentionTurn) selectAction(ctx context.Context, action string, id int64) (string, error) {
	if t.selected {
		return "已经确定本轮动作", nil
	}
	reply, history, err := t.request.Attention(ctx, action, id, t.model)
	if err != nil {
		return "", err
	}
	t.selected, t.reply = true, reply
	if !reply {
		return "本轮结束", nil
	}
	return "本轮允许回复。所选话题的记录如下，仅供参考。\n" + history, nil
}

func (t *attentionTurn) controlTools() ([]tool.BaseTool, error) {
	selectTopic, err := utils.InferTool(selectTopicTool, "续接一个现有话题或开启新话题。成功后可以回复。", func(ctx context.Context, args *topicSelection) (string, error) {
		action := args.Action
		if action != "follow_up" && action != "open" {
			action = "invalid"
		}
		return t.selectAction(ctx, action, args.TopicID)
	})
	if err != nil {
		return nil, err
	}
	wait, err := utils.InferTool(waitTool, "旁听、结束整个话题或让当前发言人离开话题，然后释放本轮。", func(ctx context.Context, args *waitSelection) (string, error) {
		action := args.Action
		if action != "wait" && action != "close" && action != "personal_exit" {
			action = "invalid"
		}
		return t.selectAction(ctx, action, args.TopicID)
	})
	if err != nil {
		return nil, err
	}
	return []tool.BaseTool{selectTopic, wait}, nil
}

func (t *attentionTurn) BeforeModelRewriteState(ctx context.Context, state *adk.ChatModelAgentState, _ *adk.ModelContext) (context.Context, *adk.ChatModelAgentState, error) {
	if t.tools == nil {
		t.tools = state.ToolInfos
	}
	t.ready = t.reply
	infos := make([]*schema.ToolInfo, 0, len(t.tools))
	for _, info := range t.tools {
		control := info.Name == selectTopicTool || info.Name == waitTool
		if (!t.selected && control) || (t.ready && !control && info.Name != "history_search") {
			infos = append(infos, info)
		}
	}
	state.ToolInfos = infos
	return ctx, state, nil
}

func (t *attentionTurn) toolAllowed(name string) bool {
	if name == selectTopicTool || name == waitTool {
		return !t.selected
	}
	return t.ready && name != "history_search"
}

func (t *attentionTurn) AfterModelRewriteState(ctx context.Context, state *adk.ChatModelAgentState, _ *adk.ModelContext) (context.Context, *adk.ChatModelAgentState, error) {
	message := state.Messages[len(state.Messages)-1]
	if !t.ready && len(message.ToolCalls) > 1 {
		return ctx, state, fmt.Errorf("agent: multiple attention actions")
	}
	for _, call := range message.ToolCalls {
		if !t.toolAllowed(call.Function.Name) {
			return ctx, state, fmt.Errorf("agent: unavailable tool in attention turn")
		}
	}
	return ctx, state, nil
}
