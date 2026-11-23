package infrastructure

import (
	"context"
	"errors"
	"testing"

	conversation "github.com/HappyLadySauceM/Nagi/internal/conversation/domain"
	conversationinfra "github.com/HappyLadySauceM/Nagi/internal/conversation/infrastructure"
	"github.com/HappyLadySauceM/Nagi/internal/intent/contracts"
	"github.com/HappyLadySauceM/Nagi/internal/intent/domain"
	"github.com/HappyLadySauceM/Nagi/pkg/db/dbtest"
)

func TestLiveRecoveryUsesDurableFacts(t *testing.T) {
	ctx := context.Background()
	pool := dbtest.Open(t)
	r, err := NewDecisionsRepo(pool)
	if err != nil {
		t.Fatal(err)
	}
	jobs, err := conversationinfra.NewRepo(pool)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, window, status, code   string
		replyStarted, interruptFirst bool
		wantReply                    bool
	}{
		{name: "before-registration", window: "unregistered", status: "pending", code: "lease_expired"},
		{name: "before-semantic-boundary", window: "registered", status: "pending", code: "lease_expired"},
		{name: "start-committed", window: "started", status: "failed", code: "semantic_result_unknown"},
		{name: "interrupted-before-job-recovery", window: "started", status: "failed", code: "semantic_result_unknown", interruptFirst: true},
		{name: "branch-only", window: "branch", status: "failed", code: "semantic_result_unknown"},
		{name: "result-durable", window: "result", status: "pending", code: "decision_resume", interruptFirst: true, wantReply: true},
		{name: "decision-durable", window: "completed", status: "pending", code: "decision_resume", wantReply: true},
		{name: "reply-already-started", window: "completed", status: "pending", code: "decision_resume", replyStarted: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			job, bot, conv := seedJob(t, pool)
			task := domain.Task{JobID: job, BotID: bot, ConversationID: conv, ClaimToken: dbtest.Claim(t, pool, job)}
			var run string
			var previousID int64
			if tc.window != "unregistered" {
				id, created, err := r.StartDecisionRun(ctx, task)
				if err != nil || !created {
					t.Fatalf("start %q %v %v", id, created, err)
				}
				run = id
			}
			if tc.window != "unregistered" && tc.window != "registered" {
				if err := r.AppendDecisionStep(ctx, task, run, 4, "semantic_started", contracts.LiveDecisionFacts{}); err != nil {
					t.Fatal(err)
				}
			}
			if tc.window == "branch" || tc.window == "result" || tc.window == "completed" {
				facts := contracts.LiveDecisionFacts{Branch: "accepted", LatencyMS: 13}
				if err := r.AppendDecisionStep(ctx, task, run, 5, "semantic", facts); err != nil {
					t.Fatal(err)
				}
			}
			if tc.window == "result" || tc.window == "completed" {
				decision := domain.FinalDecision{RunID: run, Decision: domain.DecisionReply, ReasonCode: "always_reply", Snapshot: &domain.DecisionSnapshot{Version: domain.SnapshotVersion, FlowVersion: domain.FlowVersion, RunID: run, Input: domain.SnapshotInput{Task: task, IntentAsked: true, GenerationEligible: true}}, Steps: []domain.Step{
					{Index: 0, Phase: domain.StepRules, NodeID: domain.NodeRules, NodeVersion: 1, Status: domain.NodeStatusExecuted, Detail: map[string]any{"RuleEvents": []domain.CooldownEvent{{RuleID: domain.CooldownRuleID, Branch: "passed", Sequence: 1}}}},
				}}
				if err := r.SaveDecisionRecovery(ctx, task, decision); err != nil {
					t.Fatal(err)
				}
				if tc.window == "completed" {
					if err := r.RecordDecisionRun(ctx, job, decision, task.ClaimToken); err != nil {
						t.Fatal(err)
					}
					if err := pool.QueryRow(ctx, `SELECT decision_id FROM intent_decision_runs WHERE id=$1`, run).Scan(&previousID); err != nil {
						t.Fatal(err)
					}
				}
			}
			if tc.replyStarted {
				if opened, err := r.BeginDecisionReply(ctx, task, run); err != nil || !opened {
					t.Fatalf("original reply boundary %v %v", opened, err)
				}
			}
			if _, err := pool.Exec(ctx, `UPDATE message_jobs SET lease_expires_at=clock_timestamp()-interval '1 second' WHERE id=$1`, job); err != nil {
				t.Fatal(err)
			}
			if tc.interruptFirst {
				if err := r.InterruptExpiredDecisionRuns(ctx); err != nil {
					t.Fatal(err)
				}
			}
			if err := jobs.RecoverStaleJobs(ctx, conversation.RecoveryPolicy{MaxAttempts: 5}); err != nil {
				t.Fatal(err)
			}
			if err := r.InterruptExpiredDecisionRuns(ctx); err != nil {
				t.Fatal(err)
			}
			var status, code string
			if err := pool.QueryRow(ctx, `SELECT status,last_error_code FROM message_jobs WHERE id=$1`, job).Scan(&status, &code); err != nil || status != tc.status || code != tc.code {
				t.Fatalf("job %d recovery %q %q %v", job, status, code, err)
			}
			if tc.status == "failed" {
				var state, reason string
				if err := pool.QueryRow(ctx, `SELECT r.state,e.data->>'ReasonCode' FROM intent_decision_runs r JOIN intent_run_events e ON e.run_id=r.id AND e.kind='end' WHERE r.id=$1`, run).Scan(&state, &reason); err != nil || state != "interrupted" || reason != tc.code {
					t.Fatalf("run %s end %q %q %v", run, state, reason, err)
				}
				t.Logf("job=%d original_run=%s original_claim=%s status=%s reason=%s", job, run, task.ClaimToken, state, reason)
				return
			}
			claimed, err := jobs.ClaimReadyJobs(ctx, 100)
			if err != nil {
				t.Fatal(err)
			}
			resumed := task
			resumed.ClaimToken = ""
			for _, item := range claimed {
				if item.ID == job {
					resumed.ClaimToken = item.ClaimToken
				}
			}
			if resumed.ClaimToken == "" || resumed.ClaimToken == task.ClaimToken {
				t.Fatalf("expected new claim for job %d", job)
			}
			defer func() {
				if err := jobs.CompleteJob(ctx, job, resumed.ClaimToken); err != nil {
					t.Error(err)
				}
			}()
			if run != "" {
				if err := r.AppendDecisionStep(ctx, task, run, 6, "cooldown_resolved", contracts.LiveDecisionFacts{}); err == nil {
					t.Fatal("old claim appended a decision step")
				}
			}
			decision, ok, err := r.LoadDecisionRecovery(ctx, resumed)
			if err != nil || ok != (tc.code == "decision_resume") {
				t.Fatalf("load recovery %v %v", ok, err)
			}
			if !ok {
				newRun, owned, err := r.StartDecisionRun(ctx, resumed)
				if err != nil || !owned || newRun == run {
					t.Fatalf("new run %s %v %v", newRun, owned, err)
				}
				t.Logf("job=%d original_run=%s original_claim=%s new_run=%s new_claim=%s", job, run, task.ClaimToken, newRun, resumed.ClaimToken)
				return
			}
			if decision.RunID != run || decision.Decision != domain.DecisionReply {
				t.Fatalf("unexpected recovered decision %+v", decision)
			}
			var decisionID int64
			var count, ends int
			if err := pool.QueryRow(ctx, `SELECT r.decision_id,(SELECT count(*) FROM intent_decisions WHERE job_id=r.job_id),(SELECT count(*) FROM intent_run_events WHERE run_id=r.id AND kind='end') FROM intent_decision_runs r WHERE r.id=$1`, run).Scan(&decisionID, &count, &ends); err != nil || decisionID <= 0 || count != 1 || ends != 1 || (previousID != 0 && previousID != decisionID) {
				t.Fatalf("decision identity %d %d %d %v", decisionID, count, ends, err)
			}
			if opened, err := r.BeginDecisionReply(ctx, resumed, run); err != nil || opened != tc.wantReply {
				t.Fatalf("resumed reply boundary %v %v", opened, err)
			}
			if opened, err := r.BeginDecisionReply(ctx, resumed, run); err != nil || opened {
				t.Fatalf("repeated reply boundary %v %v", opened, err)
			}
			t.Logf("job=%d original_run=%s original_claim=%s new_claim=%s decision=%d previous_decision=%d reply_opened=%v", job, run, task.ClaimToken, resumed.ClaimToken, decisionID, previousID, tc.wantReply)
		})
	}
}

func TestLiveSemanticCommitConfirmation(t *testing.T) {
	ctx := context.Background()
	pool := dbtest.Open(t)
	r, err := NewDecisionsRepo(pool)
	if err != nil {
		t.Fatal(err)
	}
	job, bot, conv := seedJob(t, pool)
	task := domain.Task{JobID: job, BotID: bot, ConversationID: conv, ClaimToken: dbtest.Claim(t, pool, job)}
	run, owned, err := r.StartDecisionRun(ctx, task)
	if err != nil || !owned {
		t.Fatalf("start %v %v", owned, err)
	}
	commitErr := errors.New("fixture commit acknowledgement unavailable")
	facts := contracts.LiveDecisionFacts{}
	if err := r.confirmDecisionStep(ctx, task, run, 4, "semantic_started", facts, commitErr); !errors.Is(err, commitErr) {
		t.Fatalf("uncommitted step: %v", err)
	}
	if err := r.AppendDecisionStep(ctx, task, run, 4, "semantic_started", facts); err != nil {
		t.Fatal(err)
	}
	if err := r.confirmDecisionStep(ctx, task, run, 4, "semantic_started", facts, commitErr); err != nil {
		t.Fatalf("committed step: %v", err)
	}
	var count int
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM intent_run_events WHERE run_id=$1 AND sequence=4", run).Scan(&count); err != nil || count != 1 {
		t.Fatalf("step count %d %v", count, err)
	}
	t.Logf("job=%d original_run=%s original_claim=%s uncommitted=unconfirmed committed=confirmed steps=%d", job, run, task.ClaimToken, count)
}

func TestLiveCompletedDecisionStopsGenericRetry(t *testing.T) {
	ctx := context.Background()
	pool := dbtest.Open(t)
	r, err := NewDecisionsRepo(pool)
	if err != nil {
		t.Fatal(err)
	}
	jobs, err := conversationinfra.NewRepo(pool)
	if err != nil {
		t.Fatal(err)
	}
	job, bot, conv := seedJob(t, pool)
	task := domain.Task{JobID: job, BotID: bot, ConversationID: conv, ClaimToken: dbtest.Claim(t, pool, job)}
	run, owned, err := r.StartDecisionRun(ctx, task)
	if err != nil || !owned {
		t.Fatalf("start %v %v", owned, err)
	}
	decision := domain.FinalDecision{RunID: run, Decision: domain.DecisionReply, ReasonCode: "always_reply"}
	if err := r.RecordDecisionRun(ctx, job, decision, task.ClaimToken); err != nil {
		t.Fatal(err)
	}
	if opened, err := r.BeginDecisionReply(ctx, task, run); err != nil || !opened {
		t.Fatalf("reply opened=%v %v", opened, err)
	}
	if err := jobs.FailJob(ctx, job, "process_error", 0, 5, task.ClaimToken); err != nil {
		t.Fatal(err)
	}
	var state, code string
	var attempts int
	if err := pool.QueryRow(ctx, "SELECT status,last_error_code,attempts FROM message_jobs WHERE id=$1", job).Scan(&state, &code, &attempts); err != nil || state != "failed" || code != "process_error" || attempts != 1 {
		t.Fatalf("job state %s %s %d %v", state, code, attempts, err)
	}
}
