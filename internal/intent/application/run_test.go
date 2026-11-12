// Tests for prompt resolution fallback and run projection.
// Prompt 解析兜底与运行投影的测试。
package application

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/HappyLadySauceM/Nagi/internal/intent/contracts"
	"github.com/HappyLadySauceM/Nagi/internal/intent/domain"
)

type fakePromptRepo struct {
	active    domain.PromptRecord
	found     bool
	activeErr error
}

func (f *fakePromptRepo) Active(context.Context, string, string) (domain.PromptRecord, bool, error) {
	return f.active, f.found, f.activeErr
}
func (*fakePromptRepo) List(context.Context) ([]domain.PromptRecord, error) { return nil, nil }
func (*fakePromptRepo) Versions(context.Context, string) ([]domain.PromptRecord, error) {
	return nil, nil
}
func (*fakePromptRepo) Get(context.Context, int64) (domain.PromptRecord, error) {
	return domain.PromptRecord{}, nil
}
func (*fakePromptRepo) CreateDraft(context.Context, string, string, string, domain.Prompt, string, string, string) (domain.PromptRecord, error) {
	return domain.PromptRecord{}, nil
}
func (*fakePromptRepo) UpdateDraft(context.Context, int64, string, domain.Prompt) (domain.PromptRecord, error) {
	return domain.PromptRecord{}, nil
}
func (*fakePromptRepo) Publish(context.Context, int64) error        { return nil }
func (*fakePromptRepo) Rollback(context.Context, string, int) error { return nil }

func TestPromptServiceFallsBackToBuiltinWithoutLocalVersion(t *testing.T) {
	service := NewPromptService(&fakePromptRepo{}, testLogger())
	got := service.Resolve(context.Background(), domain.PromptProviderJev)
	if got.Source != domain.PromptSourceBuiltin || got.Version != 1 || got.SystemOne == nil {
		t.Fatalf("fallback prompt = %+v", got)
	}
	if got.FallbackReason != "no_local_published_prompt" {
		t.Fatalf("fallback reason = %q", got.FallbackReason)
	}
}

func TestPromptServiceUsesPublishedLocalVersion(t *testing.T) {
	builtin := domain.BuiltinPrompt(domain.PromptProviderJev)
	service := NewPromptService(&fakePromptRepo{
		found: true,
		active: domain.PromptRecord{
			PromptID: builtin.ID, Name: "local prompt", Provider: domain.PromptProviderJev,
			Version: 3, Status: domain.PromptStatusPublished, Source: domain.PromptSourceLocal,
			Hash: builtin.Hash, Content: domain.PromptContentJSON(builtin),
		},
	}, testLogger())
	got := service.Resolve(context.Background(), domain.PromptProviderJev)
	if got.Source != domain.PromptSourceLocal || got.Version != 3 || got.Name != "local prompt" {
		t.Fatalf("local prompt = %+v", got)
	}
}

func TestPromptServiceFallsBackOnInvalidLocalContent(t *testing.T) {
	service := NewPromptService(&fakePromptRepo{
		found: true,
		active: domain.PromptRecord{
			PromptID: domain.BuiltinPromptID, Name: "bad", Provider: domain.PromptProviderJev,
			Version: 2, Status: domain.PromptStatusPublished, Source: domain.PromptSourceLocal,
			Content: json.RawMessage(`{"Provider":"jev"}`),
		},
	}, testLogger())
	got := service.Resolve(context.Background(), domain.PromptProviderJev)
	if got.Source != domain.PromptSourceBuiltin || got.FallbackReason != "invalid_local_prompt" {
		t.Fatalf("invalid fallback = %+v", got)
	}
}

func TestPromptServiceFallsBackOnRepositoryError(t *testing.T) {
	service := NewPromptService(&fakePromptRepo{activeErr: errors.New("db down")}, testLogger())
	got := service.Resolve(context.Background(), domain.PromptProviderJev)
	if got.Source != domain.PromptSourceBuiltin || got.FallbackReason != "prompt_store_error" {
		t.Fatalf("error fallback = %+v", got)
	}
}

func TestBuildRunFromDecisionHasFixedNodesAndEdges(t *testing.T) {
	final := domain.FinalDecision{
		Decision: domain.DecisionReply, ReasonCode: "private_normal", RunID: "run-1", FlowVersion: domain.FlowVersion,
		Steps: []domain.Step{
			{NodeID: domain.NodePolicy, Status: domain.NodeStatusExecuted},
			{NodeID: domain.NodeContext, Status: domain.NodeStatusExecuted},
			{NodeID: domain.NodeRules, Status: domain.NodeStatusExecuted},
			{NodeID: domain.NodeSemantic, Status: domain.NodeStatusSkipped},
			{NodeID: domain.NodeDecision, Status: domain.NodeStatusExecuted},
		},
	}
	run := BuildRunFromDecision(final, contracts.Delivery{})
	if run.FlowVersion != domain.FlowVersion || len(run.Nodes) != 5 || len(run.Edges) != 5 {
		t.Fatalf("run = %+v", run)
	}
	if run.Edges[2].Status != domain.EdgeStatusNotTaken || run.Edges[3].Status != domain.EdgeStatusTaken {
		t.Fatalf("edges = %+v", run.Edges)
	}
}

// TestFixedFlowOrderMatchesDecisionSteps ensures the advertised node order is
// the order the executor actually records.
// TestFixedFlowOrderMatchesDecisionSteps 验证对外节点顺序与实际记录顺序一致。
func TestFixedFlowOrderMatchesDecisionSteps(t *testing.T) {
	steps := decisionSteps(domain.Input{}, domain.RuleResult{Blocked: true, ReasonCode: "policy_disabled"},
		domain.SemanticAnswer{}, domain.FinalDecision{Decision: domain.DecisionSilent, ReasonCode: "policy_disabled"},
		0, false, domain.Thresholds{}, false)
	flow := domain.FixedFlow()
	if len(steps) != len(flow.Nodes) {
		t.Fatalf("steps=%d flow nodes=%d", len(steps), len(flow.Nodes))
	}
	for i, node := range flow.Nodes {
		if steps[i].NodeID != node.NodeID || steps[i].Index != i {
			t.Fatalf("step %d = %+v, flow node = %+v", i, steps[i], node)
		}
	}
}
