package domain

import (
	"testing"
	"time"
)

func TestCooldownGateEvidence(t *testing.T) {
	now := time.Date(2026, 11, 22, 0, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name, branch, source, reason string
		threshold                    int
		activity                     BotActivity
		recent                       []Message
		deferTopic, policyOff        bool
		elapsed                      int64
	}{
		{name: "disabled", branch: "disabled", elapsed: -1},
		{name: "missing", threshold: 1000, branch: "input_missing", source: "unavailable", elapsed: -1},
		{name: "known empty stats win over history", threshold: 1000, branch: "input_missing", source: "activity_stats", activity: BotActivity{Known: true}, recent: []Message{{IsBot: true, CreatedAt: now}}, elapsed: -1},
		{name: "equal after whole-second truncation", threshold: 2000, branch: "equal", source: "activity_stats", activity: BotActivity{Known: true, LastReplyAt: now.Add(-2900 * time.Millisecond)}, elapsed: 2000},
		{name: "strict less than", threshold: 2001, branch: "blocked", source: "activity_stats", reason: "cooldown", activity: BotActivity{Known: true, LastReplyAt: now.Add(-2900 * time.Millisecond)}, elapsed: 2000},
		{name: "authorized history fallback", threshold: 2001, branch: "deferred", source: "authorized_history", recent: []Message{{IsBot: true, CreatedAt: now.Add(-2900 * time.Millisecond)}}, deferTopic: true, elapsed: 2000},
		{name: "policy stops first", threshold: 1000, branch: "not_executed", reason: "policy_disabled", policyOff: true, elapsed: -1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			in := Input{Now: now, Activity: tc.activity, Recent: tc.recent}
			in.Policy.Enabled = !tc.policyOff
			in.Policy.AllowHistoryRead = len(tc.recent) > 0
			in.Policy.MinReplyIntervalMS = tc.threshold
			in.Policy.Source = "independent"
			in.Policy.Version = 7
			reason, event := EvaluateCooldownGate(in, tc.deferTopic)
			if reason != tc.reason || event.Branch != tc.branch || event.InputSource != tc.source || event.RuleID != CooldownRuleID || event.Version != CooldownRuleVersion {
				t.Fatalf("reason=%q event=%+v", reason, event)
			}
			if tc.policyOff {
				if event.ThresholdMS != nil || event.Operator != "" || event.ReasonCode != tc.reason {
					t.Fatalf("invented comparison: %+v", event)
				}
				return
			}
			if event.ThresholdMS == nil || *event.ThresholdMS != tc.threshold || event.Operator != "<" || event.PrecisionMS != 1000 || event.PolicyVersion != 7 {
				t.Fatalf("comparison metadata: %+v", event)
			}
			if tc.elapsed < 0 && event.ElapsedMS != nil || tc.elapsed >= 0 && (event.ElapsedMS == nil || *event.ElapsedMS != tc.elapsed) {
				t.Fatalf("unexpected operand: %+v", event)
			}
		})
	}
}
