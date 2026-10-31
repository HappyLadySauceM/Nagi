package infrastructure

import (
	"context"
	"errors"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/HappyLadySauceM/Nagi/internal/conversation/domain"
	"github.com/HappyLadySauceM/Nagi/pkg/db"
	"github.com/HappyLadySauceM/Nagi/pkg/db/dbtest"
	"github.com/google/uuid"
)

func TestClaimsSerializeConversationAcrossPools(t *testing.T) {
	pool := dbtest.Open(t)
	dsn := os.Getenv("NAGI_TEST_DSN")
	if dsn == "" {
		t.Skip("explicit isolated NAGI_TEST_DSN required for second-pool test")
	}
	other, err := db.Connect(context.Background(), dsn, 4)
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	a, _ := NewRepo(pool)
	b, _ := NewRepo(other)
	ctx := context.Background()
	base := time.Now().UnixNano()
	ids := map[int64]bool{}
	var conversation string
	for i := 0; i < 3; i++ {
		v, err := a.Ingest(ctx, domain.IngestInput{BotID: 998001, Kind: domain.KindGroup, TargetID: base, PlatformMessageID: base + int64(i), SenderID: 998002, Text: "lease"})
		if err != nil {
			t.Fatal(err)
		}
		ids[v.JobID] = true
		conversation = v.ConversationID
	}
	t.Cleanup(func() {
		for id := range ids {
			_, _ = pool.Exec(ctx, "DELETE FROM message_jobs WHERE id=$1", id)
		}
	})
	start := make(chan struct{})
	var wg sync.WaitGroup
	var mu sync.Mutex
	var claimed []domain.Job
	var issues []error
	for _, repo := range []*Repo{a, b, a, b} {
		wg.Add(1)
		go func(repo *Repo) {
			defer wg.Done()
			<-start
			jobs, err := repo.ClaimReadyJobs(ctx, 20)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				issues = append(issues, err)
			}
			for _, job := range jobs {
				if job.ConversationID == conversation {
					claimed = append(claimed, job)
				}
			}
		}(repo)
	}
	close(start)
	wg.Wait()
	if len(issues) > 0 {
		t.Fatal(issues)
	}
	if len(claimed) != 1 {
		t.Fatalf("claimed %d concurrent jobs for one conversation", len(claimed))
	}
	job := claimed[0]
	if err := b.CompleteJob(ctx, job.ID, uuid.NewString()); !errors.Is(err, ErrLeaseLost) {
		t.Fatalf("foreign claim completed task: %v", err)
	}
	if err := a.CompleteJob(ctx, job.ID, job.ClaimToken); err != nil {
		t.Fatal(err)
	}
	next, err := b.ClaimReadyJobs(ctx, 20)
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, j := range next {
		if j.ConversationID == conversation {
			count++
			if err := b.CompleteJob(ctx, j.ID, j.ClaimToken); err != nil {
				t.Fatal(err)
			}
		}
	}
	if count != 1 {
		t.Fatalf("next conversation claim count=%d", count)
	}
}

func TestExpiredClaimsCannotRenewOrWriteAfterTakeover(t *testing.T) {
	pool := dbtest.Open(t)
	repo, _ := NewRepo(pool)
	ctx := context.Background()
	base := time.Now().UnixNano()
	v, err := repo.Ingest(ctx, domain.IngestInput{BotID: 998011, Kind: domain.KindGroup, TargetID: base, PlatformMessageID: base, SenderID: 998012, Text: "expire"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(ctx, "DELETE FROM message_jobs WHERE id=$1", v.JobID) })
	jobs, err := repo.ClaimReadyJobs(ctx, 50)
	if err != nil {
		t.Fatal(err)
	}
	var job domain.Job
	for _, j := range jobs {
		if j.ID == v.JobID {
			job = j
		}
	}
	if job.ClaimToken == "" {
		t.Fatal("no claim")
	}
	renewed, err := repo.RenewClaims(ctx, []string{job.ClaimToken})
	if err != nil || len(renewed) != 1 {
		t.Fatalf("renew=%v err=%v", renewed, err)
	}
	if _, err := pool.Exec(ctx, "UPDATE message_jobs SET lease_expires_at=clock_timestamp()-interval '1 second' WHERE id=$1", v.JobID); err != nil {
		t.Fatal(err)
	}
	renewed, err = repo.RenewClaims(ctx, []string{job.ClaimToken})
	if err != nil || len(renewed) != 0 {
		t.Fatalf("expired claim renewed: %v %v", renewed, err)
	}
	if err := repo.CompleteJob(ctx, job.ID, job.ClaimToken); !errors.Is(err, ErrLeaseLost) {
		t.Fatalf("expired completion: %v", err)
	}
	if err := repo.RecoverStaleJobs(ctx, domain.RecoveryPolicy{MaxAttempts: 3}); err != nil {
		t.Fatal(err)
	}
	jobs, err = repo.ClaimReadyJobs(ctx, 50)
	if err != nil {
		t.Fatal(err)
	}
	var next domain.Job
	for _, j := range jobs {
		if j.ID == v.JobID {
			next = j
		}
	}
	if next.ClaimToken == "" || next.ClaimToken == job.ClaimToken {
		t.Fatal("takeover did not replace claim")
	}
	if err := repo.ReleaseJob(ctx, job.ID, job.ClaimToken); !errors.Is(err, ErrLeaseLost) {
		t.Fatalf("stale release changed newer run: %v", err)
	}
	if err := repo.CompleteJob(ctx, next.ID, next.ClaimToken); err != nil {
		t.Fatal(err)
	}
	if err := repo.CompleteJob(ctx, next.ID, next.ClaimToken); !errors.Is(err, ErrLeaseLost) {
		t.Fatalf("zero-row duplicate completion succeeded: %v", err)
	}
}

func TestBusyConversationDoesNotStarveReadyConversation(t *testing.T) {
	pool := dbtest.Open(t)
	repo, _ := NewRepo(pool)
	ctx := context.Background()
	const bot int64 = 998031
	busyID := "00000000-0000-0000-0000-000000000001"
	freeID := "00000000-0000-0000-0000-000000000002"
	if _, err := pool.Exec(ctx, "INSERT INTO bot_accounts(bot_id) VALUES($1) ON CONFLICT DO NOTHING", bot); err != nil {
		t.Fatal(err)
	}
	for i, id := range []string{busyID, freeID} {
		if _, err := pool.Exec(ctx, "INSERT INTO conversations(id,bot_id,kind,target_id) VALUES($1,$2,'group',$3)", id, bot, int64(i+1)); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, "DELETE FROM message_jobs WHERE incoming_message_id IN(SELECT id FROM messages WHERE bot_id=$1)", bot)
		_, _ = pool.Exec(ctx, "DELETE FROM messages WHERE bot_id=$1", bot)
		_, _ = pool.Exec(ctx, "DELETE FROM conversations WHERE bot_id=$1", bot)
		_, _ = pool.Exec(ctx, "DELETE FROM bot_accounts WHERE bot_id=$1", bot)
	})
	for i, target := range []int64{1, 1, 2} {
		_, err := repo.Ingest(ctx, domain.IngestInput{BotID: bot, Kind: domain.KindGroup, TargetID: target, PlatformMessageID: int64(i + 1), SenderID: bot + 1, Text: "fair claim"})
		if err != nil {
			t.Fatal(err)
		}
	}
	first, err := repo.ClaimReadyJobs(ctx, 1)
	if err != nil || len(first) != 1 || first[0].ConversationID != busyID {
		t.Fatalf("first=%+v err=%v", first, err)
	}
	next, err := repo.ClaimReadyJobs(ctx, 1)
	if err != nil || len(next) != 1 || next[0].ConversationID != freeID {
		t.Fatalf("ready conversation starved: %+v %v", next, err)
	}
}

func TestFailJobCannotOverwriteClaimAfterLockWait(t *testing.T) {
	pool := dbtest.Open(t)
	repo, _ := NewRepo(pool)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	base := time.Now().UnixNano()
	v, err := repo.Ingest(ctx, domain.IngestInput{BotID: 998051, Kind: domain.KindGroup, TargetID: base, PlatformMessageID: base, SenderID: 998052, Text: "fenced write"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(context.Background(), "DELETE FROM message_jobs WHERE id=$1", v.JobID) })
	token := dbtest.Claim(t, pool, v.JobID)
	blocker, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer blocker.Rollback(ctx)
	var id string
	if err := blocker.QueryRow(ctx, "SELECT id FROM conversations WHERE id=$1 FOR UPDATE", v.ConversationID).Scan(&id); err != nil {
		t.Fatal(err)
	}
	var job int64
	if err := blocker.QueryRow(ctx, "SELECT id FROM message_jobs WHERE id=$1 FOR UPDATE", v.JobID).Scan(&job); err != nil {
		t.Fatal(err)
	}
	finished := make(chan error, 1)
	go func() { finished <- repo.FailJob(ctx, v.JobID, "old_worker", 0, 5, token) }()
	waitFor(t, func() bool {
		var blocked bool
		err := pool.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE datname=current_database() AND wait_event_type='Lock' AND pid<>pg_backend_pid())").Scan(&blocked)
		return err == nil && blocked
	})
	next := uuid.NewString()
	if _, err := blocker.Exec(ctx, "UPDATE message_jobs SET claim_token=$2::uuid,lease_expires_at=clock_timestamp()+interval '2 minutes' WHERE id=$1", v.JobID, next); err != nil {
		t.Fatal(err)
	}
	if err := blocker.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-finished:
		if !errors.Is(err, ErrLeaseLost) {
			t.Fatalf("old failure overwrote new claim: %v", err)
		}
	case <-ctx.Done():
		t.Fatal("failure writer did not unblock")
	}
	var actual, status string
	var attempts int
	if err := pool.QueryRow(ctx, "SELECT claim_token,status,attempts FROM message_jobs WHERE id=$1", v.JobID).Scan(&actual, &status, &attempts); err != nil {
		t.Fatal(err)
	}
	if actual != next || status != "processing" || attempts != 0 {
		t.Fatalf("new claim mutated: %s %s %d", actual, status, attempts)
	}
}

func TestPurgePreservesProcessingConversation(t *testing.T) {
	pool := dbtest.Open(t)
	repo, _ := NewRepo(pool)
	ctx := context.Background()
	base := time.Now().UnixNano()
	v, err := repo.Ingest(ctx, domain.IngestInput{BotID: 998061, Kind: domain.KindGroup, TargetID: base, PlatformMessageID: base, SenderID: 998062, Text: "retention lease"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(ctx, "DELETE FROM message_jobs WHERE id=$1", v.JobID) })
	token := dbtest.Claim(t, pool, v.JobID)
	if _, err := pool.Exec(ctx, "UPDATE messages SET created_at=clock_timestamp()-interval '40 days' WHERE id=$1", v.MessageID); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.PurgeBefore(ctx, time.Now().Add(-30*24*time.Hour), 1000); err != nil {
		t.Fatal(err)
	}
	var exists bool
	if err := pool.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM messages WHERE id=$1)", v.MessageID).Scan(&exists); err != nil || !exists {
		t.Fatalf("live message purged: %v %v", exists, err)
	}
	if err := repo.CompleteJob(ctx, v.JobID, token); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.PurgeBefore(ctx, time.Now().Add(-30*24*time.Hour), 1000); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM messages WHERE id=$1)", v.MessageID).Scan(&exists); err != nil || exists {
		t.Fatalf("completed history retained unexpectedly: %v %v", exists, err)
	}
}
