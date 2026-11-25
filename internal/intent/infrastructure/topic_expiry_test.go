package infrastructure

import (
	"context"
	"encoding/json"
	"slices"
	"testing"
	"time"

	conversation "github.com/HappyLadySauceM/Nagi/internal/conversation/domain"
	conversationinfra "github.com/HappyLadySauceM/Nagi/internal/conversation/infrastructure"
	"github.com/HappyLadySauceM/Nagi/internal/intent/domain"
	"github.com/HappyLadySauceM/Nagi/pkg/db/dbtest"
)

func TestTopicExpiryUsesItsConfirmedEventTime(t *testing.T) {
	pool := dbtest.Open(t)
	ctx := context.Background()
	conv, _ := conversationinfra.NewRepo(pool)
	repo, _ := NewDecisionsRepo(pool)
	const idle = 300 * time.Second
	at := time.Now().UTC().Truncate(time.Millisecond).Add(-time.Hour)
	bot := time.Now().UnixNano()
	in := conversation.IngestInput{BotID: bot, Kind: conversation.KindGroup, TargetID: bot, SenderID: 10011, Text: "合成主题"}
	ingest := func(event time.Time) conversation.IngestResult {
		t.Helper()
		in.PlatformMessageID++
		row, err := conv.Ingest(ctx, in)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := pool.Exec(ctx, `UPDATE messages SET created_at=$1 WHERE id=$2`, event, row.MessageID); err != nil {
			t.Fatal(err)
		}
		return row
	}
	save := func(row conversation.IngestResult, topic int64, action string, event time.Time, idle time.Duration) {
		t.Helper()
		claim := dbtest.Claim(t, pool, row.JobID)
		change := &domain.TopicChange{ID: topic, Action: action, ExpiresAt: event.Add(idle)}
		decision := domain.FinalDecision{Decision: domain.DecisionSilent, Topic: change, Snapshot: &domain.DecisionSnapshot{Input: domain.SnapshotInput{TopicChange: change}}}
		if err := repo.Record(ctx, row.JobID, decision, claim); err != nil {
			t.Fatal(err)
		}
		if err := conv.CompleteJob(ctx, row.JobID, claim); err != nil {
			t.Fatal(err)
		}
	}
	a := ingest(at)
	save(a, a.JobID, "opened", at, idle)
	b := ingest(at.Add(10 * time.Second))
	save(b, b.JobID, "opened", at.Add(10*time.Second), idle)
	bt := at.Add(time.Minute)
	save(ingest(bt), b.JobID, domain.TopicContribution, bt, idle)
	old := at.Add(5 * time.Second)
	save(ingest(old), b.JobID, domain.TopicFollowUp, old, idle)
	duplicate, err := conv.Ingest(ctx, in)
	if err != nil || !duplicate.Duplicate {
		t.Fatalf("replay=%+v err=%v", duplicate, err)
	}
	for _, tc := range []struct {
		delta  time.Duration
		active bool
	}{
		{-time.Millisecond, true}, {0, false}, {time.Millisecond, false},
	} {
		event := at.Add(idle + tc.delta)
		current := ingest(event)
		task := domain.Task{BotID: bot, JobID: current.JobID, ConversationID: current.ConversationID}
		topics, err := repo.Topics(ctx, task, 0, int(idle.Seconds()))
		if err != nil {
			t.Fatal(err)
		}
		has := func(id int64) bool {
			return slices.ContainsFunc(topics.Candidates, func(c domain.TopicCandidate) bool { return c.ID == id && c.Active })
		}
		if has(a.JobID) != tc.active || !has(b.JobID) {
			t.Fatalf("delta=%s candidates=%+v", tc.delta, topics.Candidates)
		}
	}
	checkExpiry := func(id int64, expected time.Time, expectedVersion int64) {
		t.Helper()
		var got time.Time
		var version int64
		if err := pool.QueryRow(ctx, `SELECT expires_at,version FROM group_topics WHERE id=$1`, id).Scan(&got, &version); err != nil || !got.Equal(expected) || version != expectedVersion {
			t.Fatalf("topic %d expiry=%s version=%d want %s/%d err=%v", id, got, version, expected, expectedVersion, err)
		}
	}
	checkExpiry(a.JobID, at.Add(idle), 1)
	checkExpiry(b.JobID, bt.Add(idle), 2)
	query := ingest(at.Add(idle + 2*time.Millisecond))
	fresh, _ := NewDecisionsRepo(pool)
	topics, err := fresh.Topics(ctx, domain.Task{BotID: bot, JobID: query.JobID, ConversationID: query.ConversationID}, 1, int(idle.Seconds()))
	if err != nil || topics.QuotedID != a.JobID || len(topics.Candidates) != 2 || topics.Candidates[0].Active {
		t.Fatalf("expired quote=%+v err=%v", topics, err)
	}
	save(query, a.JobID, "reopened", at.Add(idle+2*time.Millisecond), idle)
	checkExpiry(a.JobID, at.Add(2*idle+2*time.Millisecond), 2)
	checkExpiry(b.JobID, bt.Add(idle), 2)
	short := ingest(at.Add(8 * time.Minute))
	shortAt := at.Add(8 * time.Minute)
	save(short, short.JobID, "opened", shortAt, idle)
	followAt := shortAt.Add(40 * time.Second)
	follow := ingest(followAt)
	save(follow, short.JobID, domain.TopicFollowUp, followAt, time.Minute)
	expires := shortAt.Add(100 * time.Second)
	checkExpiry(short.JobID, expires, 2)
	var snapshot []byte
	if err := pool.QueryRow(ctx, `SELECT s.input FROM intent_decision_snapshots s JOIN intent_decisions d ON d.id=s.decision_id WHERE d.job_id=$1`, follow.JobID).Scan(&snapshot); err != nil {
		t.Fatal(err)
	}
	var input domain.SnapshotInput
	if err := json.Unmarshal(snapshot, &input); err != nil || input.TopicChange == nil || !input.TopicChange.ExpiresAt.Equal(expires) {
		t.Fatalf("snapshot=%+v err=%v", input.TopicChange, err)
	}
	late := ingest(shortAt.Add(10 * time.Second))
	save(late, short.JobID, domain.TopicFollowUp, shortAt.Add(10*time.Second), idle)
	if duplicate, err := conv.Ingest(ctx, in); err != nil || !duplicate.Duplicate {
		t.Fatalf("duplicate=%+v err=%v", duplicate, err)
	}
	checkExpiry(short.JobID, expires, 2)
	for _, delta := range []time.Duration{-time.Millisecond, 0, time.Millisecond} {
		row := ingest(expires.Add(delta))
		current, err := repo.Topics(ctx, domain.Task{BotID: bot, JobID: row.JobID, ConversationID: row.ConversationID}, 0, 60)
		if err != nil {
			t.Fatal(err)
		}
		found := slices.ContainsFunc(current.Candidates, func(c domain.TopicCandidate) bool {
			return c.ID == short.JobID && c.Active && c.ExpiresAt.Equal(expires)
		})
		if found != (delta < 0) {
			t.Fatalf("shortened boundary %v, candidates=%+v", delta, current.Candidates)
		}
	}

}
