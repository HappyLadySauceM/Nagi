//go:build integration

package infrastructure

import (
	"context"
	"testing"
	"time"

	conversationinfra "github.com/HappyLadySauceM/Nagi/internal/conversation/infrastructure"
	"github.com/HappyLadySauceM/Nagi/internal/intent/contracts"
	"github.com/HappyLadySauceM/Nagi/internal/intent/domain"
	"github.com/HappyLadySauceM/Nagi/pkg/db/dbtest"
)

func TestLiveDecisionJournalAndScope(t *testing.T) {
	pool := dbtest.Open(t)
	job, bot, conversation := seedJob(t, pool)
	ctx := context.Background()
	task := domain.Task{JobID: job, BotID: bot, ConversationID: conversation, ClaimToken: dbtest.Claim(t, pool, job)}
	repo, err := NewDecisionsRepo(pool)
	if err != nil {
		t.Fatal(err)
	}
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := pool.Exec(ctx, query, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`INSERT INTO scope_policies(bot_id,scope_type,scope_id,enabled,store_messages,allow_history_read)
 SELECT bot_id,kind,target_id,true,true,true FROM conversations WHERE id=$1`, conversation)
	t.Cleanup(func() {
		exec(`DELETE FROM scope_policies p USING conversations c WHERE c.id=$1 AND p.bot_id=c.bot_id AND p.scope_type=c.kind AND p.scope_id=c.target_id`, conversation)
	})
	start, err := repo.DecisionRunSnapshot(ctx, bot, contracts.LiveHistoryKey{})
	if err != nil {
		t.Fatal(err)
	}
	id, owned, err := repo.StartDecisionRun(ctx, task)
	if err != nil || !owned {
		t.Fatalf("start %q %v %v", id, owned, err)
	}
	if duplicate, owned, _ := repo.StartDecisionRun(ctx, task); duplicate != id || owned {
		t.Fatalf("duplicate run %q %v", duplicate, owned)
	}
	facts := contracts.LiveDecisionFacts{Branch: "passed"}
	for range 2 {
		if err := repo.AppendDecisionStep(ctx, task, id, 2, "context", facts); err != nil {
			t.Fatal(err)
		}
	}
	if err := repo.AppendDecisionStep(ctx, task, id, 2, "context", contracts.LiveDecisionFacts{}); err == nil {
		t.Fatal("conflicting event accepted")
	}
	batch, err := repo.DecisionRunBatch(ctx, bot, start.Position, start.Scope)
	if err != nil || len(batch.Events) != 2 || batch.Events[0].RunID != id || batch.Events[0].Sequence != 1 || batch.Events[1].Sequence != 2 {
		t.Fatalf("ordered batch %+v %v", batch, err)
	}
	exec(`UPDATE message_jobs SET lease_expires_at=clock_timestamp()-interval '1 second' WHERE id=$1`, job)
	if err := repo.InterruptExpiredDecisionRuns(ctx); err != nil {
		t.Fatal(err)
	}
	if err := repo.AppendDecisionStep(ctx, task, id, 3, "rules", facts); err == nil {
		t.Fatal("expired claim accepted")
	}
	end, err := repo.DecisionRunBatch(ctx, bot, batch.Position, batch.Scope)
	if err != nil || len(end.Events) != 1 || end.Events[0].RunID != id || end.Events[0].Data.State != "interrupted" {
		t.Fatalf("interrupt batch %+v %v", end, err)
	}
	if err := repo.InterruptExpiredDecisionRuns(ctx); err != nil {
		t.Fatal(err)
	}
	exec(`UPDATE message_jobs SET claim_token=gen_random_uuid() WHERE id=$1`, job)
	task.ClaimToken = dbtest.Claim(t, pool, job)
	second, owned, err := repo.StartDecisionRun(ctx, task)
	if err != nil || !owned || second == id {
		t.Fatalf("new claim %q %v %v", second, owned, err)
	}
	if err := repo.RecordDecisionRun(ctx, job, domain.FinalDecision{RunID: second, Decision: domain.DecisionSilent, ReasonCode: "observe_only"}, task.ClaimToken); err != nil {
		t.Fatal(err)
	}
	snapshot, err := repo.DecisionRunSnapshot(ctx, bot, contracts.LiveHistoryKey{})
	if err != nil {
		t.Fatal(err)
	}
	found := 0
	for _, row := range snapshot.Rows {
		if row.RunID == second && row.State == "completed" && row.DecisionID > 0 && len(row.Events) == 2 && row.Events[1].Data.DecisionID == row.DecisionID {
			found++
		}
		if row.RunID == id && row.State == "interrupted" {
			found++
		}
	}
	if found != 2 {
		t.Fatalf("terminal snapshot %+v", snapshot.Rows)
	}
	exec(`UPDATE scope_policies p SET enabled=false,version=p.version+1 FROM conversations c WHERE c.id=$1 AND p.bot_id=c.bot_id AND p.scope_type=c.kind AND p.scope_id=c.target_id`, conversation)
	invalid, err := repo.DecisionRunBatch(ctx, bot, snapshot.Position, snapshot.Scope)
	if err != nil || !invalid.Reset || len(invalid.Events) != 0 {
		t.Fatalf("scope reset %+v %v", invalid, err)
	}
}

func TestLiveWriteRechecksLeaseAfterHeadLock(t *testing.T) {
	pool := dbtest.Open(t)
	repo, err := NewDecisionsRepo(pool)
	if err != nil {
		t.Fatal(err)
	}
	for _, terminal := range []bool{false, true} {
		name := "step"
		if terminal {
			name = "formal terminal"
		}
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			job, bot, conv := seedJob(t, pool)
			task := domain.Task{JobID: job, BotID: bot, ConversationID: conv, ClaimToken: dbtest.Claim(t, pool, job)}
			run, owned, err := repo.StartDecisionRun(ctx, task)
			if err != nil || !owned {
				t.Fatalf("start %s %v %v", run, owned, err)
			}
			var position int64
			if err := pool.QueryRow(ctx, `SELECT position FROM intent_stream_heads WHERE bot_id=$1`, bot).Scan(&position); err != nil {
				t.Fatal(err)
			}
			lock, err := pool.Begin(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer lock.Rollback(ctx)
			var pid int
			if err := lock.QueryRow(ctx, `SELECT pg_backend_pid() FROM intent_stream_heads WHERE bot_id=$1 FOR UPDATE`, bot).Scan(&pid); err != nil {
				t.Fatal(err)
			}
			if _, err := pool.Exec(ctx, `UPDATE message_jobs SET lease_expires_at=clock_timestamp()+interval '2 seconds' WHERE id=$1`, job); err != nil {
				t.Fatal(err)
			}
			done := make(chan error, 1)
			go func() {
				if terminal {
					done <- repo.RecordDecisionRun(ctx, job, domain.FinalDecision{RunID: run, Decision: domain.DecisionSilent, ReasonCode: "observe_only"}, task.ClaimToken)
					return
				}
				done <- repo.AppendDecisionStep(ctx, task, run, 2, "context", contracts.LiveDecisionFacts{})
			}()
			blocked := false
			for ctx.Err() == nil {
				if err := pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM pg_stat_activity WHERE $1=ANY(pg_blocking_pids(pid)))`, pid).Scan(&blocked); err != nil {
					t.Fatal(err)
				}
				if blocked {
					break
				}
				select {
				case err := <-done:
					t.Fatalf("write did not wait for head: %v", err)
				case <-time.After(10 * time.Millisecond):
				}
			}
			if !blocked {
				t.Fatal("no head lock wait observed")
			}
			for ctx.Err() == nil {
				var expired bool
				if err := pool.QueryRow(ctx, `SELECT lease_expires_at<=clock_timestamp() FROM message_jobs WHERE id=$1`, job).Scan(&expired); err != nil {
					t.Fatal(err)
				}
				if expired {
					break
				}
				time.Sleep(15 * time.Millisecond)
			}
			if err := lock.Commit(ctx); err != nil {
				t.Fatal(err)
			}
			if err := <-done; err == nil {
				t.Fatal("expired lease committed")
			}
			var state string
			var eventCount, decisions int
			var after int64
			if err := pool.QueryRow(ctx, `SELECT r.state,h.position,(SELECT count(*) FROM intent_run_events WHERE run_id=r.id),(SELECT count(*) FROM intent_decisions WHERE job_id=r.job_id) FROM intent_decision_runs r JOIN intent_stream_heads h USING(bot_id) WHERE r.id=$1`, run).Scan(&state, &after, &eventCount, &decisions); err != nil {
				t.Fatal(err)
			}
			if state != "running" || after != position || eventCount != 1 || decisions != 0 {
				t.Fatalf("uncommitted write outcome %s %d %d %d", state, after, eventCount, decisions)
			}
			if err := repo.InterruptExpiredDecisionRuns(ctx); err != nil {
				t.Fatal(err)
			}
			if err := pool.QueryRow(ctx, `SELECT state FROM intent_decision_runs WHERE id=$1`, run).Scan(&state); err != nil || state != "interrupted" {
				t.Fatalf("recovery %s %v", state, err)
			}
		})
	}
}

func TestLiveRecoveryUsesOnlyCurrentClaimFailure(t *testing.T) {
	pool := dbtest.Open(t)
	ctx := context.Background()
	decisions, err := NewDecisionsRepo(pool)
	if err != nil {
		t.Fatal(err)
	}
	conversations, err := conversationinfra.NewRepo(pool)
	if err != nil {
		t.Fatal(err)
	}
	for _, renew := range []bool{false, true} {
		job, bot, conv := seedJob(t, pool)
		task := domain.Task{JobID: job, BotID: bot, ConversationID: conv, ClaimToken: dbtest.Claim(t, pool, job)}
		run, owned, err := decisions.StartDecisionRun(ctx, task)
		if err != nil || !owned {
			t.Fatalf("start %s %v %v", run, owned, err)
		}
		if err := decisions.FailDecisionRun(ctx, task, run, "failed", "decision_persistence_failed"); err != nil {
			t.Fatal(err)
		}
		claim := task.ClaimToken
		if renew {
			claim = "141f4690-8072-49ca-9932-da35c29ab4bc"
		}
		if _, err := pool.Exec(ctx, `UPDATE message_jobs SET claim_token=$2,lease_expires_at=clock_timestamp()-interval '1 second' WHERE id=$1`, job, claim); err != nil {
			t.Fatal(err)
		}
		if err := conversations.RecoverStaleJobs(ctx); err != nil {
			t.Fatal(err)
		}
		var status, code string
		if err := pool.QueryRow(ctx, `SELECT status,last_error_code FROM message_jobs WHERE id=$1`, job).Scan(&status, &code); err != nil {
			t.Fatal(err)
		}
		if renew && (status != "pending" || code != "lease_expired") || !renew && (status != "failed" || code != "decision_persistence_failed") {
			t.Fatalf("renewed=%v status=%s code=%s", renew, status, code)
		}
	}
}

func TestPurgeInvalidatesHistoricalOnlyAccount(t *testing.T) {
	pool := dbtest.Open(t)
	ctx := context.Background()
	job, _, conv := seedJob(t, pool)
	bot := time.Now().UnixNano()
	if _, err := pool.Exec(ctx, `INSERT INTO bot_accounts(bot_id,nickname) VALUES ($1,'history fixture')`, bot); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE conversations SET bot_id=$1 WHERE id=$2`, bot, conv); err != nil {
		t.Fatal(err)
	}
	var message int64
	if err := pool.QueryRow(ctx, `UPDATE messages SET bot_id=$1,created_at='2001-01-01' WHERE conversation_id=$2 RETURNING id`, bot, conv).Scan(&message); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO scope_policies(bot_id,scope_type,scope_id,enabled,store_messages,allow_history_read)
	 SELECT bot_id,kind,target_id,true,true,true FROM conversations WHERE id=$1`, conv); err != nil {
		t.Fatal(err)
	}
	decisions, err := NewDecisionsRepo(pool)
	if err != nil {
		t.Fatal(err)
	}
	conversations, err := conversationinfra.NewRepo(pool)
	if err != nil {
		t.Fatal(err)
	}
	claim := dbtest.Claim(t, pool, job)
	if err := decisions.Record(ctx, job, domain.FinalDecision{Decision: domain.DecisionSilent, ReasonCode: "observe_only"}, claim); err != nil {
		t.Fatal(err)
	}
	if err := conversations.CompleteJob(ctx, job, claim); err != nil {
		t.Fatal(err)
	}
	before, err := decisions.DecisionRunSnapshot(ctx, bot, contracts.LiveHistoryKey{})
	if err != nil || before.Position != 0 || len(before.Rows) != 1 || before.Rows[0].State != "unknown" {
		t.Fatalf("history before purge %+v %v", before, err)
	}
	if allowed, err := decisions.DecisionSourceAccess(ctx, bot, conv, message); !allowed || err != nil {
		t.Fatalf("source before purge %v %v", allowed, err)
	}
	count, err := conversations.PurgeBefore(ctx, time.Date(2002, time.January, 1, 0, 0, 0, 0, time.UTC), 100)
	if err != nil || count < 1 {
		t.Fatalf("purged %d %v", count, err)
	}
	after, err := decisions.DecisionRunBatch(ctx, bot, before.Position, before.Scope)
	if err != nil || !after.Reset || len(after.Events) != 0 {
		t.Fatalf("old cursor %+v %v", after, err)
	}
	current, err := decisions.DecisionRunSnapshot(ctx, bot, contracts.LiveHistoryKey{})
	if err != nil || current.Scope == before.Scope || len(current.Rows) != 0 {
		t.Fatalf("history after purge %+v %v", current, err)
	}
	if allowed, err := decisions.DecisionSourceAccess(ctx, bot, conv, message); allowed || err != nil {
		t.Fatalf("source after purge %v %v", allowed, err)
	}
	var revision, progress int64
	if err := pool.QueryRow(ctx, `SELECT retention_revision,position FROM intent_stream_heads WHERE bot_id=$1`, bot).Scan(&revision, &progress); err != nil || revision != 1 || progress != 0 {
		t.Fatalf("historical-only head %d %d %v", revision, progress, err)
	}
}
