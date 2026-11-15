package infrastructure

import (
	"context"
	"testing"

	intentdomain "github.com/HappyLadySauceM/Nagi/internal/intent/domain"
	"github.com/HappyLadySauceM/Nagi/internal/memory/domain"
)

type fakeMemoryPromptProvider struct {
	prompt intentdomain.Prompt
}

func (f fakeMemoryPromptProvider) ResolveFor(context.Context, string, string, string, int64, string, int64, map[string]string) intentdomain.Prompt {
	return f.prompt
}

func TestModelExtractorResolvesEditablePrompt(t *testing.T) {
	extractor := NewModelExtractor(nil, fakeMemoryPromptProvider{prompt: intentdomain.Prompt{OpenAI: &intentdomain.OpenAIPrompt{
		SystemPrompt: "自定义抽取 System",
		UserTemplate: "消息：{{messages_json}}",
	}}})
	system, user := extractor.resolvePrompt(context.Background(), domain.ExtractionRequest{
		Scope:   domain.ExtractionScope{BotID: 1, ScopeType: "group", ScopeID: 700},
		BotName: "Nagi",
	})
	if system != "自定义抽取 System" || user != "消息：{{messages_json}}" {
		t.Fatalf("resolved prompt = %q / %q", system, user)
	}
}
