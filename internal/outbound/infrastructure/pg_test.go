// Integration tests for the PostgreSQL delivery ledger, gated by NAGI_TEST_DSN.
// PostgreSQL 投递台账的集成测试，以 NAGI_TEST_DSN 为门控。
package infrastructure

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/HappyLadySauceM/Nagi/pkg/db"
	"github.com/google/uuid"

	conversationdomain "github.com/HappyLadySauceM/Nagi/internal/conversation/domain"
	conversationinfra "github.com/HappyLadySauceM/Nagi/internal/conversation/infrastructure"
	"github.com/HappyLadySauceM/Nagi/internal/outbound/application"
	"github.com/HappyLadySauceM/Nagi/internal/outbound/domain"
	"github.com/HappyLadySauceM/Nagi/pkg/db/dbtest"
)

// ledgerFixture is one ingested job with its delivery ledger.
// ledgerFixture 是一个已完成入库的任务及其投递台账。
type ledgerFixture struct {
	repo           *Repo
	pool           *db.Pool
	claim          string
	jobID          int64
	conversationID string
	botID          int64
}

// newLedgerFixture ingests a unique message so the job and conversation exist.
// newLedgerFixture 入库一条唯一消息，使任务与会话存在。
func newLedgerFixture(t *testing.T) ledgerFixture {
	t.Helper()
	pool := dbtest.Open(t)
	ctx := context.Background()
	base := time.Now().UnixNano()
	botID := 993000 + base%1000
	convRepo, err := conversationinfra.NewRepo(pool)
	if err != nil {
		t.Fatalf("new conversation repo: %v", err)
	}
	res, err := convRepo.Ingest(ctx, conversationdomain.IngestInput{
		BotID: botID, Kind: conversationdomain.KindGroup, TargetID: base % 1_000_000_000,
		PlatformMessageID: base, SenderID: botID + 1, Text: "台账测试",
	})
	if err != nil {
		t.Fatalf("ingest: %v", err)
	}
	repo, err := NewRepo(pool)
	if err != nil {
		t.Fatalf("new ledger repo: %v", err)
	}
	// The test database is shared; a leftover pending job would shift the ready
	// set other tests fetch. Deleting the job also cascades its ledger rows.
	// 测试库是共享的；遗留的待处理任务会改变其他测试获取的就绪集合。
	// 删除任务会一并级联删除台账记录。
	t.Cleanup(func() {
		if _, err := pool.Exec(context.Background(), `DELETE FROM message_jobs WHERE id = $1`, res.JobID); err != nil {
			t.Logf("cleanup job %d: %v", res.JobID, err)
		}
	})
	claim := dbtest.Claim(t, pool, res.JobID)
	return ledgerFixture{claim: claim, repo: repo, pool: pool, jobID: res.JobID, conversationID: res.ConversationID, botID: botID}
}

func (f ledgerFixture) reserve(t *testing.T, text string, limits domain.Limits) domain.Record {
	t.Helper()
	record, err := f.repo.Reserve(context.Background(), domain.ReserveInput{ClaimToken: f.claim,
		JobID: f.jobID, BotID: f.botID, ConversationID: f.conversationID,
		Text: text, Limits: limits,
	})
	if err != nil {
		t.Fatalf("reserve: %v", err)
	}
	return record
}

func budgetErrorDimension(t *testing.T, err error) domain.Dimension {
	t.Helper()
	var budget *domain.BudgetError
	if !errors.As(err, &budget) {
		t.Fatalf("error = %v, want *BudgetError", err)
	}
	return budget.Dimension
}

// TestReserveAllocatesSequenceAndBudget verifies sequence numbering from one and
// the accumulated run budget.
// TestReserveAllocatesSequenceAndBudget 验证序号从 1 递增以及回合预算累加。
func TestReserveAllocatesSequenceAndBudget(t *testing.T) {
	f := newLedgerFixture(t)
	limits := domain.Limits{MaxMessages: 3, MaxCharsPerMessage: 10, MaxTotalChars: 30}

	first := f.reserve(t, "一二三", limits)
	second := f.reserve(t, "四五六七", limits)
	if first.Sequence != 1 || second.Sequence != 2 {
		t.Fatalf("sequences = %d/%d, want 1/2", first.Sequence, second.Sequence)
	}
	if first.Status != domain.StatusPrepared || first.ID == "" {
		t.Fatalf("first record = %+v", first)
	}

	var messages, chars int
	if err := f.pool.QueryRow(context.Background(),
		`SELECT reserved_messages, reserved_chars FROM agent_run_budgets WHERE job_id = $1`, f.jobID,
	).Scan(&messages, &chars); err != nil {
		t.Fatalf("read budget: %v", err)
	}
	if messages != 2 || chars != 7 {
		t.Fatalf("budget = %d messages / %d chars, want 2/7", messages, chars)
	}
}

// TestReserveRejectsWithoutInserting verifies each budget dimension rejects the
// reservation and leaves both the ledger and the budget untouched.
// TestReserveRejectsWithoutInserting 验证各预算维度拒绝预留，
// 且台账与预算都不发生变化。
func TestReserveRejectsWithoutInserting(t *testing.T) {
	f := newLedgerFixture(t)
	ctx := context.Background()

	// Single message longer than the per-message limit.
	_, err := f.repo.Reserve(ctx, domain.ReserveInput{ClaimToken: f.claim,
		JobID: f.jobID, BotID: f.botID, ConversationID: f.conversationID,
		Text: "01234567890", Limits: domain.Limits{MaxMessages: 3, MaxCharsPerMessage: 10, MaxTotalChars: 30},
	})
	if !errors.Is(err, domain.ErrBudgetExceeded) || budgetErrorDimension(t, err) != domain.DimensionMessageChars {
		t.Fatalf("long message error = %v", err)
	}

	limits := domain.Limits{MaxMessages: 1, MaxCharsPerMessage: 10, MaxTotalChars: 15}
	f.reserve(t, "0123456789", limits)
	_, err = f.repo.Reserve(ctx, domain.ReserveInput{ClaimToken: f.claim,
		JobID: f.jobID, BotID: f.botID, ConversationID: f.conversationID, Text: "ab", Limits: limits,
	})
	if !errors.Is(err, domain.ErrBudgetExceeded) || budgetErrorDimension(t, err) != domain.DimensionMessages {
		t.Fatalf("message count error = %v", err)
	}

	totalLimits := domain.Limits{MaxMessages: 3, MaxCharsPerMessage: 10, MaxTotalChars: 15}
	_, err = f.repo.Reserve(ctx, domain.ReserveInput{ClaimToken: f.claim,
		JobID: f.jobID, BotID: f.botID, ConversationID: f.conversationID, Text: "0123456789", Limits: totalLimits,
	})
	if !errors.Is(err, domain.ErrBudgetExceeded) || budgetErrorDimension(t, err) != domain.DimensionTotalChars {
		t.Fatalf("total chars error = %v", err)
	}

	var records, messages, chars int
	if err := f.pool.QueryRow(ctx, `SELECT count(*) FROM agent_outbound_messages WHERE job_id = $1`, f.jobID).Scan(&records); err != nil {
		t.Fatalf("count records: %v", err)
	}
	if err := f.pool.QueryRow(ctx,
		`SELECT reserved_messages, reserved_chars FROM agent_run_budgets WHERE job_id = $1`, f.jobID,
	).Scan(&messages, &chars); err != nil {
		t.Fatalf("read budget: %v", err)
	}
	if records != 1 || messages != 1 || chars != 10 {
		t.Fatalf("rejected reservations must not persist: records=%d budget=%d/%d", records, messages, chars)
	}
}

// TestReserveConcurrentSameJobIsBounded verifies that concurrent reservations of
// one job never reuse a sequence or exceed the message budget.
// TestReserveConcurrentSameJobIsBounded 验证同一任务的并发预留不会复用序号
// 或突破条数预算。
func TestReserveConcurrentSameJobIsBounded(t *testing.T) {
	f := newLedgerFixture(t)
	ctx := context.Background()
	limits := domain.Limits{MaxMessages: 5, MaxCharsPerMessage: 10, MaxTotalChars: 100}

	const attempts = 8
	var (
		mu        sync.Mutex
		sequences []int
		denied    int
		issues    []string
	)
	var wg sync.WaitGroup
	for i := 0; i < attempts; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			record, err := f.repo.Reserve(ctx, domain.ReserveInput{ClaimToken: f.claim,
				JobID: f.jobID, BotID: f.botID, ConversationID: f.conversationID,
				Text: fmt.Sprintf("并%02d", i), Limits: limits,
			})
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				if !errors.Is(err, domain.ErrBudgetExceeded) {
					issues = append(issues, err.Error())
					return
				}
				denied++
				return
			}
			sequences = append(sequences, record.Sequence)
		}(i)
	}
	wg.Wait()

	mu.Lock()
	defer mu.Unlock()
	if len(issues) != 0 {
		t.Fatalf("reserve issues: %v", issues)
	}
	if len(sequences) != limits.MaxMessages || denied != attempts-limits.MaxMessages {
		t.Fatalf("reserved=%d denied=%d, want %d/%d", len(sequences), denied, limits.MaxMessages, attempts-limits.MaxMessages)
	}
	seen := make(map[int]bool, len(sequences))
	for _, sequence := range sequences {
		if sequence < 1 || sequence > limits.MaxMessages || seen[sequence] {
			t.Fatalf("sequence %d is duplicated or out of range: %v", sequence, sequences)
		}
		seen[sequence] = true
	}
}

// TestMarkSendingAndSettleStateMachine verifies the prepared→sending→terminal
// transitions and the errors for illegal moves.
// TestMarkSendingAndSettleStateMachine 验证 prepared→sending→终态的迁移
// 以及非法迁移的错误。
func TestMarkSendingAndSettleStateMachine(t *testing.T) {
	f := newLedgerFixture(t)
	ctx := context.Background()
	record := f.reserve(t, "状态机", domain.Limits{MaxMessages: 3, MaxCharsPerMessage: 50, MaxTotalChars: 150})

	// Nothing may settle before it is sending.
	err := f.repo.Settle(ctx, record.ID, application.Outcome{Status: domain.StatusSent}, f.claim)
	var transition *domain.TransitionError
	if !errors.As(err, &transition) || transition.From != domain.StatusPrepared {
		t.Fatalf("settle before sending = %v", err)
	}
	if err := f.repo.MarkSending(ctx, record.ID, f.claim); err != nil {
		t.Fatalf("mark sending: %v", err)
	}
	// A second mark would allow a second platform call for one record.
	if err := f.repo.MarkSending(ctx, record.ID, f.claim); !errors.As(err, &transition) || transition.From != domain.StatusSending {
		t.Fatalf("second mark sending = %v", err)
	}
	if err := f.repo.Settle(ctx, record.ID, application.Outcome{
		Status: domain.StatusSent, PlatformMessageID: 654321,
	}, f.claim); err != nil {
		t.Fatalf("settle: %v", err)
	}
	if err := f.repo.Settle(ctx, record.ID, application.Outcome{Status: domain.StatusUnknown}, f.claim); !errors.As(err, &transition) ||
		transition.From != domain.StatusSent {
		t.Fatalf("settle after sent = %v", err)
	}
	if err := f.repo.MarkSending(ctx, uuid.NewString(), f.claim); !errors.Is(err, domain.ErrRecordNotFound) {
		t.Fatalf("mark missing record = %v", err)
	}
	if err := f.repo.Settle(ctx, record.ID, application.Outcome{Status: domain.StatusPrepared}, f.claim); !errors.Is(err, domain.ErrInvalidSettlement) {
		t.Fatalf("settle to prepared = %v", err)
	}

	var status string
	var platformID int64
	if err := f.pool.QueryRow(ctx,
		`SELECT status, COALESCE(platform_message_id, 0) FROM agent_outbound_messages WHERE id = $1`, record.ID,
	).Scan(&status, &platformID); err != nil {
		t.Fatalf("read record: %v", err)
	}
	if status != "sent" || platformID != 654321 {
		t.Fatalf("record = %s/%d, want sent/654321", status, platformID)
	}
}

// TestHasDelivered verifies only sent and unknown records count as delivered.
// TestHasDelivered 验证只有 sent 与 unknown 记录算已投递。
func TestHasDelivered(t *testing.T) {
	f := newLedgerFixture(t)
	ctx := context.Background()
	limits := domain.Limits{MaxMessages: 3, MaxCharsPerMessage: 50, MaxTotalChars: 150}

	record := f.reserve(t, "投递判断", limits)
	if delivered, err := f.repo.HasDelivered(ctx, f.jobID); err != nil || delivered {
		t.Fatalf("prepared record delivered=%v err=%v", delivered, err)
	}
	if err := f.repo.MarkSending(ctx, record.ID, f.claim); err != nil {
		t.Fatalf("mark sending: %v", err)
	}
	if delivered, err := f.repo.HasDelivered(ctx, f.jobID); err != nil || delivered {
		t.Fatalf("sending record delivered=%v err=%v", delivered, err)
	}
	if err := f.repo.Settle(ctx, record.ID, application.Outcome{Status: domain.StatusFailed, ErrorCode: "send_failed"}, f.claim); err != nil {
		t.Fatalf("settle failed: %v", err)
	}
	if delivered, err := f.repo.HasDelivered(ctx, f.jobID); err != nil || delivered {
		t.Fatalf("failed record delivered=%v err=%v", delivered, err)
	}
	unknown := f.reserve(t, "结果不明", limits)
	if err := f.repo.MarkSending(ctx, unknown.ID, f.claim); err != nil {
		t.Fatalf("mark unknown sending: %v", err)
	}
	if err := f.repo.Settle(ctx, unknown.ID, application.Outcome{Status: domain.StatusUnknown, ErrorCode: "timeout"}, f.claim); err != nil {
		t.Fatalf("settle unknown: %v", err)
	}
	if delivered, err := f.repo.HasDelivered(ctx, f.jobID); err != nil || !delivered {
		t.Fatalf("unknown record delivered=%v err=%v", delivered, err)
	}
}

// TestRecoverStaleMarksSendingUnknown verifies restart recovery never leaves a
// record in sending and never touches prepared records.
// TestRecoverStaleMarksSendingUnknown 验证重启恢复不会留下 sending 记录，
// 也不会改动 prepared 记录。
func TestRecoverStaleMarksSendingUnknown(t *testing.T) {
	f := newLedgerFixture(t)
	ctx := context.Background()
	limits := domain.Limits{MaxMessages: 3, MaxCharsPerMessage: 50, MaxTotalChars: 150}

	sending := f.reserve(t, "发送中", limits)
	if err := f.repo.MarkSending(ctx, sending.ID, f.claim); err != nil {
		t.Fatalf("mark sending: %v", err)
	}
	prepared := f.reserve(t, "未发送", limits)

	if _, err := f.pool.Exec(ctx, "UPDATE message_jobs SET lease_expires_at=clock_timestamp()-interval '1 second' WHERE id=$1", f.jobID); err != nil {
		t.Fatal(err)
	}
	convRepo, err := conversationinfra.NewRepo(f.pool)
	if err != nil {
		t.Fatal(err)
	}
	err = convRepo.RecoverStaleJobs(ctx)
	var recovered int
	if scanErr := f.pool.QueryRow(ctx, "SELECT count(*) FROM agent_outbound_messages WHERE job_id=$1 AND status='unknown'", f.jobID).Scan(&recovered); scanErr != nil {
		t.Fatal(scanErr)
	}
	if err != nil {
		t.Fatalf("recover stale: %v", err)
	}
	if recovered != 1 {
		t.Fatalf("recovered = %d, want 1", recovered)
	}

	var status, code string
	if err := f.pool.QueryRow(ctx,
		`SELECT status, COALESCE(error_code, '') FROM agent_outbound_messages WHERE id = $1`, sending.ID,
	).Scan(&status, &code); err != nil {
		t.Fatalf("read sending record: %v", err)
	}
	if status != "unknown" || code != "lease_expired" {
		t.Fatalf("stale record = %s/%s, want unknown/recovered", status, code)
	}
	if err := f.pool.QueryRow(ctx,
		`SELECT status FROM agent_outbound_messages WHERE id = $1`, prepared.ID,
	).Scan(&status); err != nil {
		t.Fatalf("read prepared record: %v", err)
	}
	if status != "cancelled" {
		t.Fatalf("prepared record = %s, want prepared", status)
	}
}

// TestReserveRequiresExistingJob verifies a missing job is reported instead of
// failing on the foreign key.
// TestReserveRequiresExistingJob 验证任务不存在时返回明确错误，
// 而不是外键错误。
func TestReserveRequiresExistingJob(t *testing.T) {
	f := newLedgerFixture(t)
	_, err := f.repo.Reserve(context.Background(), domain.ReserveInput{ClaimToken: f.claim,
		JobID: 999999999999, BotID: f.botID, ConversationID: f.conversationID, Text: "无任务",
		Limits: domain.Limits{MaxMessages: 3, MaxCharsPerMessage: 50, MaxTotalChars: 150},
	})
	if !errors.Is(err, domain.ErrRecordNotFound) {
		t.Fatalf("reserve without job = %v", err)
	}
}

func TestStableCallDeduplicatesAndCancelledReservationRefunds(t *testing.T) {
	f := newLedgerFixture(t)
	input := domain.ReserveInput{ClaimToken: f.claim, JobID: f.jobID, BotID: f.botID, ConversationID: f.conversationID, ToolCallID: "call-one", Text: "one", Limits: domain.Limits{MaxMessages: 1, MaxCharsPerMessage: 10, MaxTotalChars: 10}}
	first, err := f.repo.Reserve(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	again, err := f.repo.Reserve(context.Background(), input)
	if err != nil || again.ID != first.ID {
		t.Fatalf("duplicate reservation: %+v %v", again, err)
	}
	input.Text = "different"
	if _, err = f.repo.Reserve(context.Background(), input); err == nil {
		t.Fatal("same call with different payload accepted")
	}
	if err = f.repo.Cancel(context.Background(), first.ID, f.claim); err != nil {
		t.Fatal(err)
	}
	input.ToolCallID = "call-two"
	input.Text = "two"
	if _, err = f.repo.Reserve(context.Background(), input); err != nil {
		t.Fatalf("cancelled budget not released: %v", err)
	}
}

func TestHourlyLimitIncludesLegacyMessages(t *testing.T) {
	f := newLedgerFixture(t)
	_, err := f.pool.Exec(context.Background(), `INSERT INTO messages(conversation_id,bot_id,direction,sender_id,text_content,sent_status) VALUES($1,$2,'outgoing',$2,'legacy','sent')`, f.conversationID, f.botID)
	if err != nil {
		t.Fatal(err)
	}
	_, err = f.repo.Reserve(context.Background(), domain.ReserveInput{ClaimToken: f.claim, JobID: f.jobID, BotID: f.botID, ConversationID: f.conversationID, Text: "one", Limits: domain.Limits{MaxMessages: 3, MaxCharsPerMessage: 100, MaxTotalChars: 300, MaxPerHour: 1}})
	if budgetErrorDimension(t, err) != "hourly_messages" {
		t.Fatal("legacy message did not consume hourly quota")
	}
}

func TestRunStopsAfterFailedPlatformAttempt(t *testing.T) {
	f := newLedgerFixture(t)
	first := f.reserve(t, "one", domain.Limits{MaxMessages: 3, MaxCharsPerMessage: 100, MaxTotalChars: 300})
	if err := f.repo.MarkSending(context.Background(), first.ID, f.claim); err != nil {
		t.Fatal(err)
	}
	if err := f.repo.Settle(context.Background(), first.ID, application.Outcome{Status: domain.StatusFailed}, f.claim); err != nil {
		t.Fatal(err)
	}
	_, err := f.repo.Reserve(context.Background(), domain.ReserveInput{ClaimToken: f.claim, JobID: f.jobID, BotID: f.botID, ConversationID: f.conversationID, Text: "two", Limits: domain.Limits{MaxMessages: 3, MaxCharsPerMessage: 100, MaxTotalChars: 300, StopAfterFailure: true}})
	if err == nil {
		t.Fatal("failed delivery did not stop remaining messages")
	}
	if err = f.repo.DeliveryError(context.Background(), f.jobID); err == nil {
		t.Fatal("failed delivery reported success")
	}
}

func TestReplayedPreparedCallRetainsMessageInterval(t *testing.T) {
	f := newLedgerFixture(t)
	limits := domain.Limits{MaxMessages: 3, MaxCharsPerMessage: 100, MaxTotalChars: 300, MinIntervalMS: 700}
	first := f.reserve(t, "first", limits)
	if err := f.repo.MarkSending(context.Background(), first.ID, f.claim); err != nil {
		t.Fatal(err)
	}
	if err := f.repo.Settle(context.Background(), first.ID, application.Outcome{Status: domain.StatusSent, PlatformMessageID: 123}, f.claim); err != nil {
		t.Fatal(err)
	}
	in := domain.ReserveInput{ClaimToken: f.claim, JobID: f.jobID, BotID: f.botID, ConversationID: f.conversationID, ToolCallID: "second", Text: "second", Limits: limits}
	second, err := f.repo.Reserve(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	replay, err := f.repo.Reserve(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	if replay.ID != second.ID || replay.NotBefore.IsZero() || !replay.NotBefore.Equal(second.NotBefore) {
		t.Fatalf("replay lost interval: original=%v replay=%v", second.NotBefore, replay.NotBefore)
	}
}

func TestLoweredLimitsRejectWaitingReservation(t *testing.T) {
	f := newLedgerFixture(t)
	first := f.reserve(t, "one", domain.Limits{MaxMessages: 3, MaxCharsPerMessage: 100, MaxTotalChars: 300})
	if err := f.repo.MarkSending(context.Background(), first.ID, f.claim); err != nil {
		t.Fatal(err)
	}
	if err := f.repo.Settle(context.Background(), first.ID, application.Outcome{Status: domain.StatusSent, PlatformMessageID: 1}, f.claim); err != nil {
		t.Fatal(err)
	}
	second := f.reserve(t, "two", domain.Limits{MaxMessages: 3, MaxCharsPerMessage: 100, MaxTotalChars: 300})
	if err := f.repo.ValidateReserved(context.Background(), second.ID, domain.Limits{MaxMessages: 1, MaxCharsPerMessage: 100, MaxTotalChars: 300}); err == nil {
		t.Fatal("lowered budget ignored")
	}
}

func TestLoweredHourlyQuotaRejectsWaitingReservation(t *testing.T) {
	f := newLedgerFixture(t)
	limits := domain.Limits{MaxMessages: 3, MaxCharsPerMessage: 100, MaxTotalChars: 300, MaxPerHour: 20}
	first := f.reserve(t, "one", limits)
	if err := f.repo.MarkSending(context.Background(), first.ID, f.claim); err != nil {
		t.Fatal(err)
	}
	if err := f.repo.Settle(context.Background(), first.ID, application.Outcome{Status: domain.StatusSent, PlatformMessageID: 1}, f.claim); err != nil {
		t.Fatal(err)
	}
	second := f.reserve(t, "two", limits)
	limits.MaxPerHour = 1
	if err := f.repo.ValidateReserved(context.Background(), second.ID, limits); err == nil {
		t.Fatal("lowered hourly quota ignored")
	}
}

func TestPreparedCrashRecoveryCanDeliverOneMessage(t *testing.T) {
	for _, sameCall := range []bool{true, false} {
		t.Run(fmt.Sprintf("same-call-%t", sameCall), func(t *testing.T) {
			f := newLedgerFixture(t)
			ctx := context.Background()
			limits := domain.Limits{MaxMessages: 1, MaxCharsPerMessage: 100, MaxTotalChars: 100, StopAfterFailure: true}
			request := domain.ReserveInput{ClaimToken: f.claim, JobID: f.jobID, BotID: f.botID, ConversationID: f.conversationID, RunID: "recovery-run", ToolCallID: "final", Text: "recover exactly once", Limits: limits}
			first, err := f.repo.Reserve(ctx, request)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := f.pool.Exec(ctx, "UPDATE message_jobs SET lease_expires_at=clock_timestamp()-interval '1 second' WHERE id=$1", f.jobID); err != nil {
				t.Fatal(err)
			}
			conversations, err := conversationinfra.NewRepo(f.pool)
			if err != nil {
				t.Fatal(err)
			}
			if err := conversations.RecoverStaleJobs(ctx); err != nil {
				t.Fatal(err)
			}
			var state string
			if err := f.pool.QueryRow(ctx, "SELECT status FROM message_jobs WHERE id=$1", f.jobID).Scan(&state); err != nil || state != "pending" {
				t.Fatalf("recovery state=%s err=%v", state, err)
			}
			if !sameCall {
				request.ToolCallID = "new-call"
			}
			request.ClaimToken = dbtest.Claim(t, f.pool, f.jobID)
			reserved, err := f.repo.Reserve(ctx, request)
			if err != nil {
				t.Fatal(err)
			}
			if reserved.Status != domain.StatusPrepared || (sameCall && reserved.ID != first.ID) {
				t.Fatalf("unsafe recovery reservation: %+v", reserved)
			}
			if err := f.repo.ValidateReserved(ctx, reserved.ID, limits); err != nil {
				t.Fatalf("refunded reservation still consumes budget: %v", err)
			}
			calls := 0
			outcome, err := application.Deliver(ctx, f.repo, reserved.ID, func(context.Context) (int64, error) { calls++; return 556677, nil }, request.ClaimToken)
			if err != nil || outcome.Status != domain.StatusSent || calls != 1 {
				t.Fatalf("recovery delivery: %+v %v calls=%d", outcome, err, calls)
			}
			duplicate, err := f.repo.Reserve(ctx, request)
			if err != nil || duplicate.Status != domain.StatusSent || duplicate.PlatformMessageID != 556677 {
				t.Fatalf("duplicate receipt lost: %+v %v", duplicate, err)
			}
			if _, err := f.pool.Exec(ctx, "UPDATE message_jobs SET lease_expires_at=clock_timestamp()-interval '1 second' WHERE id=$1", f.jobID); err != nil {
				t.Fatal(err)
			}
			if err := conversations.RecoverStaleJobs(ctx); err != nil {
				t.Fatal(err)
			}
			if err := f.pool.QueryRow(ctx, "SELECT status FROM message_jobs WHERE id=$1", f.jobID).Scan(&state); err != nil || state != "failed" {
				t.Fatalf("attempted delivery was requeued: %s %v", state, err)
			}
		})
	}
}
