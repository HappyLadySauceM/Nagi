package infrastructure

import (
	"context"
	intentinfra "github.com/HappyLadySauceM/Nagi/internal/intent/infrastructure"
	"io"
	"log/slog"
	"strings"
	"testing"

	intentapp "github.com/HappyLadySauceM/Nagi/internal/intent/application"
	intentdomain "github.com/HappyLadySauceM/Nagi/internal/intent/domain"
	"github.com/HappyLadySauceM/Nagi/internal/memory/domain"
)

type fakeExtractionPromptProvider struct {
	current  intentdomain.Prompt
	versions map[int64]intentdomain.Prompt
}

func (f fakeExtractionPromptProvider) ResolveFor(context.Context, string, string, string, int64, string, int64, map[string]string) intentdomain.Prompt {
	return f.current
}

func (f fakeExtractionPromptProvider) VersionPrompt(_ context.Context, id int64, _, _ string) (intentdomain.Prompt, error) {
	version, ok := f.versions[id]
	if !ok {
		return intentdomain.Prompt{}, context.Canceled
	}
	return version, nil
}

func extractionPromptFixture(system string) intentdomain.Prompt {
	prompt := intentdomain.BuiltinPromptFor(intentdomain.PromptKindMemoryExtraction, intentdomain.PromptProviderOpenAI)
	prompt.OpenAI.SystemPrompt = system
	prompt.Hash = intentdomain.PromptHash(prompt)
	prompt.Layers = []intentdomain.PromptLayer{
		{Layer: intentdomain.PromptLayerAccountDefault, VersionID: 10, Version: 1, Source: "local", Hash: prompt.Hash, Editable: true},
		{Layer: intentdomain.PromptLayerServerConstraints, Version: 1, Source: intentdomain.PromptServerConstraintsVersion, Hash: intentdomain.PromptTextHash(intentdomain.PromptServerConstraintsText)},
	}
	prompt.FinalHash = intentdomain.PromptFinalHash(prompt)
	prompt.RenderedHash = intentdomain.PromptHash(prompt)
	return prompt
}

func TestExtractionPromptResolverUsesPersistedSnapshot(t *testing.T) {
	scope := domain.ExtractionScope{BotID: 7, ConversationID: "conv-1", ScopeType: "group", ScopeID: 700}
	oldPrompt := extractionPromptFixture("旧版抽取 System")
	oldSnapshot := snapshotFromPrompt(oldPrompt, scope)
	newPrompt := extractionPromptFixture("新版抽取 System")
	resolver := NewExtractionPromptResolver(fakeExtractionPromptProvider{
		current:  newPrompt,
		versions: map[int64]intentdomain.Prompt{10: oldPrompt},
	})
	resolved, err := resolver.Resolve(context.Background(), domain.ExtractionRequest{Scope: scope, PromptSnapshot: &oldSnapshot})
	if err != nil {
		t.Fatalf("resolve persisted snapshot: %v", err)
	}
	if resolved.SystemPrompt != "旧版抽取 System" || resolved.Snapshot.FinalHash != oldSnapshot.FinalHash || resolved.Snapshot.RenderedHash != oldSnapshot.RenderedHash {
		t.Fatalf("resolved snapshot = %#v", resolved)
	}
	if !strings.Contains(resolved.Snapshot.PromptID, "memory.extraction") || len(resolved.Snapshot.Layers) == 0 {
		t.Fatalf("snapshot metadata = %#v", resolved.Snapshot)
	}
}

func TestExtractionPromptResolverFailsMissingSnapshotVersion(t *testing.T) {
	scope := domain.ExtractionScope{BotID: 7, ConversationID: "conv-1", ScopeType: "group", ScopeID: 700}
	snapshot := snapshotFromPrompt(extractionPromptFixture("旧版"), scope)
	resolver := NewExtractionPromptResolver(fakeExtractionPromptProvider{current: extractionPromptFixture("新版")})
	if _, err := resolver.Resolve(context.Background(), domain.ExtractionRequest{Scope: scope, PromptSnapshot: &snapshot}); err == nil {
		t.Fatal("missing prompt version should fail explicitly")
	}
}

func TestExtractionPromptResolverRejectsHashMismatch(t *testing.T) {
	scope := domain.ExtractionScope{BotID: 7, ConversationID: "conv-1", ScopeType: "group", ScopeID: 700}
	oldPrompt := extractionPromptFixture("旧版")
	snapshot := snapshotFromPrompt(oldPrompt, scope)
	snapshot.RenderedHash = "wrong"
	resolver := NewExtractionPromptResolver(fakeExtractionPromptProvider{
		current:  extractionPromptFixture("新版"),
		versions: map[int64]intentdomain.Prompt{10: oldPrompt},
	})
	if _, err := resolver.Resolve(context.Background(), domain.ExtractionRequest{Scope: scope, PromptSnapshot: &snapshot}); err == nil {
		t.Fatal("hash mismatch should fail explicitly")
	}
}

func TestExtractionPromptResolverBuiltinRoundTrip(t *testing.T) {
	promptService := intentapp.NewPromptService(nil, slog.New(slog.NewTextHandler(io.Discard, nil)), intentinfra.PromptCodec{})
	resolver := NewExtractionPromptResolver(promptService)
	request := domain.ExtractionRequest{Scope: domain.ExtractionScope{BotID: 7, ConversationID: "conv-1", ScopeType: "group", ScopeID: 700}, BotName: "Nagi"}
	first, err := resolver.Resolve(context.Background(), request)
	if err != nil {
		t.Fatalf("resolve current prompt: %v", err)
	}
	request.PromptSnapshot = &first.Snapshot
	second, err := resolver.Resolve(context.Background(), request)
	if err != nil {
		t.Fatalf("resolve persisted snapshot: %v", err)
	}
	if first.Snapshot.FinalHash != second.Snapshot.FinalHash || first.SystemPrompt != second.SystemPrompt {
		t.Fatalf("builtin round trip changed prompt: first=%+v second=%+v", first, second)
	}
}

func TestExtractionPromptResolverRendersSnapshotVariables(t *testing.T) {
	scope := domain.ExtractionScope{BotID: 7, ConversationID: "conv-1", ScopeType: "group", ScopeID: 700}
	template := extractionPromptFixture("旧版 {{bot_name}}")
	rendered := intentdomain.RenderPromptVariables(template, map[string]string{"bot_name": "Nagi", "scope_type": "group"})
	rendered.RenderedHash = intentdomain.PromptHash(rendered)
	snapshot := snapshotFromPrompt(rendered, scope)
	resolver := NewExtractionPromptResolver(fakeExtractionPromptProvider{
		current:  extractionPromptFixture("新版"),
		versions: map[int64]intentdomain.Prompt{10: template},
	})
	resolved, err := resolver.Resolve(context.Background(), domain.ExtractionRequest{Scope: scope, BotName: "Nagi", PromptSnapshot: &snapshot})
	if err != nil {
		t.Fatalf("resolve variable snapshot: %v", err)
	}
	if resolved.SystemPrompt != "旧版 Nagi" {
		t.Fatalf("rendered system = %q, want 旧版 Nagi", resolved.SystemPrompt)
	}
}
