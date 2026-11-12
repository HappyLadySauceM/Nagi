package application

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/HappyLadySauceM/Nagi/internal/intent/contracts"
	"github.com/HappyLadySauceM/Nagi/internal/intent/domain"
)

type fakeSnapshotReader struct {
	snapshot domain.DecisionSnapshot
	err      error
}

type fakeDecisionReader struct{ record contracts.DecisionRecord }

func (*fakeDecisionReader) List(context.Context, contracts.DecisionQuery) ([]contracts.DecisionRecord, error) {
	return nil, nil
}

func (f *fakeDecisionReader) Get(context.Context, int64) (contracts.DecisionRecord, error) {
	return f.record, nil
}

func (f *fakeSnapshotReader) Snapshot(context.Context, int64) (domain.DecisionSnapshot, error) {
	return f.snapshot, f.err
}

func TestReplayModelRejectsSnapshotWithoutRule(t *testing.T) {
	d := newDecider(t, Options{Scopes: &fakeScopes{}, Recorder: &fakeRecorder{}, Thresholds: thresholds})
	svc := NewReplayService(d, nil, &fakeSnapshotReader{snapshot: domain.DecisionSnapshot{
		MissingFields: []string{"rule"},
	}})
	if _, err := svc.ReplayModel(context.Background(), 1); !errors.Is(err, ErrSnapshotMissing) {
		t.Fatalf("err = %v, want snapshot_missing", err)
	}
}

func TestReplayModelUsesStoredRuleAndCallsModel(t *testing.T) {
	semantic := &fakeSemantic{answer: domain.SemanticAnswer{Available: true, IntentCategory: "chat"}}
	recorder := &fakeRecorder{}
	executor := &fakeExecutor{}
	d := newDecider(t, Options{Scopes: &fakeScopes{}, Recorder: recorder, Semantic: semantic, Executor: executor, Thresholds: thresholds, Provider: "jev", Model: "decision-model"})
	svc := NewReplayService(d, nil, &fakeSnapshotReader{snapshot: domain.DecisionSnapshot{
		Input: domain.SnapshotInput{
			Task: domain.Task{JobID: 7, BotID: 100, Kind: "group", TargetID: 700, ConversationID: "conv-1"},
			Now:  time.Now(),
		},
		Prompt:         domain.BuiltinPrompt(domain.PromptProviderJev),
		Rule:           domain.RuleResult{NeedSemantic: true},
		RulesetVersion: "ruleset-1",
	}})
	run, err := svc.ReplayModel(context.Background(), 7)
	if err != nil {
		t.Fatalf("replay: %v", err)
	}
	if semantic.calls != 1 {
		t.Fatalf("semantic calls = %d, want 1", semantic.calls)
	}
	if run.RulesetVersion != "ruleset-1" || len(run.Nodes) != 5 {
		t.Fatalf("run = %+v", run)
	}
	if run.Replay == nil || run.Replay.Input != "snapshot" || run.Replay.Semantic != "current" || run.Replay.Prompt != "snapshot" || run.Replay.Policy != "current" || run.Replay.Rules != "snapshot" || run.Replay.Persisted {
		t.Fatalf("replay marker = %+v", run.Replay)
	}
	if len(recorder.decisions) != 0 || len(executor.tasks) != 0 {
		t.Fatalf("replay wrote or sent: decisions=%d tasks=%d", len(recorder.decisions), len(executor.tasks))
	}
}

func TestRecalculateNeverCallsModelOrWrites(t *testing.T) {
	semantic := &fakeSemantic{answer: domain.SemanticAnswer{Available: true, IntentCategory: "chat"}}
	recorder := &fakeRecorder{}
	executor := &fakeExecutor{}
	d := newDecider(t, Options{Scopes: &fakeScopes{}, Recorder: recorder, Semantic: semantic, Executor: executor, Thresholds: thresholds, Provider: "jev", Model: "decision-model"})
	svc := NewReplayService(d, &fakeDecisionReader{record: contracts.DecisionRecord{ID: 7, JobID: 9, Decision: "silent", ReasonCode: "jev_unavailable"}}, &fakeSnapshotReader{snapshot: domain.DecisionSnapshot{
		Input: domain.SnapshotInput{
			Task: domain.Task{JobID: 9, BotID: 100, Kind: "group", TargetID: 700, ConversationID: "conv-1"},
			Now:  time.Now(),
		},
		Semantic:       domain.SemanticAnswer{Available: true, IntentCategory: "chat"},
		Prompt:         domain.BuiltinPrompt(domain.PromptProviderJev),
		Rule:           domain.RuleResult{NeedSemantic: true},
		RulesetVersion: "ruleset-1",
	}})
	_, _, _, err := svc.Recalculate(context.Background(), 7)
	if err != nil {
		t.Fatalf("recalculate: %v", err)
	}
	if semantic.calls != 0 {
		t.Fatalf("recalculate called model %d times", semantic.calls)
	}
	if len(recorder.decisions) != 0 || len(executor.tasks) != 0 {
		t.Fatalf("recalculate wrote or sent: decisions=%d tasks=%d", len(recorder.decisions), len(executor.tasks))
	}
}
