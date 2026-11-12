package application

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/HappyLadySauceM/Nagi/internal/intent/domain"
)

type fakeSnapshotReader struct {
	snapshot domain.DecisionSnapshot
	err      error
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
	d := newDecider(t, Options{Scopes: &fakeScopes{}, Recorder: &fakeRecorder{}, Semantic: semantic, Thresholds: thresholds, Provider: "jev", Model: "decision-model"})
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
}
