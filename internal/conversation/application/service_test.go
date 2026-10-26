package application

import (
	"context"
	"log/slog"
	"testing"
	"time"

	"github.com/HappyLadySauceM/Nagi/internal/conversation/domain"
)

// stubRepo records the last ingest input; other methods are unused by these tests.
// stubRepo 记录最近一次入库输入；其余方法在本组测试中不被使用。
type stubRepo struct {
	lastIngest domain.IngestInput
	ingested   bool
}

func (s *stubRepo) Ingest(_ context.Context, in domain.IngestInput) (domain.IngestResult, error) {
	s.lastIngest = in
	s.ingested = true
	return domain.IngestResult{ConversationID: "conv", MessageID: 1, JobID: 2}, nil
}

func (s *stubRepo) RecentMessages(context.Context, string, int) ([]domain.Message, error) {
	return nil, nil
}

func (s *stubRepo) GetConversation(context.Context, int64, domain.Kind, int64) (domain.Conversation, bool, error) {
	return domain.Conversation{}, false, nil
}

func (s *stubRepo) BotReplyMeta(context.Context, string) (time.Time, int, error) {
	return time.Time{}, 0, nil
}

func (s *stubRepo) ClaimReadyJobs(context.Context, int) ([]domain.Job, error) { return nil, nil }

func (s *stubRepo) CompleteJob(context.Context, int64) error { return nil }

func (s *stubRepo) FailJob(context.Context, int64, string, time.Duration, int) error { return nil }

func (s *stubRepo) ReleaseJob(context.Context, int64) error { return nil }

func (s *stubRepo) RecoverStaleJobs(context.Context) error { return nil }

func (s *stubRepo) RecordOutgoing(context.Context, string, int64, string, int64, string) error {
	return nil
}

func newTestService(t *testing.T) (*Service, *stubRepo) {
	t.Helper()
	repo := &stubRepo{}
	svc, err := NewService(repo, slog.New(slog.NewTextHandler(discardWriter{}, nil)))
	if err != nil {
		t.Fatalf("new service: %v", err)
	}
	return svc, repo
}

type discardWriter struct{}

func (discardWriter) Write(p []byte) (int, error) { return len(p), nil }

func TestIngestAcceptsNegativePlatformMessageID(t *testing.T) {
	// 验证有符号的 message_id 哈希（负数）可以入库：SnowLuma 的真实消息 ID 多为负值。
	svc, repo := newTestService(t)
	_, err := svc.Ingest(context.Background(), domain.IngestInput{
		BotID:             1,
		Kind:              domain.KindGroup,
		TargetID:          100,
		PlatformMessageID: -1234567890,
		SenderID:          200,
		Text:              "hello",
	})
	if err != nil {
		t.Fatalf("negative platform message id should be accepted: %v", err)
	}
	if !repo.ingested {
		t.Fatal("expected ingest to reach the repository")
	}
}

func TestIngestRejectsZeroPlatformMessageID(t *testing.T) {
	// 验证 0 作为 message_id 被拒绝：它表示事件没有可用 ID，无法去重。
	svc, repo := newTestService(t)
	_, err := svc.Ingest(context.Background(), domain.IngestInput{
		BotID:    1,
		Kind:     domain.KindGroup,
		TargetID: 100,
		SenderID: 200,
		Text:     "hello",
	})
	if err == nil {
		t.Fatal("expected error for zero platform message id")
	}
	if repo.ingested {
		t.Fatal("repository must not be called for invalid input")
	}
}
