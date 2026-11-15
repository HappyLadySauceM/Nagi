package application

import (
	"context"
	"strings"
	"testing"

	intentdomain "github.com/HappyLadySauceM/Nagi/internal/intent/domain"
)

type fakeChatPromptResolver struct {
	prompt intentdomain.Prompt
}

func (f fakeChatPromptResolver) ResolveFor(context.Context, string, string, string, int64, string, int64, map[string]string) intentdomain.Prompt {
	return f.prompt
}

func TestBuildMessagesConsumesChatPrompt(t *testing.T) {
	r := &Replier{
		botName: "Nagi",
		prompts: fakeChatPromptResolver{prompt: intentdomain.Prompt{
			OpenAI: &intentdomain.OpenAIPrompt{SystemPrompt: "自定义聊天系统提示"},
		}},
	}
	messages := r.buildMessages(context.Background(), intentdomain.Task{
		BotID: 1, Kind: "group", TargetID: 700, ConversationID: "conv",
		Message: intentdomain.Message{SenderID: 1001, Text: "你好"},
	}, nil, nil)
	if len(messages) == 0 || messages[0].Role != "system" || !strings.Contains(messages[0].Content, "自定义聊天系统提示") {
		t.Fatalf("chat prompt not consumed: %+v", messages)
	}
}
