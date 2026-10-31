// Intent pipeline and worker wiring adapters for the composition root.
// 组合根的意图流水线与工作器装配适配器。
package assembly

import (
	"context"
	"fmt"
	"log/slog"
	"sync/atomic"
	"time"

	"github.com/HappyLadySauceM/Nagi/pkg/db"
	"github.com/cloudwego/eino/callbacks"
	"github.com/cloudwego/eino/components/tool"
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
	scopedomain "github.com/HappyLadySauceM/Nagi/internal/scope/domain"
	settingsapp "github.com/HappyLadySauceM/Nagi/internal/settings/application"
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
		if m.Direction == conversationdomain.Outgoing && m.SentStatus == "failed" {
			continue
		}
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
		ClaimToken:     job.ClaimToken,
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

// ConversationIDForScope locates an existing conversation without creating one.
// ConversationIDForScope 查找已有会话，不创建新会话。
func (a activityReader) ConversationIDForScope(ctx context.Context, botID int64, kind string, targetID int64) (string, error) {
	conversation, found, err := a.svc.GetConversation(ctx, botID, conversationdomain.Kind(kind), targetID)
	if err != nil {
		return "", err
	}
	if !found {
		return "", nil
	}
	return conversation.ID, nil
}

// buildToolsResolver returns the per-run tool snapshot filtered by the live
// settings, the conversation kind and the granted permissions. The registry is
// static after startup, so the snapshot is a cheap copy; the settings provider
// is read on every run, which is what makes console toggles effective without
// a restart.
// buildToolsResolver 返回单次运行的工具快照，按会话类型与已授予权限过滤。
// 注册表在启动后是静态的，快照只是廉价复制；配置提供者每次运行都会读取，
// 控制台的开关因此无需重启即可生效。
func buildToolsResolver(settings *settingsapp.Provider, registry *pluginsapp.Registry,
	fresh ...*scopeapp.Service,
) func(context.Context, agentcontracts.Request) []tool.BaseTool {
	return func(ctx context.Context, req agentcontracts.Request) []tool.BaseTool {
		live := settings.Settings()
		visible := registry.Snapshot()
		if len(visible) == 0 {
			return nil
		}
		// The granted set comes from the scope policy, never from the snapshot
		// itself: deriving it from the tools would make the guard self-approving.
		// 授予集合来自作用域策略，绝不来自工具快照本身：
		// 由工具推导会让守卫变成自证。
		granted := pluginsapp.ResolvePermissions(visible, live.Tools.PermissionDefaults, req.PermissionOverrides)
		// Suppression happens in the guard when a credential tool actually runs;
		// the granted set alone must not silence the conversation.
		// 脱敏由守卫在实际执行凭证工具时触发；仅凭授予集合不应静音会话。
		var replyStopped atomic.Bool
		scope := pluginsapp.Scope{
			BotID:          req.BotID,
			JobID:          req.JobID,
			ClaimToken:     req.ClaimToken,
			ConversationID: req.ConversationID,
			Kind:           req.Kind,
			TargetID:       req.TargetID,
			SenderID:       req.SenderID,
			Permissions:    granted,
			Record: func(run pluginscontracts.ToolRun) {
				// Only the current-conversation tool stops further replies; a failed
				// send to another target says nothing about the source conversation.
				// 只有当前会话工具会停止后续回复；发往其它目标失败不影响来源会话。
				if run.ToolID == replytool.ToolID && run.Status != pluginsapp.StatusOK {
					replyStopped.Store(true)
				}
				if req.OnToolRun == nil {
					return
				}
				req.OnToolRun(agentcontracts.ToolRun{
					ErrorCode: run.ErrorCode,
					ToolID:    run.ToolID,
					Status:    run.Status,
					Duration:  run.Duration,
					Bytes:     run.Bytes,
				})
			},
			Timeout:        time.Duration(live.Tools.TimeoutSeconds) * time.Second,
			MaxResultBytes: live.Tools.MaxResultBytes,
		}
		{
			scope.Reauthorize = func(ctx context.Context, descriptor pluginscontracts.ToolDescriptor) error {
				if descriptor.ReplyCurrent && replyStopped.Load() {
					return fmt.Errorf("reply run already stopped")
				}
				// Settings are re-read here too, so revoking a permission takes
				// effect inside a running turn.
				// 这里同样重新读取配置：撤销权限对进行中的回合立即生效。
				latest := settings.Settings()
				if len(fresh) == 0 || fresh[0] == nil {
					return nil
				}
				current, err := fresh[0].Resolve(ctx, req.BotID, scopedomain.ScopeType(req.Kind), req.TargetID)
				if err != nil {
					return err
				}
				if !current.Enabled || current.ReplyMode == scopedomain.ModeObserveOnly {
					return fmt.Errorf("scope authorization revoked")
				}
				grants := pluginsapp.ResolvePermissions(visible, latest.Tools.PermissionDefaults, current.ToolPermissions)
				if !pluginsapp.Allowed(descriptor, grants) {
					return fmt.Errorf("tool authorization revoked")
				}
				return nil
			}
		}
		// The budget is per run and shared by every tool the model may call.
		// 调用预算按单次运行计算，并由模型可调用的全部工具共享。
		var calls atomic.Int64
		budget := int64(live.Agent.MaxToolCalls)
		wrapped := make([]pluginscontracts.RegisteredTool, 0, len(visible))
		for _, registered := range visible {
			// A tool that does not apply to the conversation kind never reaches
			// the model, so it cannot even request it; the execution-time check
			// inside the tool stays as the second line of defence.
			// 不适用于当前会话类型的工具不会到达模型，模型连请求它的机会都没有；
			// 工具内部的执行期校验仍作为第二道防线。
			if !registered.Descriptor.AppliesTo(req.Kind) {
				continue
			}
			// Revoked tools never reach the model, so it cannot even request them.
			// 已撤销的工具不会到达模型，模型连请求它的机会都没有。
			if !pluginsapp.Allowed(registered.Descriptor, granted) {
				continue
			}
			guarded := pluginsapp.Guard(scope, registered.Descriptor, registered.Invoke)
			registered.Invoke = func(ctx context.Context, argumentsInJSON string) (string, error) {
				if budget > 0 && calls.Add(1) > budget {
					if scope.Record != nil {
						scope.Record(pluginscontracts.ToolRun{ToolID: registered.Descriptor.ID, Status: pluginsapp.StatusDenied, ErrorCode: "tool_call_budget"})
					}
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
	// Generation may be switched on later from the console, so the callback is
	// wired whenever telemetry is on; an idle callback costs nothing.
	// 生成模型可能稍后在控制台开启，因此只要遥测开启就接入回调；
	// 空闲的回调没有额外开销。
	if provider == nil || !cfg.Observability.Enabled {
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

// buildReplyExecutor wires the Eino-backed reply executor. The runtime is built
// lazily from the live settings so enabling generation or switching the model in
// the console needs no restart; long-term memory is deliberately out of scope.
// buildReplyExecutor 装配 Eino 驱动的回复执行器。运行时按实时配置惰性构建，
// 控制台开启生成或切换模型因此无需重启；本阶段刻意不接入长期记忆。
func buildReplyExecutor(cfg *config.Config, logger *slog.Logger, pool *db.Pool,
	settings *settingsapp.Provider,
	scope *scopeapp.Service, conv *conversationapp.Service, senders agentapp.SenderResolver,
	handler *langfusecb.CallbackHandler, ledger *outboundapp.Coordinator,
	limits func(ctx context.Context, botID int64, kind string, targetID int64) outbounddomain.Limits,
	tools func(ctx context.Context, req agentcontracts.Request) []tool.BaseTool,
) (intentcontracts.ReplyExecutor, error) {
	if pool == nil || scope == nil || conv == nil || ledger == nil {
		return nil, nil
	}
	var handlers []callbacks.Handler
	if handler != nil {
		// Register per run so the callback never becomes global state.
		// 按 Run 注册，避免回调变成全局状态。
		handlers = []callbacks.Handler{handler}
	}
	live := settings.Settings()
	return agentapp.NewReplier(agentapp.Options{
		Runtime:  newModelRuntime(settings, cfg.AI.APIKey, tools, handlers, logger),
		Scopes:   scope,
		Senders:  senders,
		Recorder: conv,
		Outbound: ledger,
		Sending: outbounddomain.Limits{
			MaxMessages:        live.Agent.Sending.MaxMessagesPerRun,
			MaxCharsPerMessage: live.Agent.Sending.MaxCharsPerMessage,
			MaxTotalChars:      live.Agent.Sending.MaxTotalCharsPerRun,
		},
		SendingLimits: limits,
		BotName:       live.Intent.BotName,
		MaxTokens:     live.AI.MaxOutputTokens,
		BotNameProvider: func() string {
			return settings.Settings().Intent.BotName
		},
		MaxTokensProvider: func() int {
			return settings.Settings().AI.MaxOutputTokens
		},
		Logger: logger,
	})
}

// buildWorker wires the decision pipeline and the bounded worker. The worker is
// created whenever the database exists: with intent disabled it still consumes
// jobs (skipping the decision), so the console can switch the pipeline on later
// without a restart and without a backlog.
// buildWorker 装配决策流水线与有界工作器。只要数据库存在就创建工作器：
// 流水线关闭时它仍消费任务（跳过决策），
// 控制台因此可以在不重启、不积压的情况下随后启用。
func buildWorker(cfg *config.Config, logger *slog.Logger, pool *db.Pool,
	settings *settingsapp.Provider,
	scope *scopeapp.Service, conv *conversationapp.Service, executor intentcontracts.ReplyExecutor,
	identity *observability.Identities, rules *intentinfra.RulesetsRepo,
) (*conversationinfra.Worker, intentcontracts.DecisionReader, intentcontracts.SimulationEvaluator, error) {
	if pool == nil || scope == nil || conv == nil {
		return nil, nil, nil, nil
	}
	if !settings.Settings().Intent.Enabled {
		logger.Info("intent pipeline starts disabled; stored messages are consumed without a decision",
			"hint", "enable the pipeline in 系统配置 to start replying")
	}
	if cfg.Intent.APIKey == "" && settings.Settings().Intent.Provider == "jev" {
		logger.Warn("jev api-key missing; semantic decisions degrade to explicit mentions only",
			"hint", "set intent.api-key in the config file")
	}
	semantic := newSemanticRouter(settings, cfg.Intent.APIKey, logger)
	decisionsRepo, err := intentinfra.NewDecisionsRepo(pool)
	if err != nil {
		return nil, nil, nil, err
	}
	live := settings.Settings()
	decider, err := intentapp.NewDecider(intentapp.Options{
		Semantic: semantic,
		History:  historyReader{svc: conv},
		Activity: activityReader{svc: conv},
		Scopes:   scope,
		Recorder: decisionsRepo,
		Rules:    rules,
		Executor: executor,
		Thresholds: intentdomain.Thresholds{
			Continuation:      live.Intent.ContinuationThreshold,
			Interjection:      live.Intent.InterjectionThreshold,
			AddressedOtherMax: live.Intent.AddressedOtherMax,
		},
		HistoryLimit: live.Intent.HistoryLimit,
		Provider:     live.Intent.Provider,
		Model:        live.Intent.Model,
		// The console changes these without a restart, so the decider reads
		// them per task instead of freezing the startup values.
		// 控制台可不停机修改这些值，因此决策器按任务读取，而不是固化启动值。
		Dynamic: func() intentapp.RuntimeConfig {
			current := settings.Settings()
			return intentapp.RuntimeConfig{
				Enabled:      current.Intent.Enabled,
				AIEnabled:    current.AI.Enabled,
				HistoryLimit: current.Intent.HistoryLimit,
				Provider:     current.Intent.Provider,
				Model:        current.Intent.Model,
				Thresholds: intentdomain.Thresholds{
					Continuation:      current.Intent.ContinuationThreshold,
					Interjection:      current.Intent.InterjectionThreshold,
					AddressedOtherMax: current.Intent.AddressedOtherMax,
				},
			}
		},
		Logger: logger,
	})
	if err != nil {
		return nil, nil, nil, err
	}
	repo, err := conversationinfra.NewRepo(pool)
	if err != nil {
		return nil, nil, nil, err
	}
	worker, err := conversationinfra.NewWorker(repo, intentProcessor{decider: decider}, conversationinfra.WorkerOptions{
		Concurrency:  cfg.Worker.Concurrency,
		MaxPending:   cfg.Worker.MaxPendingJobs,
		PollInterval: cfg.Worker.PollInterval.D(),
		Identity:     identity,
	}, logger)
	return worker, decisionsRepo, decider, err
}
