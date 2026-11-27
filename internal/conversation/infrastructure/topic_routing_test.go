//go:build integration

package infrastructure

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/HappyLadySauceM/Nagi/internal/conversation/domain"
	"github.com/HappyLadySauceM/Nagi/pkg/db/dbtest"
)

func TestTopicRoutingPersistsAndFencesClaims(t *testing.T) {
	pool := dbtest.Open(t)
	workerA, err := NewRepo(pool)
	if err != nil {
		t.Fatal(err)
	}
	workerB, err := NewRepo(pool)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	base := time.Now().UnixNano()
	// Leave routes from other tests intact and skip their conversations during the claim probe.
	// 保留其他用例的归属标记，领取探针跳过其会话。
	others, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer others.Rollback(ctx)
	if _, err := others.Exec(ctx, `SELECT id FROM conversations WHERE bot_id<>$1 FOR UPDATE`, base); err != nil {
		t.Fatal(err)
	}
	input := domain.IngestInput{BotID: base, Kind: domain.KindGroup, TargetID: base, PlatformMessageID: 8, SenderID: base + 1, Text: "synthetic topic"}
	ingest := func(in domain.IngestInput) domain.IngestResult {
		t.Helper()
		result, err := workerB.Ingest(ctx, in)
		if err != nil {
			t.Fatal(err)
		}
		return result
	}
	count := func(query string, id int64, want int) {
		t.Helper()
		var n int
		if err := pool.QueryRow(ctx, query, id).Scan(&n); err != nil || n != want {
			t.Fatalf("count=%d, want=%d, err=%v", n, want, err)
		}
	}
	claim := func(repo *Repo, message int64) domain.TopicRouteClaim {
		t.Helper()
		got, err := repo.ClaimTopicRoute(ctx, 10*time.Second)
		if err != nil || got == nil || got.MessageID != message {
			t.Fatalf("claim=%+v want=%d err=%v", got, message, err)
		}
		return *got
	}
	resolve := func(c domain.TopicRouteClaim, action domain.TopicRouteAction, topic int64, status string, version int64) domain.TopicRouteResult {
		t.Helper()
		got, err := workerB.ResolveTopicRoute(ctx, c, domain.TopicRouteResolution{Action: action, TopicID: topic, ExpiresAt: time.Now().Add(time.Minute)})
		if err != nil || got.Version != version || got.Status != status {
			t.Fatalf("resolve=%+v want version=%d status=%s err=%v", got, version, status, err)
		}
		return got
	}
	first := ingest(input)
	if got := ingest(input); !got.Duplicate {
		t.Fatalf("replay=%+v", got)
	}
	count(`SELECT count(*) FROM group_topic_routes p JOIN messages m ON m.id=p.id JOIN message_jobs j ON j.incoming_message_id=m.id WHERE j.id=$1`, first.JobID, 1)
	for _, input := range []domain.IngestInput{
		{BotID: base, Kind: domain.KindGroup, TargetID: base, PlatformMessageID: 50, SenderID: base + 1, ArchiveOnly: true},
		{BotID: base, Kind: domain.KindPrivate, TargetID: base, PlatformMessageID: 50, SenderID: base + 1},
		{BotID: base, Kind: domain.KindGroup, TargetID: base, PlatformMessageID: 51, SenderID: base},
	} {
		count(`SELECT count(*) FROM group_topic_routes WHERE id=$1`, ingest(input).MessageID, 0)
	}
	// Platform IDs can be out of order. The source transaction's message order is authoritative.
	// 平台 ID 可乱序，同一源会话事务中的入站顺序才用于归属。
	input.PlatformMessageID = 7
	second := ingest(input)
	input.PlatformMessageID = 6
	third := ingest(input)
	old := claim(workerA, first.MessageID)
	if next, err := workerB.ClaimTopicRoute(ctx, time.Second); err != nil || next != nil {
		t.Fatalf("younger route claimed before head: %+v, %v", next, err)
	}
	if _, err := pool.Exec(ctx, `UPDATE group_topic_routes SET lease_expires_at=clock_timestamp()-interval '1 second' WHERE id=$1`, first.MessageID); err != nil {
		t.Fatal(err)
	}
	recovered := claim(workerB, first.MessageID)
	if recovered.Token == old.Token {
		t.Fatal("lease takeover kept the old token")
	}
	if _, err := workerA.ResolveTopicRoute(ctx, old, domain.TopicRouteResolution{Action: domain.TopicRouteOpen, ExpiresAt: time.Now().Add(time.Minute)}); !errors.Is(err, domain.ErrTopicRouteClaimLost) {
		t.Fatalf("old lease resolved: %v", err)
	}
	topic := resolve(recovered, domain.TopicRouteOpen, 0, "resolved", 1).TopicID
	if topic != first.JobID {
		t.Fatalf("topic=%d, first job=%d", topic, first.JobID)
	}
	secondClaim := claim(workerA, second.MessageID)
	if len(secondClaim.Candidates) != 1 || secondClaim.Candidates[0] != (domain.TopicRouteCandidate{TopicID: topic, Version: 1}) {
		t.Fatalf("later committed inbound missed topic: %+v", secondClaim)
	}
	resolve(secondClaim, domain.TopicRouteCorrection, topic, "resolved", 2)
	thirdClaim := claim(workerB, third.MessageID)
	if len(thirdClaim.Candidates) != 1 || thirdClaim.Candidates[0].Version != 2 {
		t.Fatalf("queued candidate used old version: %+v", thirdClaim)
	}
	resolve(thirdClaim, domain.TopicRouteClose, topic, "resolved", 3)
	if _, err := workerA.ResolveTopicRoute(ctx, thirdClaim, domain.TopicRouteResolution{Action: domain.TopicRouteClose, TopicID: topic}); !errors.Is(err, domain.ErrTopicRouteClaimLost) {
		t.Fatalf("repeated resolution: %v", err)
	}
	count(`SELECT count(*) FROM group_topic_inputs WHERE topic_id=$1`, topic, 3)
	var closed bool
	if err := pool.QueryRow(ctx, `SELECT closed FROM group_topics WHERE id=$1`, topic).Scan(&closed); err != nil || !closed {
		t.Fatalf("closed=%v err=%v", closed, err)
	}
	input.TargetID++
	fourth := ingest(input)
	other := claim(workerB, fourth.MessageID)
	if len(other.Candidates) != 0 {
		t.Fatalf("cross-group candidate: %+v", other)
	}
	_, err = pool.Exec(ctx, `INSERT INTO group_topic_route_candidates(id,conversation_id,route_id,topic_id,version) VALUES($1,$2,$3,$4,3)`, uuid.New(), fourth.ConversationID, fourth.MessageID, topic)
	var pgerr *pgconn.PgError
	if !errors.As(err, &pgerr) || pgerr.Code != "23503" {
		t.Fatalf("cross-group foreign key: %v", err)
	}
	resolve(other, domain.TopicRouteUnrelated, 0, "unrelated", 0)
	count(`SELECT count(*) FROM group_topic_inputs WHERE topic_id=$1`, topic, 3)
	input.PlatformMessageID--
	fifth := ingest(input)
	if _, err := pool.Exec(ctx, `UPDATE group_topic_routes SET deadline=clock_timestamp()-interval '1 second' WHERE id=$1`, fifth.MessageID); err != nil {
		t.Fatal(err)
	}
	if next, err := workerB.ClaimTopicRoute(ctx, time.Second); err != nil || next != nil {
		t.Fatalf("expired route claimed: %+v, %v", next, err)
	}
	count(`SELECT count(*) FROM group_topic_routes WHERE id=$1 AND status='expired' AND claim_token IS NULL`, fifth.MessageID, 1)
}

func TestIngestAccountDoesNotBlockOtherGroup(t *testing.T) {
	pool := dbtest.Open(t)
	repo, err := NewRepo(pool)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	base := time.Now().UnixNano()
	in := domain.IngestInput{BotID: base, Kind: domain.KindGroup, TargetID: base, PlatformMessageID: base, SenderID: base + 1, Text: "test"}
	seeded, err := repo.Ingest(ctx, in)
	if err != nil {
		t.Fatal(err)
	}
	blocker, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer blocker.Rollback(ctx)
	var id string
	if err := blocker.QueryRow(ctx, `SELECT id FROM conversations WHERE id=$1 FOR UPDATE`, seeded.ConversationID).Scan(&id); err != nil {
		t.Fatal(err)
	}
	in.PlatformMessageID++
	waiting := make(chan error, 1)
	go func(input domain.IngestInput) { _, err := repo.Ingest(ctx, input); waiting <- err }(in)
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		var blocked bool
		if err := pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_stat_activity
      WHERE datname=current_database() AND pid<>pg_backend_pid() AND wait_event_type='Lock'
      AND query LIKE 'INSERT INTO "conversations"%')`).Scan(&blocked); err != nil {
			t.Fatal(err)
		}
		if blocked {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal("first group never reached the conversation lock")
		case err := <-waiting:
			t.Fatalf("first group did not wait: %v", err)
		case <-ticker.C:
		}
	}
	in.TargetID++
	otherCtx, stop := context.WithTimeout(ctx, time.Second)
	defer stop()
	if _, err := repo.Ingest(otherCtx, in); err != nil {
		t.Fatalf("account held across groups: %v", err)
	}
	if err := blocker.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-waiting:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal("first group did not resume")
	}
}
