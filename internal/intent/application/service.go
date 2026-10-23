// Package application implements the intent decision pipeline.
// intent/application 实现意图决策流水线。
package application

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"

	scopecontracts "github.com/HappyLadySauceM/QQBot/internal/scope/contracts"
	scopedomain "github.com/HappyLadySauceM/QQBot/internal/scope/domain"

	"github.com/HappyLadySauceM/QQBot/internal/intent/contracts"
	"github.com/HappyLadySauceM/QQBot/internal/intent/domain"
)

// SemanticDecider asks the external semantic service; implementations must be
// fail-closed on malformed or out-of-range responses.
// SemanticDecider 请求外部语义服务；实现必须对畸形或越界响应关闭式失败。
type SemanticDecider interface {
	Decide(ctx context.Context, in domain.Input) (domain.SemanticAnswer, error)
}

// HistoryReader supplies authorized recent messages for decision context.
// HistoryReader 提供用于决策上下文的已授权近期消息。
type HistoryReader interface {
	RecentIntentMessages(ctx context.Context, conversationID string, limit int) ([]domain.Message, error)
}

// ActivityReader supplies bot reply metadata for the rate rules. It reads
// timestamps and counts only — never bodies — so it stays independent of the
// chat-history authorization and the rate gate cannot be bypassed by disabling
// history reads.
// ActivityReader 为频率规则提供机器人发言元数据。仅读取时间戳与计数——不含正文——
// 因此独立于聊天历史授权，频率闸门不会因关闭历史读取而被绕过。
type ActivityReader interface {
	BotReplyMeta(ctx context.Context, conversationID string) (lastReplyAt time.Time, repliesLastHour int, err error)
}

// DecisionRecorder persists final decisions for later evaluation.
// DecisionRecorder 持久化最终决策，供后续评估。
type DecisionRecorder interface {
	Record(ctx context.Context, jobID int64, decision domain.FinalDecision) error
}

// ReplyExecutor acts on reply decisions; nil in decision-only deployments.
// ReplyExecutor 对回复决策执行动作；仅决策部署中为 nil。
type ReplyExecutor = contracts.ReplyExecutor

// Options configure the decider.
// Options 配置决策器。
type Options struct {
	Semantic     SemanticDecider
	History      HistoryReader
	Activity     ActivityReader // 可为 nil；nil 时退回历史推导 / may be nil; falls back to history
	Scopes       scopecontracts.Authorizer
	Recorder     DecisionRecorder
	Executor     ReplyExecutor // 可为 nil / may be nil
	Thresholds   domain.Thresholds
	HistoryLimit int
	Provider     string
	Model        string
	Logger       *slog.Logger
}

// Decider orchestrates rules, optional semantic judgment, reply policy and recording.
// Decider 编排规则、可选语义判定、回复策略与记录。
type Decider struct {
	scopes       scopecontracts.Authorizer
	history      HistoryReader
	activity     ActivityReader
	semantic     SemanticDecider
	recorder     DecisionRecorder
	executor     ReplyExecutor
	th           domain.Thresholds
	historyLimit int
	provider     string
	model        string
	logger       *slog.Logger
}

// NewDecider validates dependencies and builds the decider.
// NewDecider 校验依赖并构建决策器。
func NewDecider(opts Options) (*Decider, error) {
	if opts.Scopes == nil || opts.Recorder == nil || opts.Logger == nil {
		return nil, fmt.Errorf("intent: dependencies required")
	}
	if opts.HistoryLimit <= 0 {
		opts.HistoryLimit = 30
	}
	return &Decider{
		scopes: opts.Scopes, history: opts.History, activity: opts.Activity,
		semantic: opts.Semantic, recorder: opts.Recorder, executor: opts.Executor,
		th: opts.Thresholds, historyLimit: opts.HistoryLimit,
		provider: opts.Provider, model: opts.Model, logger: opts.Logger,
	}, nil
}

// ProcessTask resolves the policy fresh, applies rules, optionally the semantic
// decider, records the final decision and dispatches replies.
// ProcessTask 即时解析策略，应用规则与可选语义判定，记录最终决策并分发回复。
func (d *Decider) ProcessTask(ctx context.Context, task domain.Task) error {
	policy, err := d.scopes.Resolve(ctx, task.BotID, scopedomain.ScopeType(task.Kind), task.TargetID)
	if err != nil {
		// Retryable infrastructure failure: hand the error back to the worker.
		// 可重试的基础设施故障：把错误交还给工作器。
		return fmt.Errorf("intent: resolve policy: %w", err)
	}
	var history []domain.Message
	if policy.AllowHistoryRead && d.history != nil {
		history, err = d.history.RecentIntentMessages(ctx, task.ConversationID, d.historyLimit)
		if err != nil {
			// Degrade to current-message-only instead of failing the job.
			// 降级为仅使用当前消息，而不是让任务失败。
			d.logger.Warn("load conversation history failed", "error", err)
			history = nil
		}
	}
	// Rate-rule metadata is fetched regardless of history authorization; a
	// storage failure fails the job (retryable) rather than bypassing the gate.
	// 频率规则元数据与历史授权无关；存储故障使任务失败（可重试）而不是绕过闸门。
	var activity domain.BotActivity
	if d.activity != nil {
		lastReplyAt, repliesLastHour, err := d.activity.BotReplyMeta(ctx, task.ConversationID)
		if err != nil {
			return fmt.Errorf("intent: load bot activity: %w", err)
		}
		activity = domain.BotActivity{Known: true, LastReplyAt: lastReplyAt, RepliesLastHour: repliesLastHour}
	}
	in := domain.Input{Task: task, Recent: history, Activity: activity, Policy: policy, Now: time.Now()}

	rule := EvaluateRules(in)
	var semantic domain.SemanticAnswer
	var latency int64
	if rule.NeedSemantic && d.semantic != nil {
		start := time.Now()
		semantic, err = d.semantic.Decide(ctx, in)
		latency = time.Since(start).Milliseconds()
		if err != nil {
			// Semantic unavailability degrades to rules-only, never to guessing.
			// 语义不可用降级为仅规则决策，绝不臆测。
			d.logger.Warn("semantic decision unavailable", "error", err, "job_id", task.JobID)
			semantic = domain.SemanticAnswer{}
		}
	}

	decision := EvaluateReplyPolicy(in, rule, semantic, d.th)
	decision.Provider = d.provider
	decision.Model = d.model
	decision.LatencyMS = latency
	_, span := otel.Tracer("github.com/HappyLadySauceM/QQBot/internal/intent").
		Start(ctx, "qqbot.intent.decide")
	span.SetAttributes(
		attribute.String("decision", string(decision.Decision)),
		attribute.String("reason_code", decision.ReasonCode),
		attribute.String("provider", d.provider),
		attribute.String("model", d.model),
		attribute.Int64("latency_ms", latency),
	)
	span.End()
	if err := d.recorder.Record(ctx, task.JobID, decision); err != nil {
		d.logger.Warn("record decision failed", "error", err, "job_id", task.JobID)
	}
	d.logger.Info("intent decided",
		"job_id", task.JobID, "decision", string(decision.Decision),
		"reason", decision.ReasonCode, "latency_ms", latency)

	if decision.Decision == domain.DecisionReply && d.executor != nil {
		return d.executor.ExecuteReply(ctx, task, history)
	}
	return nil
}
