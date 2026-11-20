package infrastructure

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	intentdomain "github.com/HappyLadySauceM/Nagi/internal/intent/domain"
	"github.com/HappyLadySauceM/Nagi/internal/memory/contracts"
	models "github.com/HappyLadySauceM/Nagi/internal/models/contracts"
	"github.com/HappyLadySauceM/Nagi/pkg/modelhttp"
	"github.com/cloudwego/eino-ext/components/model/openai"
	"github.com/cloudwego/eino/schema"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

// ModelRecallRewriter queries the configured generation model without tools.
// ModelRecallRewriter 使用当前生成模型改写检索问题，调用不携带工具。
type ModelRecallRewriter struct {
	resolver models.Resolver
	prompts  PromptProvider
	botName  func() string
}

func NewModelRecallRewriter(resolver models.Resolver, prompts PromptProvider, botName func() string) *ModelRecallRewriter {
	return &ModelRecallRewriter{resolver: resolver, prompts: prompts, botName: botName}
}

func (r *ModelRecallRewriter) Rewrite(ctx context.Context, request contracts.RecallRequest) ([]string, error) {
	if utf8.RuneCountInString(request.Query) > 1000 {
		return nil, nil
	}
	resolved, err := r.resolver.ResolveModel(ctx, "generation", request.BotID, request.Kind, request.TargetID)
	if err != nil {
		return nil, err
	}
	tokens := 100
	model, err := openai.NewChatModel(ctx, &openai.ChatModelConfig{
		BaseURL: resolved.Profile.BaseURL, APIKey: resolved.APIKey, Model: resolved.Profile.Model,
		ReasoningEffort: openai.ReasoningEffortLevel("none"),
		MaxTokens:       &tokens, HTTPClient: modelhttp.Client(http.DefaultClient, resolved.Profile.HTTPHeaders),
	})
	if err != nil {
		return nil, err
	}
	prompt := r.resolvePrompt(ctx, request)
	layers := make([]map[string]any, 0, len(prompt.Layers))
	for _, layer := range prompt.Layers {
		layers = append(layers, map[string]any{
			"layer": layer.Layer, "version_id": layer.VersionID, "version": layer.Version,
			"source": layer.Source, "hash": layer.Hash,
		})
	}
	layerJSON, _ := json.Marshal(layers)
	ctx, span := otel.Tracer("github.com/HappyLadySauceM/Nagi/internal/memory").Start(ctx, "nagi.memory.rewrite.model", trace.WithAttributes(
		attribute.String("langfuse.observation.type", "generation"),
		attribute.String("langfuse.observation.model.name", resolved.Profile.Model),
		attribute.String("langfuse.observation.metadata.prompt_template_hash", prompt.FinalHash),
		attribute.String("langfuse.observation.metadata.prompt_rendered_hash", prompt.RenderedHash),
		attribute.String("langfuse.observation.metadata.prompt_layers", string(layerJSON)),
		attribute.String("langfuse.observation.metadata.prompt_system_hash", intentdomain.PromptTextHash(prompt.OpenAI.SystemPrompt)),
	))
	defer span.End()
	response, err := model.Generate(ctx, []*schema.Message{
		schema.SystemMessage(prompt.OpenAI.SystemPrompt),
		schema.UserMessage(request.Query),
	})
	if err != nil {
		span.SetStatus(codes.Error, "model call failed")
		return nil, err
	}
	var terms []string
	if response == nil || len(response.Content) > 512 ||
		json.Unmarshal([]byte(response.Content), &terms) != nil || terms == nil || len(terms) > 3 {
		span.SetStatus(codes.Error, "invalid rewrite result")
		return nil, fmt.Errorf("memory recall: invalid rewrite")
	}
	for _, term := range terms {
		if len(term) == 0 || utf8.RuneCountInString(term) > 12 || strings.IndexFunc(term, func(r rune) bool {
			return !unicode.IsLetter(r) && !unicode.IsDigit(r)
		}) >= 0 {
			span.SetStatus(codes.Error, "invalid rewrite result")
			return nil, fmt.Errorf("memory recall: invalid rewrite term")
		}
	}
	return terms, nil
}

// resolvePrompt uses the current published layers and the actual speaker, which
// may differ from the memory subject.
// resolvePrompt 使用本地已发布层和实际发言人，记忆主体可能是另外的人。
func (r *ModelRecallRewriter) resolvePrompt(ctx context.Context, request contracts.RecallRequest) intentdomain.Prompt {
	variables := map[string]string{"scope_type": request.Kind, "bot_name": "Nagi"}
	if speaker, err := strconv.ParseInt(request.SpeakerID, 10, 64); err == nil && request.SpeakerType == "user" && speaker > 0 {
		variables["sender_id"] = strconv.FormatInt(speaker, 10)
	} else if request.Kind == "private" {
		variables["sender_id"] = strconv.FormatInt(request.TargetID, 10)
	}
	if r.botName != nil {
		if name := r.botName(); strings.TrimSpace(name) != "" {
			variables["bot_name"] = name
		}
	}
	if r.prompts != nil {
		prompt := r.prompts.ResolveFor(ctx, intentdomain.PromptIDMemoryRecall, intentdomain.PromptKindMemoryRecall, intentdomain.PromptProviderOpenAI, request.BotID, request.Kind, request.TargetID, variables)
		if prompt.OpenAI != nil && prompt.OpenAI.SystemPrompt != "" {
			return prompt
		}
	}
	prompt := intentdomain.BuiltinPromptFor(intentdomain.PromptKindMemoryRecall, intentdomain.PromptProviderOpenAI)
	prompt.FinalHash = intentdomain.PromptFinalHash(prompt)
	prompt.RenderedHash = intentdomain.PromptHash(prompt)
	return prompt
}
