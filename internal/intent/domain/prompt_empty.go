package domain

// EmptyPromptSupplement returns a valid empty scope supplement for one kind.
func EmptyPromptSupplement(kind, provider string) Prompt {
	p := Prompt{Kind: kind, Provider: provider}
	switch kind {
	case PromptKindChatReply, PromptKindMemoryExtraction, PromptKindMemoryRecall:
		p.OpenAI = &OpenAIPrompt{SystemPrompt: ""}
	case PromptKindIntentParticipation:
		if provider == PromptProviderJev {
			p.SystemOne = &SystemOnePrompt{Questions: map[string]PromptQuestion{}}
		} else {
			p.OpenAI = &OpenAIPrompt{SystemPrompt: ""}
		}
	}
	return p
}
