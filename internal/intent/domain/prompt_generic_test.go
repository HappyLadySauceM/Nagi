package domain

import (
	"strings"
	"testing"
)

func TestBuiltinPromptForThreeKinds(t *testing.T) {
	cases := []struct {
		kind     string
		provider string
	}{
		{PromptKindChatReply, PromptProviderOpenAI},
		{PromptKindIntentParticipation, PromptProviderJev},
		{PromptKindIntentParticipation, PromptProviderOpenAI},
		{PromptKindMemoryExtraction, PromptProviderOpenAI},
	}
	for _, tc := range cases {
		p := BuiltinPromptFor(tc.kind, tc.provider)
		if err := ValidatePromptForKind(tc.kind, tc.provider, &p); err != nil {
			t.Fatalf("builtin %s/%s invalid: %v", tc.kind, tc.provider, err)
		}
		if p.Source != PromptSourceBuiltin || p.Version != 1 || p.Hash == "" {
			t.Fatalf("builtin %s/%s = %+v", tc.kind, tc.provider, p)
		}
	}
}

func TestMergePromptContentAppendsSupplements(t *testing.T) {
	account := BuiltinPromptFor(PromptKindIntentParticipation, PromptProviderJev)
	supplement := Prompt{Kind: PromptKindIntentParticipation, Provider: PromptProviderJev, SystemOne: &SystemOnePrompt{Questions: map[string]PromptQuestion{
		"directed_to_bot": {Instructions: "补充：结合最近上下文判断。"},
	}}}
	merged, err := MergePromptContent(PromptKindIntentParticipation, PromptProviderJev, account, supplement)
	if err != nil {
		t.Fatalf("merge: %v", err)
	}
	got := merged.SystemOne.Questions["directed_to_bot"].Instructions
	if !strings.Contains(got, account.SystemOne.Questions["directed_to_bot"].Instructions) || !strings.Contains(got, "补充：") {
		t.Fatalf("merged instruction = %q", got)
	}
	if merged.SystemOne.Questions["directed_to_bot"].Type != "noul" {
		t.Fatalf("question type changed: %+v", merged.SystemOne.Questions["directed_to_bot"])
	}
}

func TestMergePromptContentRejectsSupplementOverride(t *testing.T) {
	account := BuiltinPromptFor(PromptKindMemoryExtraction, PromptProviderOpenAI)
	supplement := Prompt{Kind: PromptKindMemoryExtraction, Provider: PromptProviderOpenAI, OpenAI: &OpenAIPrompt{
		SystemPrompt: "补充", UserTemplate: "{{messages_json}}",
	}}
	if _, err := MergePromptContent(PromptKindMemoryExtraction, PromptProviderOpenAI, account, supplement); err == nil {
		t.Fatal("expected UserTemplate override to be rejected")
	}
}

func TestRenderPromptVariables(t *testing.T) {
	p := BuiltinPromptFor(PromptKindChatReply, PromptProviderOpenAI)
	rendered := RenderPromptVariables(p, map[string]string{"bot_name": "Nagi", "scope_type": "private"})
	if rendered.OpenAI == nil || strings.Contains(rendered.OpenAI.SystemPrompt, "{{") {
		t.Fatalf("unrendered chat prompt: %+v", rendered.OpenAI)
	}
	if !strings.Contains(rendered.OpenAI.SystemPrompt, "Nagi") || !strings.Contains(rendered.OpenAI.SystemPrompt, "private") {
		t.Fatalf("rendered chat prompt = %q", rendered.OpenAI.SystemPrompt)
	}
}

func TestPromptVariableValidation(t *testing.T) {
	chat := BuiltinPromptFor(PromptKindChatReply, PromptProviderOpenAI)
	chat.OpenAI.SystemPrompt = "hello {{unknown}}"
	if err := ValidatePromptForKind(PromptKindChatReply, PromptProviderOpenAI, &chat); err == nil {
		t.Fatal("unknown chat variable accepted")
	}

	memory := BuiltinPromptFor(PromptKindMemoryExtraction, PromptProviderOpenAI)
	memory.OpenAI.UserTemplate = "{{messages_json}} {{messages_json}}"
	if err := ValidatePromptForKind(PromptKindMemoryExtraction, PromptProviderOpenAI, &memory); err == nil {
		t.Fatal("duplicate messages_json accepted")
	}

	memorySupplement := EmptyPromptSupplement(PromptKindMemoryExtraction, PromptProviderOpenAI)
	memorySupplement.OpenAI.SystemPrompt = "{{messages_json}}"
	if err := ValidatePromptSupplement(PromptKindMemoryExtraction, PromptProviderOpenAI, &memorySupplement); err == nil {
		t.Fatal("restricted supplement variable accepted")
	}

	intentSupplement := EmptyPromptSupplement(PromptKindIntentParticipation, PromptProviderJev)
	intentSupplement.SystemOne.Questions["directed_to_bot"] = PromptQuestion{Instructions: "{{question}}"}
	if err := ValidatePromptSupplement(PromptKindIntentParticipation, PromptProviderJev, &intentSupplement); err == nil {
		t.Fatal("restricted intent supplement variable accepted")
	}

	intentOpenAI := BuiltinPromptFor(PromptKindIntentParticipation, PromptProviderOpenAI)
	intentOpenAI.OpenAI.UserTemplate = "{{state}}"
	if err := ValidatePromptForKind(PromptKindIntentParticipation, PromptProviderOpenAI, &intentOpenAI); err == nil {
		t.Fatal("missing question placeholder accepted")
	}
}

func TestPromptFinalHashIncludesLayers(t *testing.T) {
	p := BuiltinPromptFor(PromptKindChatReply, PromptProviderOpenAI)
	p.Layers = []PromptLayer{{Layer: PromptLayerAccountDefault, VersionID: 1, Version: 1, Source: PromptSourceLocal, Hash: "a"}}
	first := PromptFinalHash(p)
	p.Layers[0].Hash = "b"
	if PromptFinalHash(p) == first {
		t.Fatal("layer hash did not affect FinalHash")
	}
}

func TestValidatePromptForKindWithVariablesAllowsExtra(t *testing.T) {
	// 自定义变量只在显式传入 extra 时被接受；不传时仍按未知变量拒绝。
	p := BuiltinPromptFor(PromptKindChatReply, PromptProviderOpenAI)
	p.OpenAI.SystemPrompt = "你好 {{user_name}}，你是 {{bot_name}}。"
	if err := ValidatePromptForKind(PromptKindChatReply, PromptProviderOpenAI, &p); err == nil {
		t.Fatal("unknown variable accepted without extra set")
	}
	if err := ValidatePromptForKindWithVariables(PromptKindChatReply, PromptProviderOpenAI, &p, []string{"user_name"}); err != nil {
		t.Fatalf("extra variable rejected: %v", err)
	}
}

func TestValidatePromptSupplementWithVariablesAllowsExtra(t *testing.T) {
	// 补充层同样只在显式传入 extra 时接受自定义变量。
	supplement := EmptyPromptSupplement(PromptKindChatReply, PromptProviderOpenAI)
	supplement.OpenAI.SystemPrompt = "补充 {{user_name}}"
	if err := ValidatePromptSupplement(PromptKindChatReply, PromptProviderOpenAI, &supplement); err == nil {
		t.Fatal("unknown supplement variable accepted without extra set")
	}
	if err := ValidatePromptSupplementWithVariables(PromptKindChatReply, PromptProviderOpenAI, &supplement, []string{"user_name"}); err != nil {
		t.Fatalf("extra supplement variable rejected: %v", err)
	}
}

func TestMergePromptContentWithVariablesAllowsExtra(t *testing.T) {
	// 合并时 account 与 supplement 的自定义变量都按 extra 校验。
	account := BuiltinPromptFor(PromptKindChatReply, PromptProviderOpenAI)
	account.OpenAI.SystemPrompt = "你是 {{bot_name}}，称呼用户为 {{user_name}}。"
	supplement := EmptyPromptSupplement(PromptKindChatReply, PromptProviderOpenAI)
	supplement.OpenAI.SystemPrompt = "补充：关注 {{topic}}"
	if _, err := MergePromptContent(PromptKindChatReply, PromptProviderOpenAI, account, supplement); err == nil {
		t.Fatal("custom variables accepted without extra set")
	}
	merged, err := MergePromptContentWithVariables(PromptKindChatReply, PromptProviderOpenAI, account, supplement, []string{"user_name", "topic"})
	if err != nil {
		t.Fatalf("merge: %v", err)
	}
	if !strings.Contains(merged.OpenAI.SystemPrompt, "{{user_name}}") || !strings.Contains(merged.OpenAI.SystemPrompt, "{{topic}}") {
		t.Fatalf("merged prompt = %q", merged.OpenAI.SystemPrompt)
	}
}

func TestRenderPromptVariablesReplacesCustomVariable(t *testing.T) {
	// 渲染会替换自定义变量，且不再残留占位符。
	p := BuiltinPromptFor(PromptKindChatReply, PromptProviderOpenAI)
	p.OpenAI.SystemPrompt = "你好 {{user_name}}"
	rendered := RenderPromptVariables(p, map[string]string{"user_name": "小明"})
	if strings.Contains(rendered.OpenAI.SystemPrompt, "{{") || !strings.Contains(rendered.OpenAI.SystemPrompt, "小明") {
		t.Fatalf("rendered = %q", rendered.OpenAI.SystemPrompt)
	}
}

func TestParsePromptContentForWithVariablesAllowsExtra(t *testing.T) {
	// 存储内容解析同样需要在 extra 允许时接受自定义变量。
	p := BuiltinPromptFor(PromptKindChatReply, PromptProviderOpenAI)
	p.OpenAI.SystemPrompt = "你好 {{user_name}}"
	content := PromptContentJSON(p)
	if _, err := ParsePromptContentFor(PromptKindChatReply, PromptProviderOpenAI, content); err == nil {
		t.Fatal("stored content with custom variable accepted without extra")
	}
	parsed, err := ParsePromptContentForWithVariables(PromptKindChatReply, PromptProviderOpenAI, content, []string{"user_name"})
	if err != nil {
		t.Fatalf("parse with extra: %v", err)
	}
	if !strings.Contains(parsed.OpenAI.SystemPrompt, "{{user_name}}") {
		t.Fatalf("parsed = %+v", parsed.OpenAI)
	}
}
