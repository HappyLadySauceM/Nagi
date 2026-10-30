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
	"go.opentelemetry.io/otel/trace"

	"github.com/HappyLadySauceM/Nagi/internal/observability"
	scopecontracts "github.com/HappyLadySauceM/Nagi/internal/scope/contracts"
	scopedomain "github.com/HappyLadySauceM/Nagi/internal/scope/domain"

	"github.com/HappyLadySauceM/Nagi/internal/intent/contracts"
	"github.com/HappyLadySauceM/Nagi/internal/intent/domain"
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
// RulesProvider loads the published ruleset of one scope.
// RulesProvider 加载某作用域已发布的规则集。
type RulesProvider interface {
	Active(ctx context.Context, botID int64, scopeType string, scopeID int64) (domain.Ruleset, bool, error)
}

type Options struct {
	Semantic SemanticDecider
	// Rules is optional; without it the built-in ruleset applies.
	// Rules 可选；未提供时使用内置规则。
	Rules        RulesProvider
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
	rules        RulesProvider
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
		semantic: opts.Semantic,
		rules:    opts.Rules, recorder: opts.Recorder, executor: opts.Executor,
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
	history, activity, err := d.loadContext(ctx, task, policy)
	if err != nil {
		return err
	}
	in := domain.Input{Task: task, Recent: history, Activity: activity, Policy: policy, Now: time.Now()}

	rule, rulesetVersion := d.applyRules(ctx, in)
	var semantic domain.SemanticAnswer
	var latency int64
	if rule.NeedSemantic && d.semantic != nil {
		semantic, latency = d.decideSemantic(ctx, in)
	}
	decision := d.applyPolicy(ctx, in, rule, semantic, latency)
	decision.RulesetVersion = rulesetVersion
	decision.Steps = decisionSteps(policy, rule, semantic, decision, latency, rule.NeedSemantic, d.th)

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

// loadContext reads the authorized history and the rate-rule metadata inside
// one span; the span exists even when history reads are unauthorized.
// loadContext 在单个 span 内读取授权历史与频率规则元数据；
// 即使未授权读取历史，该 span 仍然存在。
func (d *Decider) loadContext(ctx context.Context, task domain.Task, policy scopedomain.Policy) ([]domain.Message, domain.BotActivity, error) {
	ctx, span := tracer().Start(ctx, "nagi.intent.load_context")
	defer span.End()
	span.SetAttributes(attribute.Bool("nagi.history_allowed", policy.AllowHistoryRead))
	// History bodies stay out of the payload: they can be dozens of messages,
	// and the count is enough to explain the decision.
	// 历史正文不进入载荷：它可能包含几十条消息，条数已足以解释决策。
	observability.RecordObservation(span, "langfuse.observation.input", map[string]any{
		"conversation_id": task.ConversationID,
		"history_limit":   d.historyLimit,
		"history_allowed": policy.AllowHistoryRead,
	})

	var history []domain.Message
	if policy.AllowHistoryRead && d.history != nil {
		var err error
		history, err = d.history.RecentIntentMessages(ctx, task.ConversationID, d.historyLimit)
		if err != nil {
			// Degrade to current-message-only instead of failing the job.
			// 降级为仅使用当前消息，而不是让任务失败。
			span.RecordError(err)
			span.SetAttributes(attribute.Bool("nagi.history_degraded", true))
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
			span.RecordError(err)
			return nil, domain.BotActivity{}, fmt.Errorf("intent: load bot activity: %w", err)
		}
		activity = domain.BotActivity{Known: true, LastReplyAt: lastReplyAt, RepliesLastHour: repliesLastHour}
	}
	span.SetAttributes(
		attribute.Int("nagi.context_count", len(history)),
		attribute.Bool("nagi.activity_known", activity.Known),
	)
	observability.RecordObservation(span, "langfuse.observation.output", map[string]any{
		"context_count":  len(history),
		"activity_known": activity.Known,
		"history_read":   policy.AllowHistoryRead,
	})
	return history, activity, nil
}

// decisionSteps turns the pipeline stages into persisted, explainable steps.
// The order matches evaluation order so the console can replay the decision.
// decisionSteps 把流水线各阶段整理为可持久化的解释步骤，
// 顺序与判定顺序一致，控制台可据此复现决策。
func decisionSteps(policy scopedomain.Policy, rule domain.RuleResult,
	semantic domain.SemanticAnswer, decision domain.FinalDecision, latency int64, asked bool, th domain.Thresholds,
) []domain.Step {
	steps := []domain.Step{{
		Index: 0,
		Phase: domain.StepPolicy,
		Detail: map[string]any{
			"enabled":               policy.Enabled,
			"reply_mode":            string(policy.ReplyMode),
			"scope_type":            string(policy.ScopeType),
			"min_reply_interval_ms": policy.MinReplyIntervalMS,
			"max_replies_per_hour":  policy.MaxRepliesPerHour,
		},
	}, {
		Index:      1,
		Phase:      domain.StepRules,
		ReasonCode: rule.ReasonCode,
		Detail: map[string]any{
			"blocked":       rule.Blocked,
			"explicit":      rule.Explicit,
			"need_semantic": rule.NeedSemantic,
			"always_reply":  rule.AlwaysReply,
			"builtin_rules": rule.BuiltinRules,
			"evaluations":   rule.Evaluations,
			"matched":       rule.Matched,
		},
	}}
	if asked {
		steps = append(steps, domain.Step{
			Index: 2,
			Phase: domain.StepSemantic,
			Detail: map[string]any{
				"available":            semantic.Available,
				"failure_code":         semantic.FailureCode,
				"intent_category":      semantic.IntentCategory,
				"directed_to_bot":      semantic.DirectedToBot,
				"continues_bot_thread": semantic.ContinuesBotThread,
				"addressed_to_other":   semantic.AddressedToOther,
				"appropriate_to_join":  semantic.AppropriateToJoin,
				"latency_ms":           latency,
			},
		})
	}
	steps = append(steps, domain.Step{
		Index:      len(steps),
		Phase:      domain.StepFinal,
		ReasonCode: decision.ReasonCode,
		Detail: map[string]any{
			"decision":   string(decision.Decision),
			"provider":   decision.Provider,
			"model":      decision.Model,
			"scores":     decision.Scores,
			"thresholds": map[string]float64{"continuation": th.Continuation, "interjection": th.Interjection, "addressed_other_max": th.AddressedOtherMax},
		},
	})
	return steps
}

// applyRules evaluates the deterministic gate and records why it stopped.
// applyRules 执行确定性规则闸门并记录其结论。
func (d *Decider) applyRules(ctx context.Context, in domain.Input) (domain.RuleResult, string) {
	_, span := tracer().Start(ctx, "nagi.intent.rules")
	defer span.End()
	rule := EvaluateRules(in)
	version := ""
	if d.rules != nil {
		global, hasGlobal, globalErr := d.rules.Active(ctx, in.Task.BotID, "group", 0)
		specific, hasSpecific, specificErr := d.rules.Active(ctx, in.Task.BotID, string(in.Policy.ScopeType), in.Policy.ScopeID)
		if globalErr != nil || specificErr != nil {
			d.logger.Warn("load ruleset failed; using built-in rules", "job_id", in.Task.JobID)
		} else {
			var specificSet, globalSet *domain.Ruleset
			if hasSpecific {
				specificSet = &specific
			}
			if hasGlobal {
				globalSet = &global
			}
			rule, version = EvaluateRuleChain(in, specificSet, globalSet)
		}
	}
	span.SetAttributes(
		attribute.Bool("nagi.rule_blocked", rule.Blocked),
		attribute.Bool("nagi.rule_explicit", rule.Explicit),
		attribute.Bool("nagi.rule_need_semantic", rule.NeedSemantic),
	)
	if rule.ReasonCode != "" {
		span.SetAttributes(attribute.String("nagi.rule_reason_code", rule.ReasonCode))
	}
	observability.RecordObservation(span, "langfuse.observation.output", map[string]any{
		"blocked":       rule.Blocked,
		"explicit":      rule.Explicit,
		"need_semantic": rule.NeedSemantic,
		"reason_code":   rule.ReasonCode,
		"ruleset":       version,
	})
	return rule, version
}

// decideSemantic calls Jev inside its own span so the network latency, the
// returned scores and the degradation are attributable. A failure yields an
// unusable answer; it is never turned into a guessed decision.
// decideSemantic 在独立 span 内调用 Jev，使网络耗时、返回分数与降级可归因。
// 失败时返回不可用回答，绝不臆测决策。
func (d *Decider) decideSemantic(ctx context.Context, in domain.Input) (domain.SemanticAnswer, int64) {
	ctx, span := tracer().Start(ctx, "nagi.intent.jev")
	defer span.End()
	span.SetAttributes(
		attribute.String("nagi.provider", d.provider),
		attribute.String("nagi.model", d.model),
	)
	// The classified message is the semantic decider's input; body export obeys
	// the content gate.
	// 被分类的消息是语义判定的输入；正文导出遵循内容开关。
	observability.RecordObservation(span, "langfuse.observation.input", map[string]any{
		"provider": d.provider,
		"model":    d.model,
		"text":     in.Task.Message.Text,
	})
	start := time.Now()
	answer, err := d.semantic.Decide(ctx, in)
	latency := time.Since(start).Milliseconds()
	span.SetAttributes(attribute.Int64("nagi.latency_ms", latency))
	if err != nil {
		// Semantic unavailability degrades to rules-only, never to guessing.
		// 语义不可用降级为仅规则决策，绝不臆测。
		span.RecordError(err)
		span.SetAttributes(attribute.Bool("nagi.jev_degraded", true))
		observability.RecordObservation(span, "langfuse.observation.output", map[string]any{
			"degraded":   true,
			"latency_ms": latency,
		})
		d.logger.Warn("semantic decision unavailable", "error", err, "job_id", in.Task.JobID)
		return domain.SemanticAnswer{FailureCode: "semantic_unavailable"}, latency
	}
	span.SetAttributes(
		attribute.String("nagi.intent_category", answer.IntentCategory),
		attribute.Float64("nagi.jev.directed_to_bot", answer.DirectedToBot),
		attribute.Float64("nagi.jev.continues_bot_thread", answer.ContinuesBotThread),
		attribute.Float64("nagi.jev.addressed_to_other", answer.AddressedToOther),
		attribute.Float64("nagi.jev.appropriate_to_join", answer.AppropriateToJoin),
	)
	observability.RecordObservation(span, "langfuse.observation.output", map[string]any{
		"intent_category":      answer.IntentCategory,
		"directed_to_bot":      answer.DirectedToBot,
		"continues_bot_thread": answer.ContinuesBotThread,
		"addressed_to_other":   answer.AddressedToOther,
		"appropriate_to_join":  answer.AppropriateToJoin,
		"latency_ms":           latency,
	})
	return answer, latency
}

// applyPolicy computes the final decision inside its own span.
// applyPolicy 在独立 span 内计算最终决策。
func (d *Decider) applyPolicy(ctx context.Context, in domain.Input, rule domain.RuleResult,
	semantic domain.SemanticAnswer, latency int64,
) domain.FinalDecision {
	_, span := tracer().Start(ctx, "nagi.intent.policy")
	defer span.End()
	decision := EvaluateReplyPolicy(in, rule, semantic, d.th)
	decision.Provider = d.provider
	decision.Model = d.model
	decision.LatencyMS = latency
	span.SetAttributes(
		attribute.String("nagi.decision", string(decision.Decision)),
		attribute.String("nagi.reason_code", decision.ReasonCode),
		attribute.String("nagi.provider", d.provider),
		attribute.String("nagi.model", d.model),
		attribute.Int64("nagi.latency_ms", latency),
	)
	observability.RecordObservation(span, "langfuse.observation.output", map[string]any{
		"decision":           string(decision.Decision),
		"reason_code":        decision.ReasonCode,
		"rule_blocked":       rule.Blocked,
		"rule_explicit":      rule.Explicit,
		"semantic_available": semantic.Available,
	})
	return decision
}

// tracer returns the shared no-op-safe tracer of this context.
// tracer 返回本上下文共享的、无 SDK 时为 no-op 的 tracer。
func tracer() trace.Tracer {
	return otel.Tracer("github.com/HappyLadySauceM/Nagi/internal/intent")
}

// EvaluateSimulation reuses the live semantic provider and thresholds without invoking the executor.
// EvaluateSimulation 复用运行中的语义服务与阈值，不调用回复执行器。
func (d *Decider) EvaluateSimulation(ctx context.Context, in domain.Input, rule domain.RuleResult, full bool) (domain.FinalDecision, []domain.Step) {
	var semantic domain.SemanticAnswer
	var latency int64
	called := full && rule.NeedSemantic && d.semantic != nil && !rule.Blocked
	if called {
		semantic, latency = d.decideSemantic(ctx, in)
	}
	decision := d.applyPolicy(ctx, in, rule, semantic, latency)
	return decision, decisionSteps(in.Policy, rule, semantic, decision, latency, called, d.th)
}

// PrepareSimulation loads the same authorized context and activity as live decisions.
// PrepareSimulation 加载与真实决策相同的已授权上下文及会话活动。
func (d *Decider) PrepareSimulation(ctx context.Context, in domain.Input) (domain.Input, error) {
	locator, ok := d.activity.(interface {
		ConversationIDForScope(context.Context, int64, string, int64) (string, error)
	})
	if !ok {
		return in, fmt.Errorf("intent: simulation context unavailable")
	}
	id, err := locator.ConversationIDForScope(ctx, in.Task.BotID, in.Task.Kind, in.Task.TargetID)
	if err != nil {
		return in, err
	}
	in.Task.ConversationID = id
	if id == "" {
		in.Recent = nil
		in.Activity = domain.BotActivity{Known: true}
		return in, nil
	}
	history, activity, err := d.loadContext(ctx, in.Task, in.Policy)
	if err != nil {
		return in, err
	}
	in.Recent = history
	in.Activity = activity
	return in, nil
}
