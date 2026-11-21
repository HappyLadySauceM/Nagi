package main

import (
	"bytes"
	"encoding/json"
	"os"
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/HappyLadySauceM/Nagi/internal/intent/domain"
)

func TestFrozenComparisonPairsOnlyCompleteRows(t *testing.T) {
	raw, err := os.ReadFile("testdata/policy-comparison.json")
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := runComparison(nil, bytes.NewReader(raw), &out); err != nil {
		t.Fatal(err)
	}
	var report comparisonReport
	if err := json.Unmarshal(out.Bytes(), &report); err != nil {
		t.Fatal(err)
	}
	if report.EvaluationLevel != "policy_fixed_semantic" || report.Build.ExecutableSHA256 == "" || report.ManifestSHA256 == "" {
		t.Fatalf("missing evaluation provenance: %+v", report)
	}
	a, b := report.Summary[0], report.Summary[1]
	for _, tc := range []struct {
		rate        comparisonRate
		hits, total int
	}{
		{a.Evaluated, 4, 6}, {b.Evaluated, 5, 6}, {a.Paired, 4, 6}, {b.Paired, 4, 6},
		{a.Recall, 1, 2}, {b.Recall, 2, 2}, {a.Interruption, 0, 1}, {b.Interruption, 1, 1},
		{a.TopicHit, 1, 1}, {b.TopicHit, 1, 1}, {a.Labeled, 5, 6},
	} {
		if tc.rate.Numerator != tc.hits || tc.rate.Denominator != tc.total || tc.rate.Rate == nil {
			t.Fatalf("rate = %+v, want %d/%d", tc.rate, tc.hits, tc.total)
		}
	}
	if b.ModelCalls != 0 || b.Tokens != 0 || b.LatencyP95MS != nil || b.TargetLabels != 1 || b.TargetHit != nil {
		t.Fatalf("wrong offline measurements: %+v", b)
	}
	want := [][]string{{"semantic_answer"}, {"policy", "reference"}}
	got := [][]string{report.Items[3].Arms[0].Missing, report.Items[5].Arms[1].Missing}
	if !reflect.DeepEqual(got, want) || report.Items[3].Arms[0].Decision != "" || report.Items[3].Arms[1].Decision != domain.DecisionReply {
		t.Fatalf("wrong partial outcomes: %+v", got)
	}
}

func TestFrozenComparisonDoesNotSubstituteEvidence(t *testing.T) {
	raw, err := os.ReadFile("testdata/policy-comparison.json")
	if err != nil {
		t.Fatal(err)
	}
	var spec comparisonSpec
	if err := json.Unmarshal(raw, &spec); err != nil {
		t.Fatal(err)
	}
	quoted := spec.Samples[4]
	reference := quoted.Input.Topics.Reference
	quoted.Input.Topics.Reference = nil
	if got := quoted.evaluate(spec.Arms[0], nil); got.Decision != "" || !slices.Contains(got.Missing, "reference") {
		t.Fatalf("missing reference was accepted: %+v", got)
	}
	quoted.Input.Topics.Reference = reference
	quoted.Input.Attachments = &[]frozenAttachment{}
	if got := quoted.evaluate(spec.Arms[0], nil); got.Decision != "" || !slices.Contains(got.Missing, "attachment_state") {
		t.Fatalf("missing image state was accepted: %+v", got)
	}
	spec.Samples = spec.Samples[:1]
	sample := &spec.Samples[0]
	delete(sample.Semantic.Scores, "directed_to_bot")
	sample.Input.Recent = &[]domain.Message{{MessageID: 101, CreatedAt: sample.Input.Now.Add(time.Second)}}
	if got := sample.evaluate(spec.Arms[0], nil); got.Decision != "" || !slices.Contains(got.Missing, "arrival_history") {
		t.Fatalf("future history was accepted: %+v", got)
	}
	sample.Input.Recent = &[]domain.Message{}
	spec.Arms[1].Ruleset.Version = 0
	invalid, err := json.Marshal(spec)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := compareFrozen(bytes.NewReader(invalid), comparisonBuild{}); err == nil {
		t.Fatal("ruleset without a version was accepted")
	}
	spec.Arms[1].Ruleset.Version = 2
	raw, err = json.Marshal(spec)
	if err != nil {
		t.Fatal(err)
	}
	report, err := compareFrozen(bytes.NewReader(raw), comparisonBuild{})
	if err != nil {
		t.Fatal(err)
	}
	for i, arm := range report.Items[0].Arms {
		if arm.Decision != "" || !slices.Contains(arm.Missing, "semantic_score:directed_to_bot") || report.Summary[i].Recall.Rate != nil {
			t.Fatalf("arm %d accepted a missing score: %+v", i, arm)
		}
	}
}
