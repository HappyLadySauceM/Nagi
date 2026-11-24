package infrastructure

import (
	"bytes"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/HappyLadySauceM/Nagi/internal/intent/domain"
)

type promptIntentContextWire struct {
	Identity         string   `json:"identity"`
	Responsibilities []string `json:"responsibilities"`
	GroupConventions []string `json:"group_conventions"`
}

func (w *promptIntentContextWire) value() *domain.PromptIntentContext {
	if w == nil {
		return nil
	}
	return &domain.PromptIntentContext{Identity: w.Identity, Responsibilities: w.Responsibilities, GroupConventions: w.GroupConventions}
}

type PromptCodec struct{}

func (PromptCodec) Decode(kind, provider string, content []byte, extra []string) (domain.Prompt, error) {
	var raw struct {
		Provider      string
		SystemOne     *domain.SystemOnePrompt
		OpenAI        *domain.OpenAIPrompt
		IntentContext *promptIntentContextWire `json:"intent_context"`
	}
	decoder := json.NewDecoder(bytes.NewReader(content))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&raw); err != nil {
		return domain.Prompt{}, fmt.Errorf("intent: invalid prompt: invalid prompt content")
	}
	if raw.Provider != "" && raw.Provider != provider {
		return domain.Prompt{}, fmt.Errorf("intent: invalid prompt: prompt provider mismatch")
	}
	if provider == domain.PromptProviderJev && raw.SystemOne == nil {
		var direct domain.SystemOnePrompt
		if err := json.Unmarshal(content, &direct); err == nil && len(direct.Questions) > 0 {
			raw.SystemOne = &direct
		}
	}
	if provider == domain.PromptProviderOpenAI && raw.OpenAI == nil {
		var direct domain.OpenAIPrompt
		if err := json.Unmarshal(content, &direct); err == nil && (direct.SystemPrompt != "" || direct.UserTemplate != "") {
			raw.OpenAI = &direct
		}
	}
	p := domain.Prompt{Kind: kind, Provider: provider, SystemOne: raw.SystemOne, OpenAI: raw.OpenAI, IntentContext: raw.IntentContext.value()}
	if err := domain.ValidatePromptForKindWithVariables(kind, provider, &p, extra); err != nil {
		return domain.Prompt{}, err
	}
	return p, nil
}

func (PromptCodec) DecodeSupplement(kind, provider string, content []byte, extra []string, strict bool) (domain.Prompt, error) {
	var raw struct {
		Provider      string
		SystemOne     *domain.SystemOnePrompt
		OpenAI        *domain.OpenAIPrompt
		IntentContext *promptIntentContextWire `json:"intent_context"`
	}
	if strict {
		decoder := json.NewDecoder(bytes.NewReader(content))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&raw); err != nil {
			return domain.Prompt{}, err
		}
		if raw.Provider != "" && raw.Provider != provider {
			return domain.Prompt{}, fmt.Errorf("intent: invalid prompt: provider mismatch")
		}
	} else if err := json.Unmarshal(content, &raw); err != nil {
		return domain.Prompt{}, fmt.Errorf("invalid supplement content: %w", err)
	}
	prompt := domain.Prompt{Kind: kind, Provider: provider, SystemOne: raw.SystemOne, OpenAI: raw.OpenAI, IntentContext: raw.IntentContext.value()}
	if err := domain.ValidatePromptSupplementWithVariables(kind, provider, &prompt, extra); err != nil {
		return domain.Prompt{}, err
	}
	return prompt, nil
}

func (PromptCodec) VariableReferences(raw []byte) []string {
	matches := promptVariableRefPattern.FindAllSubmatch(raw, -1)
	if len(matches) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(matches))
	names := make([]string, 0, len(matches))
	for _, match := range matches {
		name := strings.TrimSpace(string(match[1]))
		if name == "" {
			continue
		}
		if _, ok := seen[name]; ok {
			continue
		}
		seen[name] = struct{}{}
		names = append(names, name)
	}
	return names
}

var promptVariableRefPattern = regexp.MustCompile(`\{\{([^{}]+)\}\}`)
