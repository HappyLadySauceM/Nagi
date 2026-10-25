// Integration test for the conversation repository, gated by QQBOT_TEST_DSN.
// 会话仓库的集成测试，以 QQBOT_TEST_DSN 为门控。
package infrastructure

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/HappyLadySauceM/QQBot/internal/conversation/domain"
	obdomain "github.com/HappyLadySauceM/QQBot/internal/onebot/domain"
	"github.com/HappyLadySauceM/QQBot/pkg/db/dbtest"
)

// TestConversationIngestDedupAndJobCycle verifies conversation auto-creation,
// platform-event dedup, job creation and the complete-job lifecycle.
// TestConversationIngestDedupAndJobCycle 验证会话自动创建、平台事件去重、
// 任务创建与完成任务生命周期。
func TestConversationIngestDedupAndJobCycle(t *testing.T) {
	pool := dbtest.Open(t)
	repo, err := NewRepo(pool)
	if err != nil {
		t.Fatalf("new repo: %v", err)
	}
	ctx := context.Background()
	// Unique per run: the local test database persists data between runs.
	// 每次运行唯一：本地测试数据库在多次运行间持久化数据。
	base := time.Now().UnixNano()
	const botID, senderID = 991001, 991004
	groupID := base % 1_000_000_000
	platformMessageID := base
	input := domain.IngestInput{
		BotID: botID, Kind: domain.KindGroup, TargetID: groupID,
		PlatformMessageID: platformMessageID, SenderID: senderID,
		Text: "集成测试消息",
		Segments: []obdomain.Segment{
			obdomain.Text("集成测试消息"),
			obdomain.At(991005),
		},
		TraceParent: "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01",
		TraceState:  "vendor=value",
	}

	first, err := repo.Ingest(ctx, input)
	if err != nil {
		t.Fatalf("ingest: %v", err)
	}
	if first.Duplicate || first.MessageID == 0 || first.JobID == 0 || first.ConversationID == "" {
		t.Fatalf("first ingest result = %+v", first)
	}

	second, err := repo.Ingest(ctx, input)
	if err != nil {
		t.Fatalf("duplicate ingest: %v", err)
	}
	if !second.Duplicate || second.MessageID != 0 || second.JobID != 0 {
		t.Fatalf("duplicate ingest result = %+v", second)
	}

	jobs, err := repo.ClaimReadyJobs(ctx, 10)
	if err != nil {
		t.Fatalf("claim ready jobs: %v", err)
	}
	var found *domain.Job
	for i := range jobs {
		if jobs[i].ID == first.JobID {
			found = &jobs[i]
		}
	}
	if found == nil {
		t.Fatalf("job %d not found in ready jobs (%d jobs)", first.JobID, len(jobs))
	}
	if found.Kind != domain.KindGroup || found.TargetID != groupID || found.BotID != botID {
		t.Fatalf("job context = %+v", found)
	}
	// The trace context must survive the ingest transaction byte for byte so
	// the worker can restore the exact parent.
	// 追踪上下文必须在入库事务中逐字节保留，供工作器还原精确的父级。
	if found.TraceParent != input.TraceParent || found.TraceState != input.TraceState {
		t.Fatalf("job trace context = %q / %q, want %q / %q",
			found.TraceParent, found.TraceState, input.TraceParent, input.TraceState)
	}
	if len(found.Message.Segments) != 2 || found.Message.Segments[0].Type != obdomain.SegText {
		t.Fatalf("job segments = %+v", found.Message.Segments)
	}

	msgs, err := repo.RecentMessages(ctx, first.ConversationID, 10)
	if err != nil || len(msgs) != 1 {
		t.Fatalf("recent messages: len=%d err=%v", len(msgs), err)
	}
	if msgs[0].Text != "集成测试消息" || msgs[0].Direction != domain.Incoming {
		t.Fatalf("message = %+v", msgs[0])
	}

	conv, ok, err := repo.GetConversation(ctx, botID, domain.KindGroup, groupID)
	if err != nil || !ok || conv.ID != first.ConversationID {
		t.Fatalf("get conversation: ok=%v conv=%+v err=%v", ok, conv, err)
	}

	if err := repo.CompleteJob(ctx, first.JobID); err != nil {
		t.Fatalf("complete job: %v", err)
	}
	jobs, err = repo.ClaimReadyJobs(ctx, 10)
	if err != nil {
		t.Fatalf("claim ready jobs after complete: %v", err)
	}
	for _, j := range jobs {
		if j.ID == first.JobID {
			t.Fatal("completed job must not be claimed again")
		}
	}
}

// TestClaimReadyJobsExclusiveAndRecovery verifies that a claimed job is not
// handed out twice, and that RecoverStaleJobs makes it claimable again.
// TestClaimReadyJobsExclusiveAndRecovery 验证任务不会被重复认领，
// 且 RecoverStaleJobs 能让遗留任务重新可认领。
func TestClaimReadyJobsExclusiveAndRecovery(t *testing.T) {
	pool := dbtest.Open(t)
	repo, err := NewRepo(pool)
	if err != nil {
		t.Fatalf("new repo: %v", err)
	}
	ctx := context.Background()
	base := time.Now().UnixNano()
	const botID, senderID = 992001, 992004
	ingest, err := repo.Ingest(ctx, domain.IngestInput{
		BotID: botID, Kind: domain.KindGroup, TargetID: base % 1_000_000_000,
		PlatformMessageID: base, SenderID: senderID, Text: "认领测试",
	})
	if err != nil {
		t.Fatalf("ingest: %v", err)
	}

	contains := func(jobs []domain.Job, id int64) bool {
		for _, j := range jobs {
			if j.ID == id {
				return true
			}
		}
		return false
	}

	first, err := repo.ClaimReadyJobs(ctx, 50)
	if err != nil {
		t.Fatalf("first claim: %v", err)
	}
	if !contains(first, ingest.JobID) {
		t.Fatalf("job %d not claimed on first pass", ingest.JobID)
	}

	second, err := repo.ClaimReadyJobs(ctx, 50)
	if err != nil {
		t.Fatalf("second claim: %v", err)
	}
	if contains(second, ingest.JobID) {
		t.Fatalf("processing job %d must not be claimed again", ingest.JobID)
	}

	if err := repo.RecoverStaleJobs(ctx); err != nil {
		t.Fatalf("recover stale jobs: %v", err)
	}
	third, err := repo.ClaimReadyJobs(ctx, 50)
	if err != nil {
		t.Fatalf("claim after recovery: %v", err)
	}
	if !contains(third, ingest.JobID) {
		t.Fatalf("recovered job %d must be claimable again", ingest.JobID)
	}

	if err := repo.CompleteJob(ctx, ingest.JobID); err != nil {
		t.Fatalf("complete job: %v", err)
	}
}

// TestClaimReadyJobsConcurrentExclusive verifies that concurrent claimers never
// receive the same job.
// TestClaimReadyJobsConcurrentExclusive 验证并发认领者不会拿到同一个任务。
func TestClaimReadyJobsConcurrentExclusive(t *testing.T) {
	pool := dbtest.Open(t)
	repo, err := NewRepo(pool)
	if err != nil {
		t.Fatalf("new repo: %v", err)
	}
	ctx := context.Background()
	base := time.Now().UnixNano()
	const botID, senderID, total = 992002, 992005, 8
	want := make(map[int64]bool, total)
	for i := 0; i < total; i++ {
		res, err := repo.Ingest(ctx, domain.IngestInput{
			BotID: botID, Kind: domain.KindGroup, TargetID: base%1_000_000_000 + int64(i),
			PlatformMessageID: base + int64(i), SenderID: senderID, Text: "并发认领",
		})
		if err != nil {
			t.Fatalf("ingest %d: %v", i, err)
		}
		want[res.JobID] = false
	}

	var (
		mu      sync.Mutex
		claimed = make(map[int64]int, total)
		issue   []string
	)
	var wg sync.WaitGroup
	for w := 0; w < 4; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				jobs, err := repo.ClaimReadyJobs(ctx, 2)
				if err != nil {
					mu.Lock()
					issue = append(issue, err.Error())
					mu.Unlock()
					return
				}
				if len(jobs) == 0 {
					return
				}
				for _, j := range jobs {
					mu.Lock()
					claimed[j.ID]++
					if claimed[j.ID] > 1 {
						issue = append(issue, fmt.Sprintf("job %d claimed twice", j.ID))
					}
					mu.Unlock()
					if err := repo.CompleteJob(ctx, j.ID); err != nil {
						mu.Lock()
						issue = append(issue, err.Error())
						mu.Unlock()
					}
				}
			}
		}()
	}
	wg.Wait()

	mu.Lock()
	defer mu.Unlock()
	if len(issue) != 0 {
		t.Fatalf("claim issues: %v", issue)
	}
	for id := range want {
		if claimed[id] != 1 {
			t.Fatalf("job %d claimed %d times, want 1", id, claimed[id])
		}
	}
}

// TestClaimReadyJobsIsolatesPoisonSegments verifies that a message with corrupt
// segment JSON fails alone without blocking the rest of the batch.
// TestClaimReadyJobsIsolatesPoisonSegments 验证损坏的 segments 只让该任务失败，
// 不阻塞整批认领。
func TestClaimReadyJobsIsolatesPoisonSegments(t *testing.T) {
	pool := dbtest.Open(t)
	repo, err := NewRepo(pool)
	if err != nil {
		t.Fatalf("new repo: %v", err)
	}
	ctx := context.Background()
	base := time.Now().UnixNano()
	res, err := repo.Ingest(ctx, domain.IngestInput{
		BotID: 992003, Kind: domain.KindPrivate, TargetID: base % 1_000_000_000,
		PlatformMessageID: base, SenderID: 992006, Text: "毒丸测试",
	})
	if err != nil {
		t.Fatalf("ingest: %v", err)
	}
	// Valid JSON with a shape decodeSegments rejects: a number where the
	// segment type string belongs.
	// 合法 JSON 但结构不符合 decodeSegments 约定：类型字段是数字。
	if _, err := pool.Exec(ctx,
		`UPDATE messages SET segments = '[{"type": 5}]'::jsonb WHERE id = $1`, res.MessageID,
	); err != nil {
		t.Fatalf("corrupt segments: %v", err)
	}

	if _, err := repo.ClaimReadyJobs(ctx, 50); err != nil {
		t.Fatalf("claim with poison row must not fail the batch: %v", err)
	}
	var status, code string
	if err := pool.QueryRow(ctx,
		`SELECT status, COALESCE(last_error_code, '') FROM message_jobs WHERE id = $1`, res.JobID,
	).Scan(&status, &code); err != nil {
		t.Fatalf("query job: %v", err)
	}
	if status != "failed" || code != "bad_message_data" {
		t.Fatalf("poison job status=%q code=%q, want failed/bad_message_data", status, code)
	}
}

// TestRecordOutgoingStoresPlatformMessageID verifies that the receipt id and
// sent status of a bot reply are persisted for later reply-to-bot matching.
// TestRecordOutgoingStoresPlatformMessageID 验证机器人回复的回执 ID 与发送状态
// 被持久化，供后续引用判定匹配。
func TestRecordOutgoingStoresPlatformMessageID(t *testing.T) {
	pool := dbtest.Open(t)
	repo, err := NewRepo(pool)
	if err != nil {
		t.Fatalf("new repo: %v", err)
	}
	ctx := context.Background()
	base := time.Now().UnixNano()
	const botID, senderID, receiptID = 992004, 992007, 424242
	res, err := repo.Ingest(ctx, domain.IngestInput{
		BotID: botID, Kind: domain.KindGroup, TargetID: base % 1_000_000_000,
		PlatformMessageID: base, SenderID: senderID, Text: "回执测试",
	})
	if err != nil {
		t.Fatalf("ingest: %v", err)
	}
	if err := repo.RecordOutgoing(ctx, res.ConversationID, botID, "测试回复", receiptID, "sent"); err != nil {
		t.Fatalf("record outgoing: %v", err)
	}

	var id, platformID int64
	var direction, sentStatus, text string
	if err := pool.QueryRow(ctx, `
		SELECT id, COALESCE(platform_message_id, 0), direction, COALESCE(sent_status, ''), text_content
		FROM messages WHERE conversation_id = $1 AND direction = 'outgoing'
	`, res.ConversationID).Scan(&id, &platformID, &direction, &sentStatus, &text); err != nil {
		t.Fatalf("query outgoing message: %v", err)
	}
	if platformID != receiptID || sentStatus != "sent" || text != "测试回复" || direction != "outgoing" {
		t.Fatalf("outgoing message id=%d platform=%d status=%q text=%q direction=%q",
			id, platformID, sentStatus, text, direction)
	}

	// The recorded reply must surface in recent history as a bot message so
	// cooldown and reply-to-bot rules can use it.
	// 已记录回复必须出现在最近历史中且标记为机器人消息。
	msgs, err := repo.RecentMessages(ctx, res.ConversationID, 10)
	if err != nil || len(msgs) != 2 {
		t.Fatalf("recent messages: len=%d err=%v", len(msgs), err)
	}
	if msgs[1].Direction != domain.Outgoing || msgs[1].PlatformMessageID != receiptID {
		t.Fatalf("history message = %+v", msgs[1])
	}
}
