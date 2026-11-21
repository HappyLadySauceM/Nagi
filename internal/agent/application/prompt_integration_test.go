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
			OpenAI: &intentdomain.OpenAIPrompt{SystemPrompt: "自定义聊天系统提示", ReplyStyle: "回答用一句话"},
		}},
	}
	messages, replyPrompt := r.buildMessages(context.Background(), intentdomain.Task{
		BotID: 1, Kind: "group", TargetID: 700, ConversationID: "conv",
		Message: intentdomain.Message{SenderID: 1001, Text: "你好"},
	}, nil, nil)
	if len(messages) == 0 || messages[0].Role != "system" || !strings.Contains(messages[0].Content, "自定义聊天系统提示") {
		t.Fatalf("chat prompt not consumed: %+v", messages)
	}
	if !strings.Contains(messages[0].Content, "回答用一句话") || strings.Contains(messages[0].Content, intentdomain.DefaultReplyStyle) {
		t.Fatal("use the published reply style")
	}
	if !strings.Contains(messages[0].Content, "请简短询问用户或请对方补发") {
		t.Fatal("reply prompt must request clarification when necessary material is missing")
	}
	if replyPrompt.RenderedHash != intentdomain.PromptTextHash(messages[0].Content) {
		t.Fatal("reply prompt hash must match the system content passed to the model")
	}
}
