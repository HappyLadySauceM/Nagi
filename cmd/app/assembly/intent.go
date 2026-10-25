// Intent pipeline and worker wiring adapters for the composition root.
// 组合根的意图流水线与工作器装配适配器。
package assembly

import (
	"context"
	"log/slog"
	"time"

	"github.com/cloudwego/eino/callbacks"
	"github.com/cloudwego/eino/components/tool"
	"github.com/jackc/pgx/v5/pgxpool"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"

	langfusecb "github.com/cloudwego/eino-ext/callbacks/langfuse/v2"

	agentapp "github.com/HappyLadySauceM/QQBot/internal/agent/application"
	agentcontracts "github.com/HappyLadySauceM/QQBot/internal/agent/contracts"
	einoinfra "github.com/HappyLadySauceM/QQBot/internal/agent/infrastructure/eino"
	conversationapp "github.com/HappyLadySauceM/QQBot/internal/conversation/application"
	conversationdomain "github.com/HappyLadySauceM/QQBot/internal/conversation/domain"
	conversationinfra "github.com/HappyLadySauceM/QQBot/internal/conversation/infrastructure"
	intentapp "github.com/HappyLadySauceM/QQBot/internal/intent/application"
	intentcontracts "github.com/HappyLadySauceM/QQBot/internal/intent/contracts"
	intentdomain "github.com/HappyLadySauceM/QQBot/internal/intent/domain"
	intentinfra "github.com/HappyLadySauceM/QQBot/internal/intent/infrastructure"
	"github.com/HappyLadySauceM/QQBot/internal/observability"
	pluginsapp "github.com/HappyLadySauceM/QQBot/internal/plugins/application"
	pluginscontracts "github.com/HappyLadySauceM/QQBot/internal/plugins/contracts"
	einoTools "github.com/HappyLadySauceM/QQBot/internal/plugins/infrastructure/eino"
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

// buildToolsResolver returns the per-run tool snapshot. The registry is static
// after startup, so the snapshot is a cheap copy; the guard binds each call to
// the message scope and the granted permissions.
// buildToolsResolver 返回单次运行的工具快照。注册表在启动后是静态的，
// 快照只是廉价复制；守卫把每次调用绑定到消息作用域与已授予权限。
func buildToolsResolver(cfg *config.Config, registry *pluginsapp.Registry) func(context.Context, agentcontracts.Request) []tool.BaseTool {
	return func(_ context.Context, req agentcontracts.Request) []tool.BaseTool {
		snapshot := registry.Snapshot()
		if len(snapshot) == 0 {
			return nil
		}
		permissions := make(map[string]bool, len(snapshot))
		for _, registered := range snapshot {
			if registered.Descriptor.Permission != "" {
				permissions[registered.Descriptor.Permission] = true
			}
		}
		scope := pluginsapp.Scope{
			BotID:          req.BotID,
			ConversationID: req.ConversationID,
			Kind:           req.Kind,
			TargetID:       req.TargetID,
			SenderID:       req.SenderID,
			Permissions:    permissions,
			Timeout:        cfg.Tools.Timeout.D(),
			MaxResultBytes: cfg.Tools.MaxResultBytes,
		}
		wrapped := make([]pluginscontracts.RegisteredTool, 0, len(snapshot))
		for _, registered := range snapshot {
			registered.Invoke = pluginsapp.Guard(scope, registered.Descriptor, registered.Invoke)
			wrapped = append(wrapped, registered)
		}
		return einoTools.ToBaseTools(wrapped)
	}
}

// buildLangfuseCallback wires the Eino callback when telemetry and raw content
// export are both enabled; the callback exports model messages, so it follows
// the same privacy gate as the business spans.
// buildLangfuseCallback 在遥测与正文导出同时开启时接入 Eino 回调；
// 回调会导出模型消息，因此沿用与业务 span 相同的隐私门控。
func buildLangfuseCallback(cfg *config.Config, logger *slog.Logger, provider *sdktrace.TracerProvider) *langfusecb.CallbackHandler {
	if provider == nil || !cfg.AI.Enabled || !cfg.Observability.Enabled {
		return nil
	}
	if !cfg.Observability.ExportRawContent {
		logger.Info("eino langfuse callback disabled: export-raw-content=false")
		return nil
	}
	handler, err := einoinfra.NewLangfuseHandler(context.Background(), provider, einoinfra.LangfuseConfig{
		BaseURL:     cfg.Observability.BaseURL,
		PublicKey:   cfg.Observability.PublicKey,
		SecretKey:   cfg.Observability.SecretKey,
		Environment: cfg.Observability.Environment,
	})
	if err != nil {
		logger.Warn("eino langfuse callback disabled", "error", err)
		return nil
	}
	return handler
}

// buildReplyExecutor wires the Eino-backed reply executor when generation is
// enabled. Long-term memory is deliberately not part of this stage.
// ai.enabled 时装配 Eino 驱动的回复执行器；本阶段刻意不接入长期记忆。
func buildReplyExecutor(ctx context.Context, cfg *config.Config, logger *slog.Logger, pool *pgxpool.Pool,
	scope *scopeapp.Service, conv *conversationapp.Service, senders agentapp.SenderResolver,
	handler *langfusecb.CallbackHandler,
	tools func(ctx context.Context, req agentcontracts.Request) []tool.BaseTool,
) (intentcontracts.ReplyExecutor, error) {
	if !cfg.AI.Enabled || pool == nil || scope == nil || conv == nil {
		return nil, nil
	}
	runtimeCfg := einoinfra.Config{
		BaseURL:         cfg.AI.BaseURL,
		APIKey:          cfg.AI.APIKey,
		Model:           cfg.AI.Model,
		MaxOutputTokens: cfg.AI.MaxOutputTokens,
		Timeout:         cfg.Agent.MaxRunDuration.D(),
		HTTPTimeout:     cfg.AI.GenerationTimeout.D(),
		MaxSteps:        cfg.Agent.MaxSteps,
		Tools:           tools,
	}
	if handler != nil {
		// Register per run so the callback never becomes global state.
		// 按 Run 注册，避免回调变成全局状态。
		runtimeCfg.Handlers = []callbacks.Handler{handler}
	}
	runtime, err := einoinfra.NewRuntime(ctx, runtimeCfg)
	if err != nil {
		return nil, err
	}
	return agentapp.NewReplier(agentapp.Options{
		Runtime:   runtime,
		Scopes:    scope,
		Senders:   senders,
		Recorder:  conv,
		BotName:   cfg.Intent.BotName,
		MaxTokens: cfg.AI.MaxOutputTokens,
		Logger:    logger,
	})
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
