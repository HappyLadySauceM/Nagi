// Intent pipeline and worker wiring adapters for the composition root.
// 组合根的意图流水线与工作器装配适配器。
package assembly

import (
	"context"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	agentapp "github.com/HappyLadySauceM/QQBot/internal/agent/application"
	agentinfra "github.com/HappyLadySauceM/QQBot/internal/agent/infrastructure"
	conversationapp "github.com/HappyLadySauceM/QQBot/internal/conversation/application"
	conversationdomain "github.com/HappyLadySauceM/QQBot/internal/conversation/domain"
	conversationinfra "github.com/HappyLadySauceM/QQBot/internal/conversation/infrastructure"
	intentapp "github.com/HappyLadySauceM/QQBot/internal/intent/application"
	intentcontracts "github.com/HappyLadySauceM/QQBot/internal/intent/contracts"
	intentdomain "github.com/HappyLadySauceM/QQBot/internal/intent/domain"
	intentinfra "github.com/HappyLadySauceM/QQBot/internal/intent/infrastructure"
	memoryapp "github.com/HappyLadySauceM/QQBot/internal/memory/application"
	memorydomain "github.com/HappyLadySauceM/QQBot/internal/memory/domain"
	"github.com/HappyLadySauceM/QQBot/internal/observability"
	scopeapp "github.com/HappyLadySauceM/QQBot/internal/scope/application"
	"github.com/HappyLadySauceM/QQBot/pkg/config"
)

// historyReader adapts the conversation service to the intent history port.
// historyReader 将会话服务适配为意图历史端口。
type historyReader struct {
	svc *conversationapp.Service
}

// RecentIntentMessages converts stored messages into intent messages; bot
// outgoing messages are marked so reply-to-bot and cooldown rules work.
// RecentIntentMessages 将存储消息转换为意图消息；出站消息标记为机器人，
// 供引用判定与冷却规则使用。
func (h historyReader) RecentIntentMessages(ctx context.Context, conversationID string, limit int) ([]intentdomain.Message, error) {
	msgs, err := h.svc.RecentMessages(ctx, conversationID, limit)
	if err != nil {
		return nil, err
	}
	out := make([]intentdomain.Message, 0, len(msgs))
	for _, m := range msgs {
		out = append(out, intentdomain.Message{
			MessageID: m.PlatformMessageID,
			SenderID:  m.SenderID,
			IsBot:     m.Direction == conversationdomain.Outgoing,
			Text:      m.Text,
			CreatedAt: m.CreatedAt,
		})
	}
	return out, nil
}

// intentProcessor adapts the intent decider to the conversation JobProcessor port.
// intentProcessor 将意图决策器适配为会话的 JobProcessor 端口。
type intentProcessor struct {
	decider *intentapp.Decider
}

// Process converts the job into an intent task and runs the decision pipeline.
// Process 将任务转换为意图任务并执行决策流水线。
func (p intentProcessor) Process(ctx context.Context, job conversationdomain.Job) error {
	return p.decider.ProcessTask(ctx, intentdomain.Task{
		JobID:          job.ID,
		BotID:          job.BotID,
		Kind:           string(job.Kind),
		TargetID:       job.TargetID,
		ConversationID: job.ConversationID,
		Message: intentdomain.Message{
			MessageID: job.Message.PlatformMessageID,
			SenderID:  job.Message.SenderID,
			Text:      job.Message.Text,
		},
		Segments: job.Message.Segments,
	})
}

// activityReader adapts the conversation service to the intent activity port.
// activityReader 将会话服务适配为意图的活动元数据端口。
type activityReader struct {
	svc *conversationapp.Service
}

// BotReplyMeta forwards the metadata query without message bodies.
// BotReplyMeta 转发不含正文的元数据查询。
func (a activityReader) BotReplyMeta(ctx context.Context, conversationID string) (time.Time, int, error) {
	return a.svc.BotReplyMeta(ctx, conversationID)
}

// memoryFinder adapts the memory service to the agent memory port.
// memoryFinder 将记忆服务适配为 agent 的记忆端口。
type memoryFinder struct {
	svc *memoryapp.Service
}

// Find converts visible memories into prompt snippets.
// Find 将可见记忆转换为提示词片段。
func (m memoryFinder) Find(ctx context.Context, botID int64, kind string, targetID int64, query string, limit int) ([]string, error) {
	memories, err := m.svc.SearchVisible(ctx, memorydomain.SearchRequest{
		BotID: botID, Kind: kind, TargetID: targetID, Query: query, Limit: limit,
	})
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(memories))
	for _, mem := range memories {
		out = append(out, mem.Content)
	}
	return out, nil
}

// buildReplyExecutor wires the LLM reply executor when AI generation is enabled.
// ai.enabled 时装配 LLM 回复执行器。
func buildReplyExecutor(cfg *config.Config, logger *slog.Logger, pool *pgxpool.Pool,
	scope *scopeapp.Service, conv *conversationapp.Service, memory *memoryapp.Service, senders agentapp.SenderResolver,
) (intentcontracts.ReplyExecutor, error) {
	if !cfg.AI.Enabled || pool == nil || scope == nil || conv == nil {
		return nil, nil
	}
	apiKey := cfg.AI.APIKey
	model, err := agentinfra.NewOpenAI(agentinfra.OpenAIConfig{
		BaseURL:         cfg.AI.BaseURL,
		APIKey:          apiKey,
		Model:           cfg.AI.Model,
		Timeout:         cfg.AI.GenerationTimeout.D(),
		MaxOutputTokens: cfg.AI.MaxOutputTokens,
	})
	if err != nil {
		return nil, err
	}
	return agentapp.NewReplier(agentapp.Options{
		Model:     model,
		Scopes:    scope,
		Senders:   senders,
		Recorder:  conv,
		Memory:    memoryFinderAdapt(memory),
		BotName:   cfg.Intent.BotName,
		MaxTokens: cfg.AI.MaxOutputTokens,
		Logger:    logger,
	})
}

// memoryFinderAdapt returns nil when the memory service is absent.
// memoryFinderAdapt 在记忆服务缺失时返回 nil。
func memoryFinderAdapt(memory *memoryapp.Service) agentapp.MemoryFinder {
	if memory == nil {
		return nil
	}
	return memoryFinder{svc: memory}
}

// buildWorker wires the decision pipeline and bounded worker when intent is enabled.
// intent 启用时装配决策流水线与有界工作器。
func buildWorker(cfg *config.Config, logger *slog.Logger, pool *pgxpool.Pool,
	scope *scopeapp.Service, conv *conversationapp.Service, executor intentcontracts.ReplyExecutor,
	identity *observability.Identities,
) (*conversationinfra.Worker, error) {
	if pool == nil {
		return nil, nil
	}
	if !cfg.Intent.Enabled {
		// The ingress handler is registered whenever a database exists, so
		// disabling the pipeline turns every stored message into an unconsumed job.
		// 只要配置了数据库就会注册入站处理器，关闭流水线会让每条消息变成永不被消费的任务。
		logger.Warn("intent pipeline disabled; messages are stored but jobs are never consumed",
			"hint", "set intent.enabled=true to activate the decision worker")
		return nil, nil
	}
	if scope == nil || conv == nil {
		return nil, nil
	}
	jevKey := cfg.Intent.APIKey
	if jevKey == "" {
		logger.Warn("jev api-key missing; semantic decisions degrade to explicit mentions only",
			"hint", "set intent.api-key in the config file")
	}
	jev, err := intentinfra.NewJevClient(intentinfra.JevConfig{
		Endpoint: cfg.Intent.Endpoint,
		APIKey:   jevKey,
		Model:    cfg.Intent.Model,
		BotName:  cfg.Intent.BotName,
		Timeout:  cfg.Intent.Timeout.D(),
	}, logger)
	if err != nil {
		return nil, err
	}
	decisionsRepo, err := intentinfra.NewDecisionsRepo(pool)
	if err != nil {
		return nil, err
	}
	decider, err := intentapp.NewDecider(intentapp.Options{
		Semantic: jev,
		History:  historyReader{svc: conv},
		Activity: activityReader{svc: conv},
		Scopes:   scope,
		Recorder: decisionsRepo,
		Executor: executor,
		Thresholds: intentdomain.Thresholds{
			Continuation:      cfg.Intent.ContinuationThreshold,
			Interjection:      cfg.Intent.InterjectionThreshold,
			AddressedOtherMax: cfg.Intent.AddressedOtherMax,
		},
		HistoryLimit: cfg.Intent.HistoryLimit,
		Provider:     cfg.Intent.Provider,
		Model:        cfg.Intent.Model,
		Logger:       logger,
	})
	if err != nil {
		return nil, err
	}
	repo, err := conversationinfra.NewRepo(pool)
	if err != nil {
		return nil, err
	}
	return conversationinfra.NewWorker(repo, intentProcessor{decider: decider}, conversationinfra.WorkerOptions{
		Concurrency:  cfg.Worker.Concurrency,
		MaxPending:   cfg.Worker.MaxPendingJobs,
		PollInterval: cfg.Worker.PollInterval.D(),
		Identity:     identity,
	}, logger)
}
