// Integration test for the replay evaluation repository, gated by NAGI_TEST_DSN.
// 回放评估仓储的集成测试，以 NAGI_TEST_DSN 为门控。
package infrastructure

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/HappyLadySauceM/Nagi/internal/intent/contracts"
	"github.com/HappyLadySauceM/Nagi/internal/intent/domain"
	"github.com/HappyLadySauceM/Nagi/pkg/db/dbtest"
)

func TestReplayEvaluationsRepoRoundTrip(t *testing.T) {
	pool := dbtest.Open(t)
	ctx := context.Background()
	jobID, botID, _ := seedJob(t, pool)
	decisions, err := NewDecisionsRepo(pool)
	if err != nil {
		t.Fatalf("new decisions repo: %v", err)
	}
	if err := decisions.Record(ctx, jobID, domain.FinalDecision{
		Decision: domain.DecisionSilent, ReasonCode: "jev_unavailable", Provider: "jev", Model: "jev-latest",
		RulesetVersion: "ruleset-1",
		Snapshot: &domain.DecisionSnapshot{
			Version: domain.SnapshotVersion, FlowVersion: domain.FlowVersion, RunID: "run-replay-1",
			Input:          domain.SnapshotInput{Task: domain.Task{JobID: jobID, BotID: botID}, Now: time.Now().UTC()},
			Prompt:         domain.BuiltinPrompt(domain.PromptProviderJev),
			Rule:           domain.RuleResult{NeedSemantic: true},
			RulesetVersion: "ruleset-1",
		},
	}, dbtest.Claim(t, pool, jobID)); err != nil {
		t.Fatalf("record decision: %v", err)
	}
	list, err := decisions.List(ctx, contracts.DecisionQuery{BotID: botID, Limit: 1})
	if err != nil || len(list) != 1 {
		t.Fatalf("list decisions = %+v err=%v", list, err)
	}
	decisionID := list[0].ID
	if list[0].Prompt.Version != 1 || list[0].Prompt.ID != domain.BuiltinPromptID {
		t.Fatalf("decision prompt info = %+v", list[0].Prompt)
	}
	repo, err := NewReplayEvaluationsRepo(pool)
	if err != nil {
		t.Fatalf("new replay repo: %v", err)
	}
	evaluation := contracts.ReplayEvaluation{
		DecisionID: decisionID, BotID: botID, Mode: contracts.ReplayModeModel, Name: "saved", Note: "note",
		Changed:      true,
		Before:       contracts.Run{Summary: contracts.RunSummary{Decision: "silent", ReasonCode: "jev_unavailable"}, Prompt: contracts.PromptInfo{ID: domain.BuiltinPromptID, Version: 1}},
		After:        contracts.Run{Summary: contracts.RunSummary{Decision: "reply", ReasonCode: "jev_reply"}, Prompt: contracts.PromptInfo{ID: domain.BuiltinPromptID, Version: 2}},
		Parameters:   contracts.ReplayParameters{Provider: "jev", Model: "jev-latest", PromptID: domain.BuiltinPromptID, PromptVersion: 1, SampleCount: 1, EstimatedExternalRequests: 1},
		Cost:         contracts.ReplayCost{MayIncur: true, Known: false, Text: "费用未知：未配置模型价格信息"},
		Reproduction: contracts.ReplayReproduction{FullyReproduced: false, Reasons: []string{"semantic_current"}},
	}
	saved, err := repo.Create(ctx, evaluation)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if saved.ID == 0 || saved.CreatedAt.IsZero() {
		t.Fatalf("saved = %+v", saved)
	}
	got, err := repo.Get(ctx, saved.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.DecisionID != decisionID || got.BotID != botID || !got.Changed || got.Mode != contracts.ReplayModeModel {
		t.Fatalf("got = %+v", got)
	}
	if got.After.Summary.Decision != "reply" || got.Before.Summary.Decision != "silent" {
		t.Fatalf("runs = before=%+v after=%+v", got.Before.Summary, got.After.Summary)
	}
	if got.Parameters.EstimatedExternalRequests != 1 || got.Cost.Known {
		t.Fatalf("parameters/cost = %+v / %+v", got.Parameters, got.Cost)
	}
	if len(got.Reproduction.Reasons) != 1 || got.Reproduction.Reasons[0] != "semantic_current" {
		t.Fatalf("reproduction = %+v", got.Reproduction)
	}
	items, err := repo.List(ctx, contracts.ReplayEvaluationQuery{BotID: botID, DecisionID: decisionID, Limit: 10})
	if err != nil || len(items) != 1 {
		t.Fatalf("list evaluations = %+v err=%v", items, err)
	}
	if items[0].ID != saved.ID || items[0].Summary.Decision != "reply" || len(items[0].Reproduction.Reasons) != 1 {
		t.Fatalf("summary = %+v", items[0])
	}
	if _, err := repo.Get(ctx, saved.ID+1000); !errors.Is(err, ErrReplayEvaluationNotFound) {
		t.Fatalf("missing get err = %v", err)
	}
}
