// Package application implements the intent decision pipeline.
// intent/application 实现意图决策流水线。
package application

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"time"

	"github.com/google/uuid"

	media "github.com/HappyLadySauceM/Nagi/internal/media/contracts"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	auditcontracts "github.com/HappyLadySauceM/Nagi/internal/audit/contracts"
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

// PromptProvider resolves the immutable prompt snapshot used by one semantic
// call. Implementations must fall back locally, never block on Langfuse.
// PromptProvider 解析一次语义调用使用的不可变 Prompt 快照。
// 实现必须在本地兜底，绝不等待 Langfuse。
type PromptProvider interface {
	Resolve(ctx context.Context, provider string) domain.Prompt
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
	Record(ctx context.Context, jobID int64, decision domain.FinalDecision, claim ...string) error
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

// RuntimeConfig is the per-call view of the editable settings. The decider
// reads it on every task so a saved change applies to the next decision
// without restarting the process.
// RuntimeConfig 是可编辑配置的单次调用视图。决策器在每个任务上读取它，
// 保存后的改动因此对下一次决策生效，无需重启进程。
type RuntimeConfig struct {
	Enabled      bool
	AIEnabled    bool
	HistoryLimit int
	Provider     string
	Model        string
	Thresholds   domain.Thresholds
}

// RuntimeConfigProvider reports the current runtime configuration; nil means
// the static options are authoritative.
// RuntimeConfigProvider 报告当前运行配置；nil 表示以静态 Options 为准。
type RuntimeConfigProvider func() RuntimeConfig

type runtimeConfigKey struct{}

// WithRuntimeConfig fixes the decision settings for one execution.
// WithRuntimeConfig 固定单次执行的决策配置。
func WithRuntimeConfig(ctx context.Context, cfg RuntimeConfig) context.Context {
	return context.WithValue(ctx, runtimeConfigKey{}, cfg)
}

func (d *Decider) runtimeFor(ctx context.Context) RuntimeConfig {
	if cfg, ok := ctx.Value(runtimeConfigKey{}).(RuntimeConfig); ok {
		return cfg
	}
	return d.runtime()
}

type TranscriptRecorder interface {
	SaveTranscript(context.Context, int64, string, string) error
}

type Options struct {
	Media       media.Transcriber
	Transcripts TranscriptRecorder
	Semantic    SemanticDecider
	Prompts     PromptProvider
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
	// Dynamic optionally overrides the static values above per task.
	// Dynamic 可选地按任务覆盖上面的静态取值。
	Dynamic RuntimeConfigProvider
	Logger  *slog.Logger
}

// Decider orchestrates rules, optional semantic judgment, reply policy and recording.
// Decider 编排规则、可选语义判定、回复策略与记录。
type Decider struct {
	media        media.Transcriber
	transcripts  TranscriptRecorder
	scopes       scopecontracts.Authorizer
	history      HistoryReader
	activity     ActivityReader
	semantic     SemanticDecider
	prompts      PromptProvider
	rules        RulesProvider
	recorder     DecisionRecorder
	executor     ReplyExecutor
	th           domain.Thresholds
	historyLimit int
	provider     string
	model        string
	dynamic      RuntimeConfigProvider
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
		media: opts.Media, transcripts: opts.Transcripts, scopes: opts.Scopes, history: opts.History, activity: opts.Activity,
		semantic: opts.Semantic, prompts: opts.Prompts,
		rules: opts.Rules, recorder: opts.Recorder, executor: opts.Executor,
		th: opts.Thresholds, historyLimit: opts.HistoryLimit,
		provider: opts.Provider, model: opts.Model, dynamic: opts.Dynamic, logger: opts.Logger,
	}, nil
}

// runtime resolves the current configuration, falling back to the static
// options when no dynamic provider is wired.
// runtime 解析当前配置；未接入动态提供者时回退到静态选项。
func (d *Decider) runtime() RuntimeConfig {
	if d.dynamic == nil {
		return RuntimeConfig{
			Enabled:      true,
			AIEnabled:    true,
			HistoryLimit: d.historyLimit,
			Provider:     d.provider,
			Model:        d.model,
			Thresholds:   d.th,
		}
	}
	cfg := d.dynamic()
	if cfg.HistoryLimit <= 0 {
		cfg.HistoryLimit = d.historyLimit
	}
	return cfg
}

// ProcessTask resolves the policy fresh, applies rules, optionally the semantic
// decider, records the final decision and dispatches replies.
// ProcessTask 即时解析策略，应用规则与可选语义判定，记录最终决策并分发回复。
func (d *Decider) ProcessTask(ctx context.Context, task domain.Task) error {
	runtime := d.runtimeFor(ctx)

	policy, err := d.scopes.Resolve(ctx, task.BotID, scopedomain.ScopeType(task.Kind), task.TargetID)
	if err != nil {
		// Retryable infrastructure failure: hand the error back to the worker.
		// 可重试的基础设施故障：把错误交还给工作器。
		return fmt.Errorf("intent: resolve policy: %w", err)
	}
	if d.media != nil && policy.PermissionGranted("media.voice.transcribe") && policy.Enabled && policy.StoreMessages && policy.ReplyMode != scopedomain.ModeObserveOnly {
		text, err := d.media.Transcribe(ctx, task.BotID, task.Segments, task.Message.Text)
		latest, resolveErr := d.scopes.Resolve(ctx, task.BotID, scopedomain.ScopeType(task.Kind), task.TargetID)
		if resolveErr != nil {
			return resolveErr
		}
		if !latest.Enabled || !latest.StoreMessages || latest.ReplyMode == scopedomain.ModeObserveOnly || !latest.PermissionGranted("media.voice.transcribe") {
			return nil
		}
		policy = latest
		if err == nil && text != task.Message.Text {
			if d.transcripts != nil {
				if err = d.transcripts.SaveTranscript(ctx, task.JobID, task.ClaimToken, text); err != nil {
					return err
				}
			}
			task.Message.Text = text
		} else if err != nil {
			if errors.Is(err, media.ErrArchiveFailed) {
				return err
			}
			task.Message.Text += " [语音未能转写]"
		}
	}
	if !runtime.Enabled {
		// The worker keeps consuming jobs while the pipeline is off so nothing
		// piles up in the queue; the decision itself is skipped.
		// 关闭流水线期间工作器仍消费任务，避免队列堆积；决策本身被跳过。
		d.logger.Debug("intent pipeline disabled; job skipped", "job_id", task.JobID)
		return nil
	}
	history, activity, historyDegraded, err := d.loadContext(ctx, task, policy)
	if err != nil {
		return err
	}
	prompt := domain.BuiltinPrompt(runtime.Provider)
	if d.prompts != nil {
		prompt = d.prompts.Resolve(ctx, runtime.Provider)
	}
	runID := uuid.NewString()
	in := domain.Input{Task: task, Recent: history, Activity: activity, Policy: policy, Now: time.Now(), Prompt: prompt}

	rule, rulesetVersion := d.applyRules(ctx, in)
	var semantic domain.SemanticAnswer
	var latency int64
	asked := rule.NeedSemantic && !rule.Blocked && d.semantic != nil
	if asked {
		semantic, latency = d.decideSemantic(ctx, in, runtime)
	}
	decision := d.applyPolicy(ctx, in, rule, semantic, latency, runtime)
	decision.RulesetVersion = rulesetVersion
	decision.RunID = runID
	decision.FlowVersion = domain.FlowVersion
	decision.Steps = decisionSteps(in, rule, semantic, decision, latency, asked, runtime.Thresholds, historyDegraded)
	decision.Snapshot = &domain.DecisionSnapshot{
		Version: domain.SnapshotVersion, FlowVersion: domain.FlowVersion, RunID: runID,
		Input:    domain.SnapshotInput{Task: in.Task, Recent: in.Recent, Activity: in.Activity, Policy: in.Policy, Now: in.Now},
		Semantic: semantic, Prompt: prompt, Rule: rule, RulesetVersion: rulesetVersion,
	}

	// The durable decision record remains the source used by evaluation. Mirror
	// its explainable, non-message facts into the run timeline before persisting
	// it, so a missing local audit write prevents a later reply from being sent.
	// 决策表仍是评估的事实来源。先把不含消息正文的可解释事实写入运行时间线，
	// 这样本地审计写入失败时不会继续到后续回复。
	auditEvent, err := auditcontracts.StartEvent(ctx, "intent.decision", map[string]any{
		"job_id":          task.JobID,
		"conversation_id": task.ConversationID,
		"config": map[string]any{
			"provider":      runtime.Provider,
			"model":         runtime.Model,
			"history_limit": runtime.HistoryLimit,
			"thresholds":    runtime.Thresholds,
		},
		"ruleset_version": rulesetVersion,
	})
	if err != nil {
		return err
	}
	if err := d.recorder.Record(ctx, task.JobID, decision, task.ClaimToken); err != nil {
		if finishErr := auditcontracts.FinishEvent(ctx, auditEvent, "failed", "decision_record_failed", map[string]any{"error": err.Error()}); finishErr != nil {
			return finishErr
		}
		return fmt.Errorf("intent: record decision: %w", err)
	}
	// Audit keeps only decision facts. Snapshot.Input contains message bodies,
	// so the audit projection deliberately omits the snapshot and raw steps.
	// 审计只保留决策事实；Snapshot.Input 含消息正文，因此审计投影不包含快照与原始步骤。
	auditDecision := map[string]any{
		"Decision":       string(decision.Decision),
		"ReasonCode":     decision.ReasonCode,
		"Provider":       decision.Provider,
		"Model":          decision.Model,
		"Scores":         decision.Scores,
		"LatencyMS":      decision.LatencyMS,
		"RulesetVersion": decision.RulesetVersion,
		"RunID":          decision.RunID,
		"FlowVersion":    decision.FlowVersion,
	}
	if err := auditcontracts.FinishEvent(ctx, auditEvent, "succeeded", "", map[string]any{"decision": auditDecision}); err != nil {
		return err
	}
	d.logger.Info("intent decided",
		"job_id", task.JobID, "decision", string(decision.Decision),
		"reason", decision.ReasonCode, "latency_ms", latency)

	// A disabled generation model records the decision but never sends: the
	// console explains that replies need both switches on.
	// 关闭生成模型时仍记录决策但不发送；控制台的说明是回复需要两个开关同时开启。
	if decision.Decision == domain.DecisionReply && d.executor != nil && runtime.AIEnabled {
		return d.executor.ExecuteReply(ctx, task, history)
	}
	return nil
}

// loadContext reads the authorized history and the rate-rule metadata inside
// one span; the span exists even when history reads are unauthorized.
// loadContext 在单个 span 内读取授权历史与频率规则元数据；
// 即使未授权读取历史，该 span 仍然存在。
func (d *Decider) loadContext(ctx context.Context, task domain.Task, policy scopedomain.Policy) ([]domain.Message, domain.BotActivity, bool, error) {
	ctx, span := tracer().Start(ctx, "nagi.intent.load_context")
	defer span.End()
	span.SetAttributes(attribute.Bool("nagi.history_allowed", policy.AllowHistoryRead))
	// History bodies stay out of the payload: they can be dozens of messages,
	// and the count is enough to explain the decision.
	// 历史正文不进入载荷：它可能包含几十条消息，条数已足以解释决策。
	historyLimit := d.runtimeFor(ctx).HistoryLimit
	observability.RecordObservation(span, "langfuse.observation.input", map[string]any{
		"conversation_id": task.ConversationID,
		"history_limit":   historyLimit,
		"history_allowed": policy.AllowHistoryRead,
	})

	var history []domain.Message
	degraded := false
	if policy.AllowHistoryRead && d.history != nil {
		var err error
		history, err = d.history.RecentIntentMessages(ctx, task.ConversationID, historyLimit)
		if err != nil {
			// Degrade to current-message-only instead of failing the job.
			// 降级为仅使用当前消息，而不是让任务失败。
			span.RecordError(err)
			span.SetAttributes(attribute.Bool("nagi.history_degraded", true))
			d.logger.Warn("load conversation history failed", "error", err)
			history = nil
			degraded = true
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
			return nil, domain.BotActivity{}, degraded, fmt.Errorf("intent: load bot activity: %w", err)
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
	return history, activity, degraded, nil
}

// decisionSteps turns the pipeline stages into persisted, explainable nodes.
// The order matches evaluation order and the fixed flow definition.
// decisionSteps 把流水线各阶段整理为可持久化的解释节点，
// 顺序与判定顺序及固定流程定义一致。
func decisionSteps(in domain.Input, rule domain.RuleResult,
	semantic domain.SemanticAnswer, decision domain.FinalDecision, latency int64, asked bool,
	th domain.Thresholds, historyDegraded bool,
) []domain.Step {
	policy := in.Policy
	policyBranch := "authorized"
	policySummary := "已通过作用域策略与频率闸门"
	policyReason := ""
	if rule.Blocked && isPolicyBlockReason(rule.ReasonCode) {
		policyBranch = "blocked"
		policySummary = domain.ReasonText(rule.ReasonCode)
		policyReason = rule.ReasonCode
	}
	steps := []domain.Step{{
		Index:       0,
		Phase:       domain.StepPolicy,
		NodeID:      domain.NodePolicy,
		NodeVersion: 1,
		Status:      domain.NodeStatusExecuted,
		Branch:      policyBranch,
		ReasonCode:  policyReason,
		Summary:     policySummary,
		Detail: map[string]any{
			"Source":             policy.Source,
			"PolicyVersion":      policy.Version,
			"TemplateVersion":    policy.TemplateVersion,
			"Enabled":            policy.Enabled,
			"ReplyMode":          string(policy.ReplyMode),
			"MinReplyIntervalMS": policy.MinReplyIntervalMS,
			"MaxRepliesPerHour":  policy.MaxRepliesPerHour,
			"ScopeType":          string(policy.ScopeType),
			"ScopeID":            policy.ScopeID,
		},
		Evidence: []domain.Evidence{{Kind: "policy", Text: "reply_mode=" + string(policy.ReplyMode)}},
	}, {
		Index:       1,
		Phase:       domain.StepContext,
		NodeID:      domain.NodeContext,
		NodeVersion: 1,
		Status:      statusForContext(historyDegraded),
		Branch:      contextBranch(historyDegraded, policy.AllowHistoryRead),
		Summary:     contextSummary(historyDegraded, len(in.Recent)),
		Detail: map[string]any{
			"AllowHistoryRead": policy.AllowHistoryRead,
			"AllowMemoryRead":  policy.AllowMemoryRead,
			"AllowMemoryWrite": policy.AllowMemoryWrite,
			"HistoryCount":     len(in.Recent),
			"MemoryCount":      0,
			"HistorySource":    historySource(policy.AllowHistoryRead),
			"Skipped":          false,
			"Degraded":         historyDegraded,
		},
		Evidence: []domain.Evidence{{Kind: "context", Text: "history_count=" + strconv.Itoa(len(in.Recent))}},
	}, {
		Index:       2,
		Phase:       domain.StepRules,
		NodeID:      domain.NodeRules,
		NodeVersion: 1,
		Status:      domain.NodeStatusExecuted,
		Branch:      rulesBranch(rule),
		ReasonCode:  rule.ReasonCode,
		Summary:     "内置规则与版本化规则求值完成",
		Detail: map[string]any{
			"BuiltinRules": rule.BuiltinRules,
			"Evaluations":  rule.Evaluations,
			"evaluations":  rule.Evaluations,
			"Matched":      rule.Matched,
			"Blocked":      rule.Blocked,
			"Explicit":     rule.Explicit,
			"NeedSemantic": rule.NeedSemantic,
			"AlwaysReply":  rule.AlwaysReply,
		},
		Evidence: []domain.Evidence{{Kind: "rules", Text: "need_semantic=" + boolText(rule.NeedSemantic)}},
	}}
	semanticStatus := domain.NodeStatusSkipped
	if asked && semantic.Available {
		semanticStatus = domain.NodeStatusExecuted
	} else if asked && !semantic.Available {
		semanticStatus = domain.NodeStatusDegraded
	}
	semanticDetail := map[string]any{
		"Called":               asked,
		"Available":            semantic.Available,
		"FailureCode":          semantic.FailureCode,
		"intent_category":      semantic.IntentCategory,
		"called":               asked,
		"available":            semantic.Available,
		"failure_code":         semantic.FailureCode,
		"Provider":             decision.Provider,
		"Model":                decision.Model,
		"PromptID":             in.Prompt.ID,
		"PromptVersion":        in.Prompt.Version,
		"PromptSource":         in.Prompt.Source,
		"PromptHash":           in.Prompt.Hash,
		"PromptFallbackReason": in.Prompt.FallbackReason,
		"Scores":               decision.Scores,
		"Thresholds":           map[string]float64{"Continuation": th.Continuation, "Interjection": th.Interjection, "AddressedOtherMax": th.AddressedOtherMax},
		"Comparisons":          semanticComparisons(semantic, th),
		"LatencyMS":            latency,
	}
	if !asked {
		semanticDetail["SkipReason"] = semanticSkipReason(rule)
	}
	steps = append(steps, domain.Step{
		Index:       3,
		Phase:       domain.StepSemantic,
		NodeID:      domain.NodeSemantic,
		NodeVersion: 1,
		Status:      semanticStatus,
		Branch:      semanticBranch(asked, semantic),
		ReasonCode:  semantic.FailureCode,
		Summary:     semanticSummary(asked, semantic),
		Detail:      semanticDetail,
		Evidence:    []domain.Evidence{{Kind: "semantic", Text: semanticEvidence(asked, semantic)}},
		DurationMS:  latency,
	})
	steps = append(steps, domain.Step{
		Index:       4,
		Phase:       domain.StepFinal,
		NodeID:      domain.NodeDecision,
		NodeVersion: 1,
		Status:      domain.NodeStatusExecuted,
		Branch:      string(decision.Decision),
		ReasonCode:  decision.ReasonCode,
		Summary:     domain.ReasonText(decision.ReasonCode),
		Detail: map[string]any{
			"Decision":   string(decision.Decision),
			"ReasonCode": decision.ReasonCode,
			"ReasonText": domain.ReasonText(decision.ReasonCode),
			"Thresholds": map[string]float64{"Continuation": th.Continuation, "Interjection": th.Interjection, "AddressedOtherMax": th.AddressedOtherMax},
			"thresholds": map[string]float64{"continuation": th.Continuation, "interjection": th.Interjection, "addressed_other_max": th.AddressedOtherMax},
		},
		Evidence: []domain.Evidence{{Kind: "decision", Text: "decision=" + string(decision.Decision)}},
	})
	return steps
}

func isPolicyBlockReason(reason string) bool {
	switch reason {
	case "policy_disabled", "observe_only", "cooldown", "rate_limited", "not_addressed":
		return true
	default:
		return false
	}
}

func statusForContext(degraded bool) string {
	if degraded {
		return domain.NodeStatusDegraded
	}
	return domain.NodeStatusExecuted
}

func contextBranch(degraded, allowed bool) string {
	if degraded {
		return "degraded"
	}
	if !allowed {
		return "history_denied"
	}
	return "history_read"
}

func rulesBranch(rule domain.RuleResult) string {
	if rule.Blocked {
		return "blocked"
	}
	if rule.Explicit {
		return "explicit"
	}
	if rule.AlwaysReply {
		return "always_reply"
	}
	if rule.NeedSemantic {
		return "semantic"
	}
	return "rule_direct"
}

func semanticBranch(asked bool, semantic domain.SemanticAnswer) string {
	if !asked {
		return "skipped"
	}
	if !semantic.Available {
		return "degraded"
	}
	return "available"
}

func contextSummary(degraded bool, count int) string {
	if degraded {
		return "历史读取失败，已降级为当前消息"
	}
	return "读取已授权上下文 " + strconv.Itoa(count) + " 条"
}

func historySource(allowed bool) string {
	if allowed {
		return "database"
	}
	return "none"
}

func semanticSummary(asked bool, semantic domain.SemanticAnswer) string {
	if !asked {
		return "未调用语义判定"
	}
	if !semantic.Available {
		return "语义不可用，已按规则降级"
	}
	return "语义服务返回可用结果"
}

func semanticEvidence(asked bool, semantic domain.SemanticAnswer) string {
	if !asked {
		return "semantic_skipped"
	}
	if !semantic.Available {
		return "semantic_unavailable"
	}
	return "intent=" + semantic.IntentCategory
}

func semanticComparisons(semantic domain.SemanticAnswer, th domain.Thresholds) []map[string]any {
	if !semantic.Available {
		return []map[string]any{}
	}
	return []map[string]any{
		{"Name": "directed_to_bot", "Value": semantic.DirectedToBot, "Operator": ">=", "Threshold": th.Continuation, "Satisfied": semantic.DirectedToBot >= th.Continuation},
		{"Name": "continues_bot_thread", "Value": semantic.ContinuesBotThread, "Operator": ">=", "Threshold": th.Continuation, "Satisfied": semantic.ContinuesBotThread >= th.Continuation},
		{"Name": "appropriate_to_join", "Value": semantic.AppropriateToJoin, "Operator": ">=", "Threshold": th.Interjection, "Satisfied": semantic.AppropriateToJoin >= th.Interjection},
		{"Name": "addressed_to_other", "Value": semantic.AddressedToOther, "Operator": ">=", "Threshold": th.AddressedOtherMax, "Satisfied": semantic.AddressedToOther >= th.AddressedOtherMax},
	}
}

func boolText(v bool) string {
	if v {
		return "true"
	}
	return "false"
}

func semanticSkipReason(rule domain.RuleResult) string {
	if rule.Blocked {
		return "rule_blocked"
	}
	if rule.AlwaysReply {
		return "always_reply"
	}
	if !rule.NeedSemantic {
		return "rules_sufficient"
	}
	return "semantic_unavailable"
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
func (d *Decider) decideSemantic(ctx context.Context, in domain.Input, runtime RuntimeConfig) (domain.SemanticAnswer, int64) {
	ctx, span := tracer().Start(ctx, "nagi.intent.jev")
	defer span.End()
	span.SetAttributes(
		attribute.String("nagi.provider", runtime.Provider),
		attribute.String("nagi.model", runtime.Model),
	)
	// The classified message is the semantic decider's input; body export obeys
	// the content gate.
	// 被分类的消息是语义判定的输入；正文导出遵循内容开关。
	observability.RecordObservation(span, "langfuse.observation.input", map[string]any{
		"provider": runtime.Provider,
		"model":    runtime.Model,
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
	semantic domain.SemanticAnswer, latency int64, runtime RuntimeConfig,
) domain.FinalDecision {
	_, span := tracer().Start(ctx, "nagi.intent.policy")
	defer span.End()
	decision := EvaluateReplyPolicy(in, rule, semantic, runtime.Thresholds)
	decision.Provider = runtime.Provider
	decision.Model = runtime.Model
	decision.LatencyMS = latency
	span.SetAttributes(
		attribute.String("nagi.decision", string(decision.Decision)),
		attribute.String("nagi.reason_code", decision.ReasonCode),
		attribute.String("nagi.provider", runtime.Provider),
		attribute.String("nagi.model", runtime.Model),
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
	runtime := d.runtimeFor(ctx)
	var semantic domain.SemanticAnswer
	var latency int64
	called := full && rule.NeedSemantic && d.semantic != nil && !rule.Blocked
	if called {
		semantic, latency = d.decideSemantic(ctx, in, runtime)
	}
	decision := d.applyPolicy(ctx, in, rule, semantic, latency, runtime)
	return decision, decisionSteps(in, rule, semantic, decision, latency, called, runtime.Thresholds, false)
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
	history, activity, _, err := d.loadContext(ctx, in.Task, in.Policy)
	if err != nil {
		return in, err
	}
	in.Recent = history
	in.Activity = activity
	return in, nil
}
