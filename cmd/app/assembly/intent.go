// Intent pipeline and worker wiring adapters for the composition root.
// 组合根的意图流水线与工作器装配适配器。
package assembly

import (
	"context"
	"log/slog"
	"sync/atomic"
	"time"

	"github.com/cloudwego/eino/callbacks"
	"github.com/cloudwego/eino/components/tool"
	"github.com/jackc/pgx/v5/pgxpool"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"

	langfusecb "github.com/cloudwego/eino-ext/callbacks/langfuse/v2"

	agentapp "github.com/HappyLadySauceM/Nagi/internal/agent/application"
	agentcontracts "github.com/HappyLadySauceM/Nagi/internal/agent/contracts"
	einoinfra "github.com/HappyLadySauceM/Nagi/internal/agent/infrastructure/eino"
	replytool "github.com/HappyLadySauceM/Nagi/internal/agent/infrastructure/replytool"
	conversationapp "github.com/HappyLadySauceM/Nagi/internal/conversation/application"
	conversationdomain "github.com/HappyLadySauceM/Nagi/internal/conversation/domain"
	conversationinfra "github.com/HappyLadySauceM/Nagi/internal/conversation/infrastructure"
	intentapp "github.com/HappyLadySauceM/Nagi/internal/intent/application"
	intentcontracts "github.com/HappyLadySauceM/Nagi/internal/intent/contracts"
	intentdomain "github.com/HappyLadySauceM/Nagi/internal/intent/domain"
	intentinfra "github.com/HappyLadySauceM/Nagi/internal/intent/infrastructure"
	"github.com/HappyLadySauceM/Nagi/internal/observability"
	outboundapp "github.com/HappyLadySauceM/Nagi/internal/outbound/application"
	outbounddomain "github.com/HappyLadySauceM/Nagi/internal/outbound/domain"
	pluginsapp "github.com/HappyLadySauceM/Nagi/internal/plugins/application"
	pluginscontracts "github.com/HappyLadySauceM/Nagi/internal/plugins/contracts"
	einoTools "github.com/HappyLadySauceM/Nagi/internal/plugins/infrastructure/eino"
	scopeapp "github.com/HappyLadySauceM/Nagi/internal/scope/application"
	"github.com/HappyLadySauceM/Nagi/pkg/config"
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

// buildToolsResolver returns the per-run tool snapshot filtered by the
// conversation kind and the granted permissions. The registry is static after
// startup, so the snapshot is a cheap copy; the guard binds each call to the
// message scope and the granted permissions.
// buildToolsResolver 返回单次运行的工具快照，按会话类型与已授予权限过滤。
// 注册表在启动后是静态的，快照只是廉价复制；
// 守卫把每次调用绑定到消息作用域与已授予权限。
func buildToolsResolver(cfg *config.Config, registry *pluginsapp.Registry) func(context.Context, agentcontracts.Request) []tool.BaseTool {
	return func(_ context.Context, req agentcontracts.Request) []tool.BaseTool {
		snapshot := registry.Snapshot()
		if len(snapshot) == 0 {
			return nil
		}
		// The granted set comes from the scope policy, never from the snapshot
		// itself: deriving it from the tools would make the guard self-approving.
		// 授予集合来自作用域策略，绝不来自工具快照本身：
		// 由工具推导会让守卫变成自证。
		granted := pluginsapp.ResolvePermissions(snapshot, cfg.Tools.PermissionDefaults, req.PermissionOverrides)
		scope := pluginsapp.Scope{
			BotID:            req.BotID,
			JobID:            req.JobID,
			ConversationID:   req.ConversationID,
			Kind:             req.Kind,
			TargetID:         req.TargetID,
			SenderID:         req.SenderID,
			Permissions:      granted,
			AllowSideEffects: cfg.Tools.AllowSideEffects,
			Record: func(run pluginscontracts.ToolRun) {
				if req.OnToolRun == nil {
					return
				}
				req.OnToolRun(agentcontracts.ToolRun{
					ToolID:   run.ToolID,
					Status:   run.Status,
					Duration: run.Duration,
					Bytes:    run.Bytes,
				})
			},
			Timeout:        cfg.Tools.Timeout.D(),
			MaxResultBytes: cfg.Tools.MaxResultBytes,
		}
		// The budget is per run and shared by every tool the model may call.
		// 调用预算按单次运行计算，并由模型可调用的全部工具共享。
		var calls atomic.Int64
		budget := int64(cfg.Agent.MaxToolCalls)
		wrapped := make([]pluginscontracts.RegisteredTool, 0, len(snapshot))
		for _, registered := range snapshot {
			// A tool that does not apply to the conversation kind never reaches
			// the model, so it cannot even request it; the execution-time check
			// inside the tool stays as the second line of defence.
			// 不适用于当前会话类型的工具不会到达模型，模型连请求它的机会都没有；
			// 工具内部的执行期校验仍作为第二道防线。
			if !registered.Descriptor.AppliesTo(req.Kind) {
				continue
			}
			// The reply tool stays hidden unless the dedicated switch is on;
			// widening the generic side-effect flag must not expose it.
			// 未打开专用开关时回复工具不可见；
			// 放开通用副作用开关不得连带暴露它。
			if registered.Descriptor.ID == replytool.ToolID && !cfg.Tools.AllowSendCurrent {
				continue
			}
			// Revoked tools never reach the model, so it cannot even request them.
			// 已撤销的工具不会到达模型，模型连请求它的机会都没有。
			if !pluginsapp.Allowed(registered.Descriptor, granted, cfg.Tools.AllowSideEffects) {
				continue
			}
			guarded := pluginsapp.Guard(scope, registered.Descriptor, registered.Invoke)
			registered.Invoke = func(ctx context.Context, argumentsInJSON string) (string, error) {
				if budget > 0 && calls.Add(1) > budget {
					return "error: 工具调用次数已达上限，请直接回答", nil
				}
				return guarded(ctx, argumentsInJSON)
			}
			wrapped = append(wrapped, registered)
		}
		if len(wrapped) == 0 {
			return nil
		}
		return einoTools.ToBaseTools(wrapped)
	}
}

// buildLangfuseCallback wires the Eino callback whenever telemetry is enabled.
// Model and tool spans are part of the trace even when content export is off:
// in that case the callback redacts values instead of disappearing.
// buildLangfuseCallback 在遥测开启时接入 Eino 回调。
// 即使正文导出关闭，模型与工具 span 仍属于 trace：此时回调脱敏取值，
// 而不是整体消失。
func buildLangfuseCallback(cfg *config.Config, logger *slog.Logger, provider *sdktrace.TracerProvider) *langfusecb.CallbackHandler {
	if provider == nil || !cfg.AI.Enabled || !cfg.Observability.Enabled {
		return nil
	}
	if !cfg.Observability.ExportRawContent {
		logger.Info("eino langfuse callback redacts model and tool content",
			"hint", "set observability.export-raw-content=true to export prompts and replies")
	}
	handler, err := einoinfra.NewLangfuseHandler(context.Background(), provider, einoinfra.LangfuseConfig{
		BaseURL:       cfg.Observability.BaseURL,
		PublicKey:     cfg.Observability.PublicKey,
		SecretKey:     cfg.Observability.SecretKey,
		Environment:   cfg.Observability.Environment,
		RedactContent: !cfg.Observability.ExportRawContent,
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
	handler *langfusecb.CallbackHandler, ledger *outboundapp.Coordinator,
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
	if ledger == nil {
		return nil, nil
	}
	return agentapp.NewReplier(agentapp.Options{
		Runtime:  runtime,
		Scopes:   scope,
		Senders:  senders,
		Recorder: conv,
		Outbound: ledger,
		Sending: outbounddomain.Limits{
			MaxMessages:        cfg.Agent.Sending.MaxMessagesPerRun,
			MaxCharsPerMessage: cfg.Agent.Sending.MaxCharsPerMessage,
			MaxTotalChars:      cfg.Agent.Sending.MaxTotalCharsPerRun,
		},
		BotName:   cfg.Intent.BotName,
		MaxTokens: cfg.AI.MaxOutputTokens,
		Logger:    logger,
	})
}

// buildWorker wires the decision pipeline and bounded worker when intent is enabled.
// intent 启用时装配决策流水线与有界工作器。
func buildWorker(cfg *config.Config, logger *slog.Logger, pool *pgxpool.Pool,
	scope *scopeapp.Service, conv *conversationapp.Service, executor intentcontracts.ReplyExecutor,
	identity *observability.Identities, rules *intentinfra.RulesetsRepo,
) (*conversationinfra.Worker, intentcontracts.DecisionReader, error) {
	if pool == nil {
		return nil, nil, nil
	}
	if !cfg.Intent.Enabled {
		// The ingress handler is registered whenever a database exists, so
		// disabling the pipeline turns every stored message into an unconsumed job.
		// 只要配置了数据库就会注册入站处理器，关闭流水线会让每条消息变成永不被消费的任务。
		logger.Warn("intent pipeline disabled; messages are stored but jobs are never consumed",
			"hint", "set intent.enabled=true to activate the decision worker")
		return nil, nil, nil
	}
	if scope == nil || conv == nil {
		return nil, nil, nil
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
		return nil, nil, err
	}
	decisionsRepo, err := intentinfra.NewDecisionsRepo(pool)
	if err != nil {
		return nil, nil, err
	}
	decider, err := intentapp.NewDecider(intentapp.Options{
		Semantic: jev,
		History:  historyReader{svc: conv},
		Activity: activityReader{svc: conv},
		Scopes:   scope,
		Recorder: decisionsRepo,
		Rules:    rules,
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
		return nil, nil, err
	}
	repo, err := conversationinfra.NewRepo(pool)
	if err != nil {
		return nil, nil, err
	}
	worker, err := conversationinfra.NewWorker(repo, intentProcessor{decider: decider}, conversationinfra.WorkerOptions{
		Concurrency:  cfg.Worker.Concurrency,
		MaxPending:   cfg.Worker.MaxPendingJobs,
		PollInterval: cfg.Worker.PollInterval.D(),
		Identity:     identity,
	}, logger)
	return worker, decisionsRepo, err
}
