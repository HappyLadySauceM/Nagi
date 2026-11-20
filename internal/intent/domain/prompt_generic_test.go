package domain

import (
	"strings"
	"testing"
)

func TestBuiltinPromptForSupportedKinds(t *testing.T) {
	cases := []struct {
		kind     string
		provider string
	}{
		{PromptKindChatReply, PromptProviderOpenAI},
		{PromptKindIntentParticipation, PromptProviderJev},
		{PromptKindIntentParticipation, PromptProviderOpenAI},
		{PromptKindMemoryExtraction, PromptProviderOpenAI},
		{PromptKindMemoryRecall, PromptProviderOpenAI},
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

func TestPromptVariableAllowSetAcceptsRuntimeVariables(t *testing.T) {
	// 基础允许集应接受运行时提供的 bot_id/target_id/sender_id，与 bot_name、scope_type 同级。
	p := BuiltinPromptFor(PromptKindChatReply, PromptProviderOpenAI)
	p.OpenAI.SystemPrompt = "{{bot_name}} {{scope_type}} {{bot_id}} {{target_id}} {{sender_id}}"
	if err := ValidatePromptForKind(PromptKindChatReply, PromptProviderOpenAI, &p); err != nil {
		t.Fatalf("runtime variables rejected: %v", err)
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

// validIntentQuestion builds one intent score question.
// validIntentQuestion 构造一个 intent 分数题。
func validIntentQuestion(instructions string) PromptQuestion {
	return PromptQuestion{
		Type: "noul", Role: QuestionRoleIntent, Instructions: instructions,
	}
}

// TestValidateSystemOnePromptSupportsAddedAndRemovedQuestions verifies a prompt
// may drop standard roles and add custom noul questions without six fixed slots.
// TestValidateSystemOnePromptSupportsAddedAndRemovedQuestions 验证 Prompt 可删除标准角色、
// 新增 custom noul 问题，而不再强制五个固定槽位。
func TestValidateSystemOnePromptSupportsAddedAndRemovedQuestions(t *testing.T) {
	p := &SystemOnePrompt{Questions: map[string]PromptQuestion{
		"directed_to_bot": {
			Type: "noul", Role: QuestionRoleDirectedToBot, Weight: 2,
			Instructions: "是否在对机器人说话？",
		},
		"intent":  validIntentQuestion("当前意图？"),
		"urgency": {Type: "noul", Role: QuestionRoleCustom, Weight: 5, Instructions: "是否紧急？"},
	}}
	if err := ValidateSystemOnePrompt(p); err != nil {
		t.Fatalf("added/removed question set rejected: %v", err)
	}
}

// TestValidateSystemOnePromptRejectsInvalidSets covers the documented limits.
// TestValidateSystemOnePromptRejectsInvalidSets 覆盖文档规定的各项限制。
func TestValidateSystemOnePromptRejectsInvalidSets(t *testing.T) {
	tooMany := make(map[string]PromptQuestion, 21)
	for i := 0; i < 21; i++ {
		key := "custom_q" + string(rune('a'+i))
		tooMany[key] = PromptQuestion{Type: "noul", Role: QuestionRoleCustom, Weight: 1, Instructions: "问题"}
	}
	cases := map[string]*SystemOnePrompt{
		"empty":    {Questions: map[string]PromptQuestion{}},
		"too many": {Questions: tooMany},
		"bad key":  {Questions: map[string]PromptQuestion{"1bad": {Type: "noul", Role: QuestionRoleCustom, Instructions: "x"}}},
		"empty instructions": {Questions: map[string]PromptQuestion{
			"note": {Type: "noul", Role: QuestionRoleCustom, Instructions: "  "},
		}},
		"intent missing closing": {Questions: map[string]PromptQuestion{
			"intent": {Type: "choice", Role: QuestionRoleIntent, Instructions: "x", Criteria: map[string]string{"chat": "闲聊"}},
		}},
		"custom choice": {Questions: map[string]PromptQuestion{
			"note": {Type: "choice", Role: QuestionRoleCustom, Instructions: "x", Criteria: map[string]string{"a": "b"}},
		}},
		"duplicate role": {Questions: map[string]PromptQuestion{
			"directed_to_bot": {Type: "noul", Role: QuestionRoleDirectedToBot, Instructions: "x"},
			"directed_again":  {Type: "noul", Role: QuestionRoleDirectedToBot, Instructions: "y"},
		}},
		"weight out of range": {Questions: map[string]PromptQuestion{
			"note": {Type: "noul", Role: QuestionRoleCustom, Weight: 11, Instructions: "x"},
		}},
		"unknown role": {Questions: map[string]PromptQuestion{
			"note": {Type: "noul", Role: "weird", Instructions: "x"},
		}},
	}
	for name, p := range cases {
		if err := ValidateSystemOnePrompt(p); err == nil {
			t.Fatalf("%s: expected rejection", name)
		}
	}
}

// TestLegacyQuestionRolesInferred verifies prompts stored before Role/Weight keep
// working through key-based role inference and default weights.
// TestLegacyQuestionRolesInferred 验证 Role/Weight 之前存储的 Prompt 通过 key 推断角色与
// 默认权重继续可用。
func TestLegacyQuestionRolesInferred(t *testing.T) {
	legacy := &SystemOnePrompt{Questions: map[string]PromptQuestion{
		"directed_to_bot":      {Type: "noul", Instructions: "a"},
		"continues_bot_thread": {Type: "noul", Instructions: "b"},
		"addressed_to_other":   {Type: "noul", Instructions: "c"},
		"appropriate_to_join":  {Type: "noul", Instructions: "d"},
		"intent": {
			Type: "choice", Instructions: "e",
			Criteria: map[string]string{"question": "q", "chat": "c", "command": "m", "closing": "x", "other": "o"},
		},
	}}
	if err := ValidateSystemOnePrompt(legacy); err != nil {
		t.Fatalf("legacy prompt rejected: %v", err)
	}
	if got := EffectiveQuestionRole("directed_to_bot", legacy.Questions["directed_to_bot"]); got != QuestionRoleDirectedToBot {
		t.Fatalf("directed role = %q", got)
	}
	if got := EffectiveQuestionWeight("directed_to_bot", legacy.Questions["directed_to_bot"]); got != 1 {
		t.Fatalf("directed weight = %v, want 1", got)
	}
	if got := EffectiveQuestionWeight("addressed_to_other", legacy.Questions["addressed_to_other"]); got != -1 {
		t.Fatalf("addressed weight = %v, want -1", got)
	}
	if got := EffectiveQuestionRole("custom_extra", PromptQuestion{Type: "noul"}); got != QuestionRoleCustom {
		t.Fatalf("unknown key role = %q, want custom", got)
	}
}

// TestBuiltinQuestionsCarryRolesAndWeights verifies the built-in five questions
// are explicit so their behavior does not depend on inference.
// TestBuiltinQuestionsCarryRolesAndWeights 验证内置五问显式带角色与权重，不依赖推断。
func TestBuiltinQuestionsCarryRolesAndWeights(t *testing.T) {
	questions := BuiltinPrompt(PromptProviderJev).SystemOne.Questions
	want := map[string]struct {
		role   string
		weight float64
	}{
		"directed_to_bot":      {QuestionRoleDirectedToBot, 1},
		"continues_bot_thread": {QuestionRoleContinuesBotThread, 1},
		"addressed_to_other":   {QuestionRoleAddressedToOther, -1},
		"appropriate_to_join":  {QuestionRoleAppropriateToJoin, 1},
		"intent":               {QuestionRoleIntent, 0},
	}
	for key, expected := range want {
		q, ok := questions[key]
		if !ok {
			t.Fatalf("missing built-in question %q", key)
		}
		if q.Role != expected.role || q.Weight != expected.weight {
			t.Fatalf("question %q role/weight = %q/%v, want %q/%v", key, q.Role, q.Weight, expected.role, expected.weight)
		}
	}
}
