package infrastructure

import (
	"context"
	"errors"
	conversationdomain "github.com/HappyLadySauceM/Nagi/internal/conversation/domain"
	conversationinfra "github.com/HappyLadySauceM/Nagi/internal/conversation/infrastructure"
	"github.com/HappyLadySauceM/Nagi/pkg/db/dbtest"
	"io/fs"
	"strings"
	"testing"
	"time"

	rt "github.com/HappyLadySauceM/Nagi/internal/agent/infrastructure/replytool"
	ob "github.com/HappyLadySauceM/Nagi/internal/onebot/domain"
	"github.com/HappyLadySauceM/Nagi/internal/outbound/application"
	"github.com/HappyLadySauceM/Nagi/internal/outbound/domain"
	pa "github.com/HappyLadySauceM/Nagi/internal/plugins/application"
	"github.com/HappyLadySauceM/Nagi/pkg/db"
	"github.com/HappyLadySauceM/Nagi/pkg/db/migrations"
)

func TestPublicSendSharesBudgetDeduplicatesAndRecordsTarget(t *testing.T) {
	f := newLedgerFixture(t)
	ctx := context.Background()
	coordinator, _ := application.NewCoordinator(f.repo)
	limits := domain.Limits{MaxMessages: 2, MaxCharsPerMessage: 10, MaxTotalChars: 10, StopAfterFailure: true}
	// Reserve a current reply first: the public tool must share its budget.
	// 先预留当前会话回复，标准工具必须共享这份预算。
	first := f.reserve(t, "a", limits)
	_, err := application.Deliver(ctx, f.repo, first.ID, func(context.Context) (int64, error) { return -1, nil }, f.claim)
	if err != nil {
		t.Fatal(err)
	}
	scope := pa.Scope{ClaimToken: f.claim, JobID: f.jobID, BotID: f.botID, ConversationID: f.conversationID, Kind: "group", TargetID: 1, Permissions: map[string]bool{"qq.api.send_private_msg": true, "qq.cross_conversation": true, "qq.content.media": true}}
	f.grantScope(t, "private", int64(f.botID+50))
	ctx = pa.WithToolCallID(pa.WithScope(ctx, scope), "public-one")
	c := ob.PublicCommand{Name: ob.ActionSendPrivate, UserID: ob.UserID(f.botID + 50), Segments: []ob.Segment{ob.Text("你好"), {Type: ob.SegImage, Data: map[string]string{"file": "a.jpg"}}}}
	calls := 0
	send := func(context.Context, ob.PublicCommand) (ob.PublicResult, error) {
		calls++
		return ob.PublicResult{Data: map[string]any{"message_id": -22}, MessageID: -22}, nil
	}
	opts := rt.Options{Ledger: coordinator, Limits: limits}
	for i := 0; i < 2; i++ {
		result, err := rt.SendPublic(ctx, opts, c, send)
		if err != nil || result.MessageID != -22 {
			t.Fatalf("send %d: %+v %v", i, result, err)
		}
	}
	if calls != 1 {
		t.Fatalf("duplicate sends: %d", calls)
	}
	var messages, chars int
	if err := f.pool.QueryRow(ctx, "SELECT reserved_messages,reserved_chars FROM agent_run_budgets WHERE job_id=$1", f.jobID).Scan(&messages, &chars); err != nil || messages != 2 || chars != 4 {
		t.Fatalf("budget %d %d %v", messages, chars, err)
	}
	var targetKind, source string
	var targetID int64
	var segments string
	if err := f.pool.QueryRow(ctx, `SELECT c.kind,c.target_id,m.segments::text,o.conversation_id::text FROM messages m JOIN conversations c ON c.id=m.conversation_id JOIN agent_outbound_messages o ON o.id=m.outbound_id WHERE o.job_id=$1 AND o.tool_call_id='public-one'`, f.jobID).Scan(&targetKind, &targetID, &segments, &source); err != nil {
		t.Fatal(err)
	}
	if targetKind != "private" || targetID != int64(c.UserID) || source != f.conversationID || !strings.Contains(segments, "image") {
		t.Fatalf("wrong history: %s %d %s %s", targetKind, targetID, source, segments)
	}
	c.UserID++
	if _, err := rt.SendPublic(ctx, opts, c, send); err == nil {
		t.Fatal("same call changed target")
	}
	if _, err := rt.SendPublic(pa.WithToolCallID(ctx, "public-two"), opts, c, send); !errors.Is(err, domain.ErrBudgetExceeded) {
		t.Fatalf("budget bypass: %v", err)
	}
	if calls != 1 {
		t.Fatal("rejected calls reached platform")
	}
}

func TestPublicAsyncAndTimeoutNeverResend(t *testing.T) {
	for _, async := range []bool{true, false} {
		t.Run(map[bool]string{true: "async", false: "timeout"}[async], func(t *testing.T) {
			f := newLedgerFixture(t)
			coordinator, _ := application.NewCoordinator(f.repo)
			scope := pa.Scope{ClaimToken: f.claim, JobID: f.jobID, BotID: f.botID, ConversationID: f.conversationID, Kind: "group", TargetID: 1, Permissions: map[string]bool{"qq.api.send_private_msg": true, "qq.cross_conversation": true}}
			ctx := pa.WithToolCallID(pa.WithScope(context.Background(), scope), "call")
			text := "hello"
			c := ob.PublicCommand{Name: ob.ActionSendPrivate, UserID: 22, Text: &text, AutoEscape: true}
			calls := 0
			send := func(context.Context, ob.PublicCommand) (ob.PublicResult, error) {
				calls++
				if async {
					return ob.PublicResult{Async: true, MessageID: 99}, nil
				}
				return ob.PublicResult{}, context.DeadlineExceeded
			}
			opts := rt.Options{Ledger: coordinator, Limits: domain.Limits{MaxMessages: 3, StopAfterFailure: true}}
			for i := 0; i < 2; i++ {
				result, err := rt.SendPublic(ctx, opts, c, send)
				if err != nil || !result.Async {
					t.Fatalf("result=%+v err=%v", result, err)
				}
			}
			if calls != 1 {
				t.Fatalf("retried uncertain delivery: %d", calls)
			}
			rows, err := f.repo.ListByJob(ctx, f.jobID)
			if err != nil || len(rows) != 1 || rows[0].Status != domain.StatusUnknown {
				t.Fatalf("records=%+v err=%v", rows, err)
			}
		})
	}
}

func TestPublicMigrationBackfillsLegacyAndRoundTrips(t *testing.T) {
	f := newLedgerFixture(t)
	ctx := context.Background()
	record := f.reserve(t, "旧记录", domain.Limits{})
	files, err := fs.Glob(migrations.FS, "*public_api_delivery.down.sql")
	if err != nil || len(files) != 1 {
		t.Fatal(files, err)
	}
	down, _ := fs.ReadFile(migrations.FS, files[0])
	if _, err := f.pool.Exec(ctx, string(down)); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = db.Migrate(context.Background(), f.pool, migrations.FS) })
	for i := 0; i < 2; i++ {
		if _, err := db.Migrate(ctx, f.pool, migrations.FS); err != nil {
			t.Fatal(err)
		}
		var kind, action, payload string
		var target int64
		var chars int
		if err := f.pool.QueryRow(ctx, `SELECT target_kind,target_id,action,payload::text,charge_chars FROM agent_outbound_messages WHERE id=$1`, record.ID).Scan(&kind, &target, &action, &payload, &chars); err != nil {
			t.Fatal(err)
		}
		if kind != "group" || target <= 0 || action != "send_group_msg" || chars != 3 || !strings.Contains(payload, "旧记录") {
			t.Fatalf("backfill %s %d %s %d %s", kind, target, action, chars, payload)
		}
		if i == 0 {
			if _, err := f.pool.Exec(ctx, string(down)); err != nil {
				t.Fatal(err)
			}
		}
	}
}

func TestRichPreparedRecoveryRefundsChargedCost(t *testing.T) {
	f := newLedgerFixture(t)
	ctx := context.Background()
	record, err := f.repo.Reserve(ctx, domain.ReserveInput{ClaimToken: f.claim, JobID: f.jobID, BotID: f.botID, ConversationID: f.conversationID, Text: "", ChargeChars: 3, Payload: `{"segments":[{"type":"image","data":{"file":"a.jpg"}}]}`, Limits: domain.Limits{MaxMessages: 2}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.pool.Exec(ctx, "UPDATE message_jobs SET lease_expires_at=clock_timestamp()-interval '1 second' WHERE id=$1", f.jobID); err != nil {
		t.Fatal(err)
	}
	conversations, _ := conversationinfra.NewRepo(f.pool)
	if err := conversations.RecoverStaleJobs(ctx); err != nil {
		t.Fatal(err)
	}
	var messages, chars int
	var status string
	if err := f.pool.QueryRow(ctx, "SELECT reserved_messages,reserved_chars FROM agent_run_budgets WHERE job_id=$1", f.jobID).Scan(&messages, &chars); err != nil {
		t.Fatal(err)
	}
	if err := f.pool.QueryRow(ctx, "SELECT status FROM agent_outbound_messages WHERE id=$1", record.ID).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if messages != 0 || chars != 0 || status != "cancelled" {
		t.Fatalf("refund: messages=%d chars=%d status=%s", messages, chars, status)
	}
}

func TestReciprocalCrossConversationSettlement(t *testing.T) {
	f := newLedgerFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var sourceTarget int64
	if err := f.pool.QueryRow(ctx, "SELECT target_id FROM conversations WHERE id=$1", f.conversationID).Scan(&sourceTarget); err != nil {
		t.Fatal(err)
	}
	conversations, _ := conversationinfra.NewRepo(f.pool)
	second, err := conversations.Ingest(ctx, conversationdomain.IngestInput{BotID: f.botID, Kind: conversationdomain.KindGroup, TargetID: sourceTarget + 1, PlatformMessageID: time.Now().UnixNano(), SenderID: f.botID + 1, Text: "second"})
	if err != nil {
		t.Fatal(err)
	}
	defer f.pool.Exec(context.Background(), "DELETE FROM message_jobs WHERE id=$1", second.JobID)
	claim := dbtest.Claim(t, f.pool, second.JobID)
	f.grantScope(t, "group", sourceTarget)
	f.grantScope(t, "group", sourceTarget+1)
	one, err := f.repo.Reserve(ctx, domain.ReserveInput{ClaimToken: f.claim, JobID: f.jobID, BotID: f.botID, ConversationID: f.conversationID, Text: "one", TargetKind: "group", TargetID: sourceTarget + 1})
	if err != nil {
		t.Fatal(err)
	}
	two, err := f.repo.Reserve(ctx, domain.ReserveInput{ClaimToken: claim, JobID: second.JobID, BotID: f.botID, ConversationID: second.ConversationID, Text: "two", TargetKind: "group", TargetID: sourceTarget})
	if err != nil {
		t.Fatal(err)
	}
	if err := f.repo.MarkSending(ctx, one.ID, f.claim); err != nil {
		t.Fatal(err)
	}
	if err := f.repo.MarkSending(ctx, two.ID, claim); err != nil {
		t.Fatal(err)
	}
	start := make(chan struct{})
	results := make(chan error, 2)
	for _, record := range []domain.Record{one, two} {
		go func(record domain.Record) {
			<-start
			results <- f.repo.Settle(ctx, record.ID, application.Outcome{Status: domain.StatusSent, PlatformMessageID: -1}, record.ClaimToken)
		}(record)
	}
	close(start)
	for i := 0; i < 2; i++ {
		if err := <-results; err != nil {
			t.Fatal(err)
		}
	}
	var count int
	if err := f.pool.QueryRow(ctx, "SELECT count(*) FROM messages WHERE outbound_id IN($1,$2)", one.ID, two.ID).Scan(&count); err != nil || count != 2 {
		t.Fatalf("history: %d %v", count, err)
	}
}

func TestPublicSendRechecksAuthorizationBeforePlatform(t *testing.T) {
	f := newLedgerFixture(t)
	coordinator, _ := application.NewCoordinator(f.repo)
	scope := pa.Scope{ClaimToken: f.claim, JobID: f.jobID, BotID: f.botID, ConversationID: f.conversationID, Kind: "group", TargetID: 1, Permissions: map[string]bool{"qq.api.send_private_msg": true, "qq.cross_conversation": true}}
	ctx := pa.WithToolCallID(pa.WithScope(context.Background(), scope), "revoked-call")
	checks, calls := 0, 0
	opts := rt.Options{Ledger: coordinator, Authorize: func(context.Context, pa.Scope) (domain.Limits, error) {
		checks++
		if checks == 2 {
			return domain.Limits{}, pa.Denied("权限已撤销")
		}
		return domain.Limits{MaxMessages: 3}, nil
	}}
	text := "hello"
	_, err := rt.SendPublic(ctx, opts, ob.PublicCommand{Name: ob.ActionSendPrivate, UserID: 22, Text: &text}, func(context.Context, ob.PublicCommand) (ob.PublicResult, error) {
		calls++
		return ob.PublicResult{MessageID: 1}, nil
	})
	if err == nil || checks != 2 || calls != 0 {
		t.Fatalf("checks=%d calls=%d err=%v", checks, calls, err)
	}
	records, err := f.repo.ListByJob(ctx, f.jobID)
	if err != nil || len(records) != 1 || records[0].Status != domain.StatusCancelled {
		t.Fatalf("records=%+v err=%v", records, err)
	}
	var outcome, reason string
	if err := f.pool.QueryRow(ctx, "SELECT COALESCE(delivery_outcome,''),COALESCE(delivery_reason,'') FROM message_jobs WHERE id=$1", f.jobID).Scan(&outcome, &reason); err != nil {
		t.Fatal(err)
	}
	// A refused public send is attributed without blocking the job, so the
	// source conversation can still receive its own reply.
	// 公开发送被拒只记录原因而不阻断任务，来源会话仍可收到自己的回复。
	if outcome != "" || reason != "authorization_revoked" {
		t.Fatalf("refusal not recorded: outcome=%q reason=%q", outcome, reason)
	}
}

// TestSettlementSkipsHistoryForUngovernedTarget keeps outbound text out of a
// target conversation that never enabled history.
// TestSettlementSkipsHistoryForUngovernedTarget 不向未开启留存的目标会话写入出站正文。
func TestSettlementSkipsHistoryForUngovernedTarget(t *testing.T) {
	f := newLedgerFixture(t)
	ctx := context.Background()
	record, err := f.repo.Reserve(ctx, domain.ReserveInput{ClaimToken: f.claim, JobID: f.jobID, BotID: f.botID, ConversationID: f.conversationID, Text: "hi", TargetKind: "group", TargetID: 424242, Limits: domain.Limits{MaxMessages: 1, MaxCharsPerMessage: 100, MaxTotalChars: 100}})
	if err != nil {
		t.Fatal(err)
	}
	if err := f.repo.MarkSending(ctx, record.ID, f.claim); err != nil {
		t.Fatal(err)
	}
	if err := f.repo.Settle(ctx, record.ID, application.Outcome{Status: domain.StatusSent, PlatformMessageID: 7}, f.claim); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := f.pool.QueryRow(ctx, "SELECT count(*) FROM messages WHERE outbound_id=$1", record.ID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("history written for ungoverned target: %d", count)
	}
}
