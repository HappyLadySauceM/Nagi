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
