package contracts

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/HappyLadySauceM/Nagi/internal/audit/domain"
)

type runRecorder struct {
	events []domain.AIEvent
}

func (*runRecorder) StartRun(context.Context, domain.AIRun) (domain.AIRun, error) {
	return domain.AIRun{}, nil
}
func (*runRecorder) FinishRun(context.Context, string, string, string) error { return nil }
func (r *runRecorder) RecordEvent(_ context.Context, event domain.AIEvent) (domain.AIEvent, error) {
	event.ID = "event-" + string(rune('a'+len(r.events)))
	r.events = append(r.events, event)
	return event, nil
}
func (r *runRecorder) FinishEvent(_ context.Context, id, status, code string, output []byte) error {
	for index := range r.events {
		if r.events[index].ID == id {
			r.events[index].Status, r.events[index].ErrorCode, r.events[index].Output = status, code, output
			return nil
		}
	}
	return domain.ErrNotFound
}
func (*runRecorder) RecordArtifact(context.Context, domain.AIArtifact) (domain.AIArtifact, error) {
	return domain.AIArtifact{}, nil
}

func TestRunContextWritesOrderedOperationFacts(t *testing.T) {
	recorder := &runRecorder{}
	ctx := WithRun(context.Background(), recorder, "run-1")
	first, err := StartEvent(ctx, "asr", map[string]any{"input": "voice"})
	if err != nil {
		t.Fatal(err)
	}
	second, err := StartEvent(ctx, "generation", map[string]any{"input": "prompt"})
	if err != nil {
		t.Fatal(err)
	}
	if first.Sequence != 1 || second.Sequence != 2 || first.OperationID == second.OperationID {
		t.Fatalf("events not ordered: first=%+v second=%+v", first, second)
	}
	if err := FinishEvent(ctx, first, "succeeded", "", map[string]any{"text": "transcript"}); err != nil {
		t.Fatal(err)
	}
	if !json.Valid(recorder.events[0].Output) || recorder.events[0].Status != "succeeded" {
		t.Fatalf("terminal fact = %+v", recorder.events[0])
	}
}

func TestRunContextRedactsSensitiveEventData(t *testing.T) {
	recorder := &runRecorder{}
	ctx := WithRun(context.Background(), recorder, "run-1")
	_, err := StartEvent(ctx, "tool.execute", map[string]any{
		"authorization": "Bearer secret-token",
		"url":           "https://media.example/a.mp3?X-Amz-Signature=secret",
	})
	if err != nil {
		t.Fatalf("start event: %v", err)
	}
	if len(recorder.events) != 1 {
		t.Fatalf("events = %+v", recorder.events)
	}
	payload := string(recorder.events[0].Input)
	if strings.Contains(payload, "secret-token") || strings.Contains(payload, "Signature=secret") {
		t.Fatalf("sensitive event input leaked: %s", payload)
	}
}
