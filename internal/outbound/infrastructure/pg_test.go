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

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

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
	pool           *pgxpool.Pool
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
	return ledgerFixture{repo: repo, pool: pool, jobID: res.JobID, conversationID: res.ConversationID, botID: botID}
}

func (f ledgerFixture) reserve(t *testing.T, text string, limits domain.Limits) domain.Record {
	t.Helper()
	record, err := f.repo.Reserve(context.Background(), domain.ReserveInput{
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
	_, err := f.repo.Reserve(ctx, domain.ReserveInput{
		JobID: f.jobID, BotID: f.botID, ConversationID: f.conversationID,
		Text: "01234567890", Limits: domain.Limits{MaxMessages: 3, MaxCharsPerMessage: 10, MaxTotalChars: 30},
	})
	if !errors.Is(err, domain.ErrBudgetExceeded) || budgetErrorDimension(t, err) != domain.DimensionMessageChars {
		t.Fatalf("long message error = %v", err)
	}

	limits := domain.Limits{MaxMessages: 1, MaxCharsPerMessage: 10, MaxTotalChars: 15}
	f.reserve(t, "0123456789", limits)
	_, err = f.repo.Reserve(ctx, domain.ReserveInput{
		JobID: f.jobID, BotID: f.botID, ConversationID: f.conversationID, Text: "ab", Limits: limits,
	})
	if !errors.Is(err, domain.ErrBudgetExceeded) || budgetErrorDimension(t, err) != domain.DimensionMessages {
		t.Fatalf("message count error = %v", err)
	}

	totalLimits := domain.Limits{MaxMessages: 3, MaxCharsPerMessage: 10, MaxTotalChars: 15}
	_, err = f.repo.Reserve(ctx, domain.ReserveInput{
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
			record, err := f.repo.Reserve(ctx, domain.ReserveInput{
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
	err := f.repo.Settle(ctx, record.ID, application.Outcome{Status: domain.StatusSent})
	var transition *domain.TransitionError
	if !errors.As(err, &transition) || transition.From != domain.StatusPrepared {
		t.Fatalf("settle before sending = %v", err)
	}
	if err := f.repo.MarkSending(ctx, record.ID); err != nil {
		t.Fatalf("mark sending: %v", err)
	}
	// A second mark would allow a second platform call for one record.
	if err := f.repo.MarkSending(ctx, record.ID); !errors.As(err, &transition) || transition.From != domain.StatusSending {
		t.Fatalf("second mark sending = %v", err)
	}
	if err := f.repo.Settle(ctx, record.ID, application.Outcome{
		Status: domain.StatusSent, PlatformMessageID: 654321,
	}); err != nil {
		t.Fatalf("settle: %v", err)
	}
	if err := f.repo.Settle(ctx, record.ID, application.Outcome{Status: domain.StatusUnknown}); !errors.As(err, &transition) ||
		transition.From != domain.StatusSent {
		t.Fatalf("settle after sent = %v", err)
	}
	if err := f.repo.MarkSending(ctx, uuid.NewString()); !errors.Is(err, domain.ErrRecordNotFound) {
		t.Fatalf("mark missing record = %v", err)
	}
	if err := f.repo.Settle(ctx, record.ID, application.Outcome{Status: domain.StatusPrepared}); !errors.Is(err, domain.ErrInvalidSettlement) {
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
	if err := f.repo.MarkSending(ctx, record.ID); err != nil {
		t.Fatalf("mark sending: %v", err)
	}
	if delivered, err := f.repo.HasDelivered(ctx, f.jobID); err != nil || delivered {
		t.Fatalf("sending record delivered=%v err=%v", delivered, err)
	}
	if err := f.repo.Settle(ctx, record.ID, application.Outcome{Status: domain.StatusFailed, ErrorCode: "send_failed"}); err != nil {
		t.Fatalf("settle failed: %v", err)
	}
	if delivered, err := f.repo.HasDelivered(ctx, f.jobID); err != nil || delivered {
		t.Fatalf("failed record delivered=%v err=%v", delivered, err)
	}
	unknown := f.reserve(t, "结果不明", limits)
	if err := f.repo.MarkSending(ctx, unknown.ID); err != nil {
		t.Fatalf("mark unknown sending: %v", err)
	}
	if err := f.repo.Settle(ctx, unknown.ID, application.Outcome{Status: domain.StatusUnknown, ErrorCode: "timeout"}); err != nil {
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
	if err := f.repo.MarkSending(ctx, sending.ID); err != nil {
		t.Fatalf("mark sending: %v", err)
	}
	prepared := f.reserve(t, "未发送", limits)

	recovered, err := f.repo.RecoverStale(ctx)
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
	if status != "unknown" || code != "recovered" {
		t.Fatalf("stale record = %s/%s, want unknown/recovered", status, code)
	}
	if err := f.pool.QueryRow(ctx,
		`SELECT status FROM agent_outbound_messages WHERE id = $1`, prepared.ID,
	).Scan(&status); err != nil {
		t.Fatalf("read prepared record: %v", err)
	}
	if status != "prepared" {
		t.Fatalf("prepared record = %s, want prepared", status)
	}
}

// TestReserveRequiresExistingJob verifies a missing job is reported instead of
// failing on the foreign key.
// TestReserveRequiresExistingJob 验证任务不存在时返回明确错误，
// 而不是外键错误。
func TestReserveRequiresExistingJob(t *testing.T) {
	f := newLedgerFixture(t)
	_, err := f.repo.Reserve(context.Background(), domain.ReserveInput{
		JobID: 999999999999, BotID: f.botID, ConversationID: f.conversationID, Text: "无任务",
		Limits: domain.Limits{MaxMessages: 3, MaxCharsPerMessage: 50, MaxTotalChars: 150},
	})
	if !errors.Is(err, domain.ErrRecordNotFound) {
		t.Fatalf("reserve without job = %v", err)
	}
}
