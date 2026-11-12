// Tests for saved replay evaluations and paid model confirmation.
// 已保存回放评估与付费模型确认测试。
package application

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/HappyLadySauceM/Nagi/internal/intent/contracts"
	"github.com/HappyLadySauceM/Nagi/internal/intent/domain"
)

type fakeReplayEvaluations struct {
	items []contracts.ReplayEvaluation
	next  int64
}

func (f *fakeReplayEvaluations) Create(_ context.Context, evaluation contracts.ReplayEvaluation) (contracts.ReplayEvaluation, error) {
	f.next++
	evaluation.ID = f.next
	if evaluation.CreatedAt.IsZero() {
		evaluation.CreatedAt = time.Now()
	}
	f.items = append(f.items, evaluation)
	return evaluation, nil
}

func (f *fakeReplayEvaluations) Get(_ context.Context, id int64) (contracts.ReplayEvaluation, error) {
	for _, item := range f.items {
		if item.ID == id {
			return item, nil
		}
	}
	return contracts.ReplayEvaluation{}, ErrReplayEvaluationNotFound
}

func (f *fakeReplayEvaluations) List(_ context.Context, query contracts.ReplayEvaluationQuery) ([]contracts.ReplayEvaluationSummary, error) {
	out := make([]contracts.ReplayEvaluationSummary, 0)
	for i := len(f.items) - 1; i >= 0; i-- {
		item := f.items[i]
		if query.BotID > 0 && item.BotID != query.BotID {
			continue
		}
		if query.DecisionID > 0 && item.DecisionID != query.DecisionID {
			continue
		}
		if query.BeforeID > 0 && item.ID >= query.BeforeID {
			continue
		}
		out = append(out, contracts.ReplayEvaluationSummary{
			ID: item.ID, DecisionID: item.DecisionID, BotID: item.BotID, Mode: item.Mode, Name: item.Name,
			Changed: item.Changed, Summary: item.After.Summary, Reproduction: item.Reproduction, CreatedAt: item.CreatedAt,
		})
	}
	return out, nil
}

func replaySnapshot() domain.DecisionSnapshot {
	return domain.DecisionSnapshot{
		Version: domain.SnapshotVersion, FlowVersion: domain.FlowVersion, RunID: "run-1",
		Input: domain.SnapshotInput{
			Task: domain.Task{JobID: 7, BotID: 100, Kind: "group", TargetID: 700, ConversationID: "conv-1"},
			Now:  time.Now(),
		},
		Prompt:         domain.BuiltinPrompt(domain.PromptProviderJev),
		Rule:           domain.RuleResult{NeedSemantic: true},
		RulesetVersion: "ruleset-1",
	}
}

func replayDecisionRecord() contracts.DecisionRecord {
	return contracts.DecisionRecord{
		ID: 7, JobID: 9, BotID: 100, ConversationID: "conv-1",
		Decision: "silent", ReasonCode: "jev_unavailable", Provider: "jev", Model: "decision-model",
		RulesetVersion: "ruleset-1", SnapshotStatus: domain.SnapshotStatusComplete,
	}
}

func TestEstimateModelReplayRequiresNoModelCall(t *testing.T) {
	semantic := &fakeSemantic{answer: domain.SemanticAnswer{Available: true, IntentCategory: "chat"}}
	d := newDecider(t, Options{Scopes: &fakeScopes{}, Recorder: &fakeRecorder{}, Semantic: semantic, Thresholds: thresholds, Provider: "jev", Model: "decision-model"})
	svc := NewReplayService(d, &fakeDecisionReader{record: replayDecisionRecord()}, &fakeSnapshotReader{snapshot: replaySnapshot()}, &fakeReplayEvaluations{})
	estimate, err := svc.EstimateModelReplay(context.Background(), 7)
	if err != nil {
		t.Fatalf("estimate: %v", err)
	}
	if semantic.calls != 0 {
		t.Fatalf("estimate called model %d times", semantic.calls)
	}
	if estimate.ConfirmationToken == "" || estimate.ExpiresAt.Before(time.Now()) {
		t.Fatalf("confirmation = %+v", estimate)
	}
	if estimate.SampleCount != 1 || estimate.EstimatedExternalRequests != 1 || estimate.TokenEstimate.TotalTokens <= 0 {
		t.Fatalf("estimate = %+v", estimate)
	}
	if estimate.Cost.Known || estimate.Cost.Amount != nil || !strings.Contains(estimate.Cost.Text, "费用未知") {
		t.Fatalf("cost = %+v", estimate.Cost)
	}
}

func TestReplayModelWithoutConfirmationIsRejected(t *testing.T) {
	semantic := &fakeSemantic{answer: domain.SemanticAnswer{Available: true, IntentCategory: "chat"}}
	d := newDecider(t, Options{Scopes: &fakeScopes{}, Recorder: &fakeRecorder{}, Semantic: semantic, Thresholds: thresholds, Provider: "jev", Model: "decision-model"})
	svc := NewReplayService(d, &fakeDecisionReader{record: replayDecisionRecord()}, &fakeSnapshotReader{snapshot: replaySnapshot()}, &fakeReplayEvaluations{})
	if _, err := svc.ReplayModelConfirmed(context.Background(), 7, "", "", ""); !errors.Is(err, ErrReplayConfirmationRequired) {
		t.Fatalf("err = %v, want confirmation required", err)
	}
	if semantic.calls != 0 {
		t.Fatalf("rejected replay called model %d times", semantic.calls)
	}
}

func TestReplayModelConfirmationBecomesStaleWhenModelChanges(t *testing.T) {
	runtime := RuntimeConfig{Enabled: true, AIEnabled: true, HistoryLimit: 30, Provider: "jev", Model: "model-a", Thresholds: thresholds}
	semantic := &fakeSemantic{answer: domain.SemanticAnswer{Available: true, IntentCategory: "chat"}}
	d := newDecider(t, Options{Scopes: &fakeScopes{}, Recorder: &fakeRecorder{}, Semantic: semantic, Thresholds: thresholds, Dynamic: func() RuntimeConfig { return runtime }})
	svc := NewReplayService(d, &fakeDecisionReader{record: replayDecisionRecord()}, &fakeSnapshotReader{snapshot: replaySnapshot()}, &fakeReplayEvaluations{})
	estimate, err := svc.EstimateModelReplay(context.Background(), 7)
	if err != nil {
		t.Fatalf("estimate: %v", err)
	}
	runtime.Model = "model-b"
	if _, err := svc.ReplayModelConfirmed(context.Background(), 7, estimate.ConfirmationToken, "", ""); !errors.Is(err, ErrReplayConfirmationStale) {
		t.Fatalf("err = %v, want stale", err)
	}
	if semantic.calls != 0 {
		t.Fatalf("stale replay called model %d times", semantic.calls)
	}
}

func TestConfirmedModelReplaySavesIndependentEvaluation(t *testing.T) {
	semantic := &fakeSemantic{answer: domain.SemanticAnswer{Available: true, IntentCategory: "chat"}}
	recorder := &fakeRecorder{}
	executor := &fakeExecutor{}
	evaluations := &fakeReplayEvaluations{}
	d := newDecider(t, Options{
		Scopes: &fakeScopes{}, Recorder: recorder, Semantic: semantic, Executor: executor,
		Thresholds: thresholds, Provider: "jev", Model: "decision-model",
	})
	svc := NewReplayService(d, &fakeDecisionReader{record: replayDecisionRecord()}, &fakeSnapshotReader{snapshot: replaySnapshot()}, evaluations)
	estimate, err := svc.EstimateModelReplay(context.Background(), 7)
	if err != nil {
		t.Fatalf("estimate: %v", err)
	}
	evaluation, err := svc.ReplayModelConfirmed(context.Background(), 7, estimate.ConfirmationToken, "manual check", "note")
	if err != nil {
		t.Fatalf("confirmed replay: %v", err)
	}
	if semantic.calls != 1 {
		t.Fatalf("semantic calls = %d, want 1", semantic.calls)
	}
	if len(recorder.decisions) != 0 || len(executor.tasks) != 0 {
		t.Fatalf("replay wrote or sent: decisions=%d tasks=%d", len(recorder.decisions), len(executor.tasks))
	}
	if evaluation.Mode != contracts.ReplayModeModel || evaluation.Name != "manual check" || evaluation.BotID != 100 {
		t.Fatalf("evaluation = %+v", evaluation)
	}
	if evaluation.After.Prompt.ID != domain.BuiltinPromptID || evaluation.After.Prompt.Version != 1 {
		t.Fatalf("run prompt = %+v", evaluation.After.Prompt)
	}
	if evaluation.Reproduction.FullyReproduced || !containsString(evaluation.Reproduction.Reasons, "semantic_current") {
		t.Fatalf("reproduction = %+v", evaluation.Reproduction)
	}
	// The same token cannot be used a second time.
	if _, err := svc.ReplayModelConfirmed(context.Background(), 7, estimate.ConfirmationToken, "", ""); !errors.Is(err, ErrReplayConfirmationRequired) {
		t.Fatalf("second confirm err = %v", err)
	}
}

func TestSaveRecalculateEvaluationIsFreeAndLocal(t *testing.T) {
	semantic := &fakeSemantic{answer: domain.SemanticAnswer{Available: true, IntentCategory: "chat"}}
	executor := &fakeExecutor{}
	evaluations := &fakeReplayEvaluations{}
	d := newDecider(t, Options{Scopes: &fakeScopes{}, Recorder: &fakeRecorder{}, Semantic: semantic, Executor: executor, Thresholds: thresholds, Provider: "jev", Model: "decision-model"})
	svc := NewReplayService(d, &fakeDecisionReader{record: replayDecisionRecord()}, &fakeSnapshotReader{snapshot: replaySnapshot()}, evaluations)
	evaluation, err := svc.SaveRecalculateEvaluation(context.Background(), 7, "free", "")
	if err != nil {
		t.Fatalf("save recalculate: %v", err)
	}
	if semantic.calls != 0 || len(executor.tasks) != 0 {
		t.Fatalf("recalculate called model=%d sent=%d", semantic.calls, len(executor.tasks))
	}
	if evaluation.Mode != contracts.ReplayModeRecalculate || evaluation.Cost.MayIncur {
		t.Fatalf("evaluation = %+v", evaluation)
	}
	if evaluation.Parameters.RulesetVersion != evaluation.After.RulesetVersion {
		t.Fatalf("saved ruleset = %q, actual = %q", evaluation.Parameters.RulesetVersion, evaluation.After.RulesetVersion)
	}
	if !containsString(evaluation.Reproduction.Reasons, "policy_current") || !containsString(evaluation.Reproduction.Reasons, "rules_current") {
		t.Fatalf("reproduction = %+v", evaluation.Reproduction)
	}
	if items, err := svc.ListEvaluations(context.Background(), contracts.ReplayEvaluationQuery{BotID: 100}); err != nil || len(items) != 1 {
		t.Fatalf("list = %+v err=%v", items, err)
	}
}

func TestCompareEvaluationReportsDifferences(t *testing.T) {
	evaluations := &fakeReplayEvaluations{items: []contracts.ReplayEvaluation{{
		ID: 1, DecisionID: 7, BotID: 100, Mode: contracts.ReplayModeRecalculate, Changed: true,
		Before: contracts.Run{Summary: contracts.RunSummary{Decision: "silent", ReasonCode: "jev_unavailable"}, Provider: "jev", Model: "model-a", RulesetVersion: "ruleset-1", Prompt: contracts.PromptInfo{ID: domain.BuiltinPromptID, Version: 1, Hash: "a"}},
		After:  contracts.Run{Summary: contracts.RunSummary{Decision: "reply", ReasonCode: "jev_reply"}, Provider: "jev", Model: "model-b", RulesetVersion: "ruleset-2", Prompt: contracts.PromptInfo{ID: domain.BuiltinPromptID, Version: 2, Hash: "b"}},
	}}}
	svc := NewReplayService(nil, nil, nil, evaluations)
	comparison, err := svc.CompareEvaluation(context.Background(), 1, 0)
	if err != nil {
		t.Fatalf("compare: %v", err)
	}
	if !comparison.Changed || comparison.Against != "original" {
		t.Fatalf("comparison = %+v", comparison)
	}
	fields := make(map[string]bool)
	for _, difference := range comparison.Differences {
		fields[difference.Field] = true
	}
	for _, field := range []string{"Decision", "ReasonCode", "Model", "RulesetVersion", "Prompt", "PromptHash"} {
		if !fields[field] {
			t.Fatalf("missing difference %q in %+v", field, comparison.Differences)
		}
	}
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func TestEstimateModelReplaySkipsCostWhenNoSemanticCall(t *testing.T) {
	snapshot := replaySnapshot()
	snapshot.Rule = domain.RuleResult{NeedSemantic: false}
	semantic := &fakeSemantic{answer: domain.SemanticAnswer{Available: true, IntentCategory: "chat"}}
	d := newDecider(t, Options{Scopes: &fakeScopes{}, Recorder: &fakeRecorder{}, Semantic: semantic, Thresholds: thresholds, Provider: "jev", Model: "decision-model"})
	svc := NewReplayService(d, &fakeDecisionReader{record: replayDecisionRecord()}, &fakeSnapshotReader{snapshot: snapshot}, &fakeReplayEvaluations{})
	estimate, err := svc.EstimateModelReplay(context.Background(), 7)
	if err != nil {
		t.Fatalf("estimate: %v", err)
	}
	if estimate.EstimatedExternalRequests != 0 || estimate.TokenEstimate.TotalTokens != 0 {
		t.Fatalf("estimate = %+v", estimate)
	}
	if estimate.Cost.MayIncur || !estimate.Cost.Known || !strings.Contains(estimate.Cost.Text, "不产生外部模型费用") {
		t.Fatalf("cost = %+v", estimate.Cost)
	}
	if semantic.calls != 0 {
		t.Fatalf("estimate called model %d times", semantic.calls)
	}
}

func TestReplayModelConfirmedChecksStoreBeforeExternalCall(t *testing.T) {
	semantic := &fakeSemantic{answer: domain.SemanticAnswer{Available: true, IntentCategory: "chat"}}
	d := newDecider(t, Options{Scopes: &fakeScopes{}, Recorder: &fakeRecorder{}, Semantic: semantic, Thresholds: thresholds, Provider: "jev", Model: "decision-model"})
	svc := NewReplayService(d, &fakeDecisionReader{record: replayDecisionRecord()}, &fakeSnapshotReader{snapshot: replaySnapshot()})
	estimate, err := svc.EstimateModelReplay(context.Background(), 7)
	if err != nil {
		t.Fatalf("estimate: %v", err)
	}
	if _, err := svc.ReplayModelConfirmed(context.Background(), 7, estimate.ConfirmationToken, "", ""); !errors.Is(err, ErrReplayEvaluationStoreDown) {
		t.Fatalf("err = %v, want store down", err)
	}
	if semantic.calls != 0 {
		t.Fatalf("missing evaluation store still called model %d times", semantic.calls)
	}
}
