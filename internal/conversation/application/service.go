// Package application implements message ingest and history use cases.
// conversation/application 实现消息入库与历史查询用例。
package application

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/HappyLadySauceM/Nagi/internal/conversation/domain"
)

// Repository persists conversations, messages and decision jobs; implemented by infrastructure.
// Repository 持久化会话、消息与决策任务；由 infrastructure 实现。
type Repository interface {
	Ingest(ctx context.Context, in domain.IngestInput) (domain.IngestResult, error)
	RecentMessages(ctx context.Context, conversationID string, limit int) ([]domain.Message, error)
	GetConversation(ctx context.Context, botID int64, kind domain.Kind, targetID int64) (domain.Conversation, bool, error)
	BotReplyMeta(ctx context.Context, conversationID string) (time.Time, int, error)
	ClaimReadyJobs(ctx context.Context, limit int) ([]domain.Job, error)
	CompleteJob(ctx context.Context, jobID int64) error
	FailJob(ctx context.Context, jobID int64, code string, retryAfter time.Duration, maxAttempts int) error
	ReleaseJob(ctx context.Context, jobID int64) error
	RecoverStaleJobs(ctx context.Context) error
	RecordOutgoing(ctx context.Context, conversationID string, botID int64, text string, platformMessageID int64, sentStatus string) error
}

// Ingestor is the minimal ingest port used by the AI ingress handler.
// Ingestor 是 AI 入站处理器使用的最小入库端口。
type Ingestor interface {
	Ingest(ctx context.Context, in domain.IngestInput) (domain.IngestResult, error)
}

// Service provides ingest validation and history queries.
// Service 提供入库校验与历史查询。
type Service struct {
	repo   Repository
	logger *slog.Logger
}

// NewService validates dependencies and builds the service.
// NewService 校验依赖并构建服务。
func NewService(repo Repository, logger *slog.Logger) (*Service, error) {
	if repo == nil || logger == nil {
		return nil, fmt.Errorf("conversation: dependencies required")
	}
	return &Service{repo: repo, logger: logger}, nil
}

// Ingest validates and persists one authorized inbound message, creating the
// decision job in the same transaction; duplicates are reported, not errored.
// Ingest 校验并持久化一条已授权入站消息，在同一事务中创建决策任务；
// 重复消息以结果报告而不作为错误。
func (s *Service) Ingest(ctx context.Context, in domain.IngestInput) (domain.IngestResult, error) {
	if in.BotID <= 0 {
		return domain.IngestResult{}, fmt.Errorf("conversation: bot id must be positive")
	}
	if !in.Kind.Valid() {
		return domain.IngestResult{}, fmt.Errorf("conversation: invalid kind %q", in.Kind)
	}
	if in.TargetID <= 0 || in.SenderID <= 0 {
		return domain.IngestResult{}, fmt.Errorf("conversation: target and sender id must be positive")
	}
	// OneBot v11 message ids are signed int32 hashes, so real messages are often
	// negative; only zero is invalid (it means "no id").
	// OneBot v11 的 message_id 是有符号 int32 哈希，真实消息常为负数，只有 0 表示没有 ID。
	if in.PlatformMessageID == 0 {
		return domain.IngestResult{}, fmt.Errorf("conversation: platform message id must be non-zero")
	}
	result, err := s.repo.Ingest(ctx, in)
	if err != nil {
		return domain.IngestResult{}, err
	}
	if !result.Duplicate {
		s.logger.Info("message ingested",
			"bot_id", in.BotID, "kind", string(in.Kind), "message_id", result.MessageID)
	}
	return result, nil
}

// RecentMessages returns the newest messages of one conversation, oldest first.
// RecentMessages 返回一个会话最近的消息，按时间正序。
func (s *Service) RecentMessages(ctx context.Context, conversationID string, limit int) ([]domain.Message, error) {
	if limit <= 0 {
		limit = 30
	}
	return s.repo.RecentMessages(ctx, conversationID, limit)
}

// GetConversation returns one conversation when it exists.
// GetConversation 返回已存在的会话。
func (s *Service) GetConversation(ctx context.Context, botID int64, kind domain.Kind, targetID int64) (domain.Conversation, bool, error) {
	return s.repo.GetConversation(ctx, botID, kind, targetID)
}

// BotReplyMeta returns the bot's last reply time and hourly reply count for one
// conversation without reading message bodies.
// BotReplyMeta 返回会话内机器人最近回复时间与每小时回复数，不读取消息正文。
func (s *Service) BotReplyMeta(ctx context.Context, conversationID string) (time.Time, int, error) {
	if conversationID == "" {
		return time.Time{}, 0, fmt.Errorf("conversation: bot reply meta requires conversation id")
	}
	return s.repo.BotReplyMeta(ctx, conversationID)
}

// RecordOutgoing persists one bot reply so later context can see what it said.
// RecordOutgoing 持久化一条机器人回复，保证后续上下文能看到机器人说过什么。
func (s *Service) RecordOutgoing(ctx context.Context, conversationID string, botID int64, text string, platformMessageID int64, sentStatus string) error {
	if conversationID == "" || botID <= 0 {
		return fmt.Errorf("conversation: outgoing record requires conversation and bot id")
	}
	return s.repo.RecordOutgoing(ctx, conversationID, botID, text, platformMessageID, sentStatus)
}
