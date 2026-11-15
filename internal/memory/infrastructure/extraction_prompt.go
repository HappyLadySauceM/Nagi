package infrastructure

import (
	"context"
	"fmt"
	"strings"

	intentdomain "github.com/HappyLadySauceM/Nagi/internal/intent/domain"
	"github.com/HappyLadySauceM/Nagi/internal/memory/domain"
)

// ExtractionPromptProvider resolves a current extraction prompt and can load
// one immutable prompt version for snapshot reconstruction.
// ExtractionPromptProvider 解析当前抽取 Prompt，并能按版本加载不可变 Prompt 以重建快照。
type ExtractionPromptProvider interface {
	ResolveFor(ctx context.Context, promptID, kind, provider string, botID int64, targetType string, targetID int64, variables map[string]string) intentdomain.Prompt
	VersionPrompt(ctx context.Context, id int64, kind, provider string) (intentdomain.Prompt, error)
}

// ExtractionPromptResolver resolves and reconstructs the immutable prompt used
// by one extraction task.
// ExtractionPromptResolver 解析并重建单个抽取任务使用的不可变 Prompt。
type ExtractionPromptResolver struct {
	prompts ExtractionPromptProvider
}

// NewExtractionPromptResolver builds a resolver around the shared prompt service.
// NewExtractionPromptResolver 围绕共享 Prompt 服务构建解析器。
func NewExtractionPromptResolver(prompts ExtractionPromptProvider) *ExtractionPromptResolver {
	return &ExtractionPromptResolver{prompts: prompts}
}

// Resolve returns the prompt and snapshot for a task. With a persisted snapshot
// it reconstructs the exact template from immutable version IDs; it never falls
// back to the current published prompt.
// Resolve 返回任务的 Prompt 与快照。存在已持久化快照时，它按不可变版本 ID 重建
// 当时模板，绝不回退到当前发布版本。
func (r *ExtractionPromptResolver) Resolve(ctx context.Context, request domain.ExtractionRequest) (domain.ExtractionPrompt, error) {
	if request.PromptSnapshot != nil {
		snapshot := *request.PromptSnapshot
		prompt, err := r.rebuild(ctx, snapshot)
		if err != nil {
			return domain.ExtractionPrompt{}, err
		}
		if err := verifySnapshot(snapshot, prompt, request); err != nil {
			return domain.ExtractionPrompt{}, err
		}
		prompt = intentdomain.RenderPromptVariables(prompt, extractionVariables(request))
		return resolvedPrompt(snapshot, prompt)
	}
	prompt := r.resolveCurrent(ctx, request)
	snapshot := snapshotFromPrompt(prompt, request.Scope)
	return resolvedPrompt(snapshot, prompt)
}

func (r *ExtractionPromptResolver) resolveCurrent(ctx context.Context, request domain.ExtractionRequest) intentdomain.Prompt {
	provider := intentdomain.PromptProviderOpenAI
	if r.prompts == nil {
		prompt := intentdomain.BuiltinPromptFor(intentdomain.PromptKindMemoryExtraction, provider)
		prompt.BotID = request.Scope.BotID
		prompt.TargetType = request.Scope.ScopeType
		prompt.TargetID = request.Scope.ScopeID
		prompt.Layers = []intentdomain.PromptLayer{
			{Layer: intentdomain.PromptLayerAccountDefault, VersionID: 0, Version: 1, Source: intentdomain.PromptSourceBuiltin, Hash: prompt.Hash},
			{Layer: intentdomain.PromptLayerServerConstraints, Version: 1, Source: intentdomain.PromptServerConstraintsVersion, Hash: intentdomain.PromptTextHash(intentdomain.PromptServerConstraintsText)},
		}
		prompt.FinalHash = intentdomain.PromptFinalHash(prompt)
		prompt = intentdomain.RenderPromptVariables(prompt, extractionVariables(request))
		prompt.RenderedHash = intentdomain.PromptHash(prompt)
		return prompt
	}
	return r.prompts.ResolveFor(ctx, intentdomain.PromptIDMemoryExtraction, intentdomain.PromptKindMemoryExtraction, provider, request.Scope.BotID, request.Scope.ScopeType, request.Scope.ScopeID, extractionVariables(request))
}

func (r *ExtractionPromptResolver) rebuild(ctx context.Context, snapshot domain.PromptSnapshot) (intentdomain.Prompt, error) {
	if strings.TrimSpace(snapshot.Kind) == "" || strings.TrimSpace(snapshot.Provider) == "" {
		return intentdomain.Prompt{}, fmt.Errorf("memory extraction: prompt snapshot kind/provider required")
	}
	prompt := intentdomain.BuiltinPromptFor(snapshot.Kind, snapshot.Provider)
	prompt.BotID = snapshot.BotID
	prompt.TargetType = snapshot.TargetType
	prompt.TargetID = snapshot.TargetID
	for _, layer := range snapshot.Layers {
		switch layer.Layer {
		case intentdomain.PromptLayerAccountDefault:
			if layer.VersionID <= 0 {
				continue
			}
			loaded, err := r.loadVersion(ctx, layer.VersionID, snapshot.Kind, snapshot.Provider)
			if err != nil {
				return intentdomain.Prompt{}, err
			}
			prompt = loaded
		case intentdomain.PromptLayerScopeSupplement:
			if layer.VersionID <= 0 {
				continue
			}
			loaded, err := r.loadVersion(ctx, layer.VersionID, snapshot.Kind, snapshot.Provider)
			if err != nil {
				return intentdomain.Prompt{}, err
			}
			merged, err := intentdomain.MergePromptContent(snapshot.Kind, snapshot.Provider, prompt, loaded)
			if err != nil {
				return intentdomain.Prompt{}, fmt.Errorf("memory extraction: merge prompt snapshot supplement: %w", err)
			}
			prompt = merged
		}
	}
	prompt.ID = snapshot.PromptID
	prompt.Kind = snapshot.Kind
	prompt.Provider = snapshot.Provider
	prompt.BotID = snapshot.BotID
	prompt.TargetType = snapshot.TargetType
	prompt.TargetID = snapshot.TargetID
	prompt.ConversationID = snapshot.ConversationID
	prompt.Layers = snapshotLayers(snapshot.Layers)
	prompt.Variables = map[string]string{
		"bot_name":   "",
		"scope_type": snapshot.TargetType,
	}
	if prompt.OpenAI == nil {
		return intentdomain.Prompt{}, fmt.Errorf("memory extraction: prompt snapshot content missing OpenAI")
	}
	return prompt, nil
}

func (r *ExtractionPromptResolver) loadVersion(ctx context.Context, id int64, kind, provider string) (intentdomain.Prompt, error) {
	if r.prompts == nil {
		return intentdomain.Prompt{}, fmt.Errorf("memory extraction: prompt version store unavailable")
	}
	loaded, err := r.prompts.VersionPrompt(ctx, id, kind, provider)
	if err != nil {
		return intentdomain.Prompt{}, fmt.Errorf("memory extraction: load prompt version %d: %w", id, err)
	}
	return loaded, nil
}

func verifySnapshot(snapshot domain.PromptSnapshot, prompt intentdomain.Prompt, request domain.ExtractionRequest) error {
	if snapshot.BotID != request.Scope.BotID || snapshot.TargetType != request.Scope.ScopeType || snapshot.TargetID != request.Scope.ScopeID || snapshot.ConversationID != request.Scope.ConversationID {
		return fmt.Errorf("memory extraction: prompt snapshot target does not match task")
	}
	prompt.Variables = extractionVariables(request)
	if got := intentdomain.PromptFinalHash(prompt); got != snapshot.FinalHash {
		return fmt.Errorf("memory extraction: prompt snapshot final hash mismatch")
	}
	rendered := intentdomain.RenderPromptVariables(prompt, extractionVariables(request))
	if got := intentdomain.PromptHash(rendered); got != snapshot.RenderedHash {
		return fmt.Errorf("memory extraction: prompt snapshot rendered hash mismatch")
	}
	return nil
}

func resolvedPrompt(snapshot domain.PromptSnapshot, prompt intentdomain.Prompt) (domain.ExtractionPrompt, error) {
	systemPrompt := extractionSystemPrompt
	userTemplate := "聊天消息：\n{{messages_json}}"
	if prompt.OpenAI != nil {
		if strings.TrimSpace(prompt.OpenAI.SystemPrompt) != "" {
			systemPrompt = prompt.OpenAI.SystemPrompt
		}
		if strings.Contains(prompt.OpenAI.UserTemplate, "{{messages_json}}") {
			userTemplate = prompt.OpenAI.UserTemplate
		}
	}
	if strings.TrimSpace(snapshot.FinalHash) == "" || strings.TrimSpace(snapshot.RenderedHash) == "" {
		return domain.ExtractionPrompt{}, fmt.Errorf("memory extraction: prompt snapshot hashes required")
	}
	if len(snapshot.Layers) == 0 {
		return domain.ExtractionPrompt{}, fmt.Errorf("memory extraction: prompt snapshot layers required")
	}
	return domain.ExtractionPrompt{Snapshot: snapshot, SystemPrompt: systemPrompt, UserTemplate: userTemplate}, nil
}

func snapshotFromPrompt(prompt intentdomain.Prompt, scope domain.ExtractionScope) domain.PromptSnapshot {
	kind := prompt.Kind
	if strings.TrimSpace(kind) == "" {
		kind = intentdomain.PromptKindMemoryExtraction
	}
	provider := prompt.Provider
	if strings.TrimSpace(provider) == "" {
		provider = intentdomain.PromptProviderOpenAI
	}
	promptID := prompt.ID
	if strings.TrimSpace(promptID) == "" {
		promptID = intentdomain.PromptIDForKindOrEmpty(kind)
	}
	finalHash := prompt.FinalHash
	if strings.TrimSpace(finalHash) == "" {
		finalHash = intentdomain.PromptFinalHash(prompt)
	}
	renderedHash := prompt.RenderedHash
	if strings.TrimSpace(renderedHash) == "" {
		renderedHash = intentdomain.PromptHash(prompt)
	}
	layers := make([]domain.PromptSnapshotLayer, 0, len(prompt.Layers))
	for _, layer := range prompt.Layers {
		layers = append(layers, domain.PromptSnapshotLayer{
			Layer: layer.Layer, VersionID: layer.VersionID, Version: layer.Version, Source: layer.Source, Hash: layer.Hash,
		})
	}
	if len(layers) == 0 {
		versionID := int64(0)
		if prompt.Source != intentdomain.PromptSourceBuiltin {
			versionID = int64(prompt.Version)
		}
		layers = []domain.PromptSnapshotLayer{{
			Layer: intentdomain.PromptLayerAccountDefault, VersionID: versionID, Version: prompt.Version, Source: prompt.Source, Hash: prompt.Hash,
		}}
	}
	return domain.PromptSnapshot{
		PromptID: promptID, Kind: kind, Provider: provider, BotID: scope.BotID,
		TargetType: scope.ScopeType, TargetID: scope.ScopeID, ConversationID: scope.ConversationID,
		FinalHash: finalHash, RenderedHash: renderedHash, Layers: layers,
	}
}

func snapshotLayers(layers []domain.PromptSnapshotLayer) []intentdomain.PromptLayer {
	out := make([]intentdomain.PromptLayer, 0, len(layers))
	for _, layer := range layers {
		out = append(out, intentdomain.PromptLayer{
			Layer: layer.Layer, VersionID: layer.VersionID, Version: layer.Version, Source: layer.Source, Hash: layer.Hash,
		})
	}
	return out
}

func extractionVariables(request domain.ExtractionRequest) map[string]string {
	return map[string]string{"bot_name": request.BotName, "scope_type": request.Scope.ScopeType}
}
