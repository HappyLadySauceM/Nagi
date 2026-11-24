// Replay services for stored decisions and fixed-input model evaluation.
// 存量决策回放与固定输入模型评估服务。
package application

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"sync"
	"time"
	"unicode/utf8"

	scopedomain "github.com/HappyLadySauceM/Nagi/internal/scope/domain"
	"github.com/google/uuid"

	"github.com/HappyLadySauceM/Nagi/internal/intent/contracts"
	"github.com/HappyLadySauceM/Nagi/internal/intent/domain"
)

var (
	ErrSnapshotMissing = errors.New("intent: snapshot_missing")
	// ErrReplayConfirmationRequired rejects a model replay without a fresh
	// server-issued confirmation token.
	// ErrReplayConfirmationRequired 拒绝没有新鲜服务端确认令牌的模型回放。
	ErrReplayConfirmationRequired = errors.New("intent: replay confirmation required")
	// ErrReplayConfirmationExpired rejects a token past its bounded lifetime.
	// ErrReplayConfirmationExpired 拒绝超出有效期的令牌。
	ErrReplayConfirmationExpired = errors.New("intent: replay confirmation expired")
	// ErrReplayConfirmationStale rejects a token whose parameters no longer
	// match the current model, prompt or estimate inputs.
	// ErrReplayConfirmationStale 拒绝参数与当前模型、Prompt 或估算输入不再一致的令牌。
	ErrReplayConfirmationStale   = errors.New("intent: replay confirmation stale")
	ErrReplayEvaluationNotFound  = errors.New("intent: replay evaluation not found")
	ErrReplayEvaluationStoreDown = errors.New("intent: replay evaluation store unavailable")
)

// Replay confirmation tokens are short lived and single use.
// 回放确认令牌有效期短且只能使用一次。
const replayConfirmationTTL = 10 * time.Minute

type frozenBackgroundChecker interface {
	CheckFrozenBackground(context.Context, domain.Input) string
}

func (d *Decider) fixedBackground(ctx context.Context, in *domain.Input, snapshot domain.DecisionSnapshot) string {
	if !in.Policy.AllowHistoryRead && (len(snapshot.Input.Recent) > 0 || snapshot.Input.AwaitedRequest != nil || snapshot.Input.Topics != nil) {
		return "background_history_denied"
	}
	if snapshot.Input.Topics != nil && in.Topics == nil {
		return "background_topic_unavailable"
	}
	checker, ok := d.semantic.(frozenBackgroundChecker)
	if !ok {
		return ""
	}
	if snapshot.Semantic.BackgroundFailure != "" {
		return snapshot.Semantic.BackgroundFailure
	}
	in.FixedBackground = true
	in.FrozenBackground = snapshot.Semantic.BackgroundEvidence
	return checker.CheckFrozenBackground(ctx, *in)
}

func frozenSemanticNeeded(snapshot domain.DecisionSnapshot) bool {
	return shouldAskSemantic(domain.Input{
		Task: snapshot.Input.Task, BotName: snapshot.Input.BotName, CallIdentity: snapshot.Input.CallIdentity,
		Topics: snapshot.Input.Topics, Policy: snapshot.Input.Policy,
	}, snapshot.Rule)
}

func replayUsage(info *contracts.ReplayInfo, answer domain.SemanticAnswer) *contracts.ReplayInfo {
	info.ExternalRequests = answer.ExternalRequests
	info.RequestCountUnknown = answer.UnknownRequests > 0
	info.UnresolvedReservations = answer.UnknownRequests
	info.InputTokens = answer.Usage.InputTokens
	info.OutputTokens = answer.Usage.OutputTokens
	info.UsageResponses = answer.Usage.Responses
	info.InputUsageResponses = answer.Usage.InputResponses
	info.OutputUsageResponses = answer.Usage.OutputResponses
	for _, attempt := range answer.Attempts {
		row := contracts.ReplayAttempt{
			Outcome: attempt.Outcome, Memory: attempt.Memory, InputTokens: attempt.InputTokens, OutputTokens: attempt.OutputTokens,
			CostBasis: attempt.CostBasis, Currency: attempt.Currency, PartialWrite: attempt.PartialWrite, ReservationHeld: attempt.ReservationHeld,
		}
		if attempt.Budget != nil {
			budget := contracts.ReplayAttemptBudget(*attempt.Budget)
			row.Budget = &budget
		}
		info.Attempts = append(info.Attempts, row)
	}
	return info
}

func notComparableRun(decisionID int64, reason string, answer domain.SemanticAnswer) contracts.Run {
	return contracts.Run{DecisionID: decisionID, SnapshotStatus: domain.SnapshotStatusComplete, Summary: contracts.RunSummary{Decision: "not_comparable", ReasonCode: reason}, Replay: replayUsage(&contracts.ReplayInfo{Status: "not_comparable", ReasonCode: reason}, answer)}
}

// SnapshotReader loads the immutable snapshot of one decision.
// SnapshotReader 读取一条决策的不可变快照。
type SnapshotReader interface {
	Snapshot(ctx context.Context, decisionID int64) (domain.DecisionSnapshot, error)
}

// ReplayEvaluationRepository persists independent replay evaluation records.
// ReplayEvaluationRepository 持久化独立的回放评估记录。
type ReplayEvaluationRepository interface {
	Create(ctx context.Context, evaluation contracts.ReplayEvaluation) (contracts.ReplayEvaluation, error)
	Get(ctx context.Context, id int64) (contracts.ReplayEvaluation, error)
	List(ctx context.Context, query contracts.ReplayEvaluationQuery) ([]contracts.ReplayEvaluationSummary, error)
}

type replayConfirmation struct {
	decisionID  int64
	fingerprint string
	budget      contracts.ReplayBudgetPlan
	destination string
	expiresAt   time.Time
}

// ReplayService re-evaluates decisions without writing or delivering.
// ReplayService 在不写入决策、不投递消息的情况下重算决策。
type ReplayService struct {
	decider       *Decider
	records       contracts.DecisionReader
	snapshots     SnapshotReader
	evaluations   ReplayEvaluationRepository
	mu            sync.Mutex
	confirmations map[string]replayConfirmation
	budgetPlanner func(context.Context, contracts.ReplayParameters) contracts.ReplayBudgetPlan
}

func NewReplayService(decider *Decider, records contracts.DecisionReader, snapshots SnapshotReader, evaluations ...ReplayEvaluationRepository) *ReplayService {
	svc := &ReplayService{decider: decider, records: records, snapshots: snapshots, confirmations: make(map[string]replayConfirmation)}
	if len(evaluations) > 0 {
		svc.evaluations = evaluations[0]
	}
	return svc
}

func (s *ReplayService) WithReplayBudgetPlanner(planner func(context.Context, contracts.ReplayParameters) contracts.ReplayBudgetPlan) *ReplayService {
	s.budgetPlanner = planner
	return s
}

// Recalculate re-runs policy and rules from the stored semantic answer.
// It never calls the model and never writes a decision or outbound record.
// Recalculate 用已保存的语义回答重跑当前策略与规则；
// 绝不调用模型，也不写决策或投递记录。
func (s *ReplayService) Recalculate(ctx context.Context, decisionID int64) (contracts.Run, contracts.Run, bool, error) {
	if s == nil || s.decider == nil || s.records == nil || s.snapshots == nil {
		return contracts.Run{}, contracts.Run{}, false, fmt.Errorf("intent: replay unavailable")
	}
	beforeRecord, err := s.records.Get(ctx, decisionID)
	if err != nil {
		return contracts.Run{}, contracts.Run{}, false, err
	}
	snapshot, err := s.snapshots.Snapshot(ctx, decisionID)
	if err != nil {
		if errors.Is(err, domain.ErrSnapshotNotFound) {
			return contracts.Run{}, contracts.Run{}, false, ErrSnapshotMissing
		}
		return contracts.Run{}, contracts.Run{}, false, err
	}
	in, err := s.decider.replayInput(ctx, snapshot)
	if err != nil {
		return contracts.Run{}, contracts.Run{}, false, err
	}
	runtime := s.decider.runtimeFor(ctx)
	if !runtime.AIEnabled || runtime.TopicIdleSeconds <= 0 {
		in.Topics = nil
	}
	rule, version, _ := s.decider.applyRules(ctx, in)
	decision := s.decider.applyPolicy(ctx, in, rule, snapshot.Semantic, 0, runtime)
	decision.RulesetVersion = version
	decision.RunID = "recalc-" + uuid.NewString()
	decision.FlowVersion = domain.FlowVersion
	asked := rule.NeedSemantic && !rule.Blocked
	decision.Steps = decisionSteps(in, rule, snapshot.Semantic, decision, 0, asked, false)
	before := BuildRunFromRecord(beforeRecord, contracts.Delivery{})
	before.Prompt = promptInfoFromDomain(snapshot.Prompt)
	after := BuildRunFromDecision(decision, contracts.Delivery{})
	after.DecisionID = before.DecisionID
	after.JobID = before.JobID
	after.Prompt = promptInfoFromDomain(snapshot.Prompt)
	after.Replay = &contracts.ReplayInfo{Input: "snapshot", Semantic: "snapshot", Prompt: "snapshot", Policy: "current", Rules: "current", Persisted: false}
	return before, after, changed(before, after), nil
}

// ReplayModel calls the current semantic model with the fixed stored input and
// the stored rule state. It never writes or sends anything.
// ReplayModel 使用固定保存的输入与规则状态调用当前语义模型；不写入、不发送。
func (s *ReplayService) ReplayModel(ctx context.Context, decisionID int64) (contracts.Run, error) {
	return s.replayModel(ctx, decisionID, nil, nil)
}

type replaySemanticResult struct {
	input  domain.Input
	answer domain.SemanticAnswer
}

func (s *ReplayService) replayModel(ctx context.Context, decisionID int64, confirmed *contracts.ReplayParameters, capture *replaySemanticResult) (contracts.Run, error) {
	if s == nil || s.decider == nil || s.snapshots == nil {
		return contracts.Run{}, fmt.Errorf("intent: replay unavailable")
	}
	snapshot, err := s.snapshots.Snapshot(ctx, decisionID)
	if err != nil {
		if errors.Is(err, domain.ErrSnapshotNotFound) {
			return contracts.Run{}, ErrSnapshotMissing
		}
		return contracts.Run{}, err
	}
	if snapshotMissingField(snapshot.MissingFields, "rule") {
		return contracts.Run{}, ErrSnapshotMissing
	}
	in, err := s.decider.replayInput(ctx, snapshot)
	if err != nil {
		return contracts.Run{}, err
	}
	rule := replayRule(in, snapshot.Rule)
	runtime := s.decider.runtimeFor(ctx)
	semantic := initialCallAddressing(in, rule)
	semantic.FailureCode = "semantic_unavailable"
	latency := int64(0)
	asked := shouldAskSemantic(in, rule) && s.decider.semantic != nil
	if confirmed != nil && confirmed.BackgroundReason != "" {
		return notComparableRun(decisionID, confirmed.BackgroundReason, domain.SemanticAnswer{}), nil
	}
	if asked || (s.decider.semantic != nil && frozenSemanticNeeded(snapshot)) {
		if reason := s.decider.fixedBackground(ctx, &in, snapshot); reason != "" {
			return notComparableRun(decisionID, reason, domain.SemanticAnswer{}), nil
		}
	}
	if confirmed != nil && (runtime.Provider != confirmed.Provider || runtime.Model != confirmed.Model ||
		replayRequestCount(runtime.Provider, asked, in.Topics) != confirmed.EstimatedExternalRequests) {
		return contracts.Run{}, ErrReplayConfirmationStale
	}
	if asked {
		if confirmed != nil && confirmed.DestinationFingerprint != "" {
			route, ok := s.decider.semantic.(interface{ ReplayDestination(context.Context) string })
			if !ok || route.ReplayDestination(ctx) != confirmed.DestinationFingerprint {
				return notComparableRun(decisionID, "background_destination_changed", domain.SemanticAnswer{}), nil
			}
		}
		semantic, latency = s.decider.decideSemantic(ctx, in, runtime, rule)
		if semantic.BackgroundFailure != "" {
			return notComparableRun(decisionID, semantic.BackgroundFailure, semantic), nil
		}
		if !semantic.Available || semantic.FailureCode != "" {
			return notComparableRun(decisionID, "semantic_response_unusable", semantic), nil
		}
	}
	var run contracts.Run
	build := func() error {
		decision := s.decider.applyPolicy(ctx, in, rule, semantic, latency, runtime)
		decision.RulesetVersion = snapshot.RulesetVersion
		decision.RunID = "replay-" + uuid.NewString()
		decision.FlowVersion = domain.FlowVersion
		decision.Steps = decisionSteps(in, rule, semantic, decision, latency, asked, false)
		run = BuildRunFromDecision(decision, contracts.Delivery{})
		run.DecisionID = decisionID
		run.Prompt = promptInfoFromDomain(snapshot.Prompt)
		run.Replay = replayUsage(&contracts.ReplayInfo{Input: "snapshot", Semantic: "current", Prompt: "snapshot", Policy: "current", Rules: "snapshot", Persisted: false}, semantic)
		return nil
	}
	if guard, ok := s.decider.semantic.(semanticResultGuard); ok && asked {
		reason, err := guard.WithSemanticResult(ctx, in, semantic, build)
		if err != nil {
			return notComparableRun(decisionID, "semantic_response_unusable", semantic), nil
		}
		if reason != "" {
			return notComparableRun(decisionID, reason, semantic), nil
		}
		if capture != nil {
			*capture = replaySemanticResult{input: in, answer: semantic}
		}
	} else if err := build(); err != nil {
		return contracts.Run{}, err
	}
	return run, nil
}

// EstimateModelReplay returns the server-side model, sample count, external
// request count, token estimate and cost status before any paid model call.
// The confirmation token is bound to these parameters and expires.
// EstimateModelReplay 在任何付费模型调用前返回服务端计算的模型、样本数、
// 外部请求次数、token 估算与费用状态。确认令牌绑定这些参数并会过期。
func (s *ReplayService) EstimateModelReplay(ctx context.Context, decisionID int64) (contracts.ReplayCostEstimate, error) {
	params, err := s.modelReplayParameters(ctx, decisionID)
	if err != nil {
		return contracts.ReplayCostEstimate{}, err
	}
	if params.BackgroundReason != "" {
		return contracts.ReplayCostEstimate{DecisionID: decisionID, Provider: params.Provider, Model: params.Model, SampleCount: params.SampleCount, Status: "not_comparable", ReasonCode: params.BackgroundReason, Cost: replayCostForModel(0)}, nil
	}
	fingerprint := replayFingerprint(decisionID, params)
	token := newReplayToken()
	expires := time.Now().Add(replayConfirmationTTL)
	s.mu.Lock()
	if s.confirmations == nil {
		s.confirmations = make(map[string]replayConfirmation)
	}
	now := time.Now()
	for key, record := range s.confirmations {
		if now.After(record.expiresAt) {
			delete(s.confirmations, key)
		}
	}
	s.confirmations[token] = replayConfirmation{decisionID: decisionID, fingerprint: fingerprint, budget: params.Budget, destination: params.DestinationFingerprint, expiresAt: expires}
	s.mu.Unlock()
	return contracts.ReplayCostEstimate{
		DecisionID:                decisionID,
		Provider:                  params.Provider,
		Model:                     params.Model,
		SampleCount:               params.SampleCount,
		EstimatedExternalRequests: params.EstimatedExternalRequests,
		Budget:                    &params.Budget,
		TokenEstimate:             params.TokenEstimate,
		Cost:                      replayCostForModel(params.EstimatedExternalRequests),
		ConfirmationToken:         token,
		ExpiresAt:                 expires,
		ParametersFingerprint:     fingerprint,
	}, nil
}

// ReplayModelConfirmed executes a model replay only after validating a fresh
// confirmation token bound to the current parameters. The token is consumed
// before the external call so a retry cannot reuse it.
// ReplayModelConfirmed 仅在验证绑定当前参数的新鲜确认令牌后执行模型回放。
// 令牌在外部调用前消费，重试不能复用。
func (s *ReplayService) ReplayModelConfirmed(ctx context.Context, decisionID int64, token, name, note string) (contracts.ReplayEvaluation, error) {
	if token == "" {
		return contracts.ReplayEvaluation{}, ErrReplayConfirmationRequired
	}
	if s.records == nil || s.evaluations == nil {
		return contracts.ReplayEvaluation{}, ErrReplayEvaluationStoreDown
	}
	params, err := s.consumeConfirmation(ctx, decisionID, token)
	if err != nil {
		return contracts.ReplayEvaluation{}, err
	}
	record, err := s.records.Get(ctx, decisionID)
	if err != nil {
		return contracts.ReplayEvaluation{}, err
	}
	snapshot, err := s.snapshots.Snapshot(ctx, decisionID)
	if err != nil {
		return contracts.ReplayEvaluation{}, err
	}
	budget := &replayBudget{plan: params.Budget, destination: params.DestinationFingerprint}
	var result replaySemanticResult
	run, err := s.replayModel(context.WithValue(ctx, replayBudgetKey{}, budget), decisionID, &params, &result)
	if err != nil {
		return contracts.ReplayEvaluation{}, err
	}
	persistCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	save := func(run contracts.Run) (contracts.ReplayEvaluation, error) {
		if run.Replay != nil {
			balance := budget.summary()
			run.Replay.Budget = &balance
		}
		before := BuildRunFromRecord(record, contracts.Delivery{})
		before.Prompt = promptInfoFromDomain(snapshot.Prompt)
		run.JobID = before.JobID
		evaluation := contracts.ReplayEvaluation{
			DecisionID: decisionID, BotID: record.BotID, Mode: contracts.ReplayModeModel, Name: name, Note: note,
			Changed: (run.Replay == nil || run.Replay.Status != "not_comparable") && changed(before, run), Before: before, After: run,
			Parameters: params, Cost: replayCostForModel(params.EstimatedExternalRequests),
			Reproduction: reproductionFrom(run, snapshot), CreatedAt: time.Now(),
		}
		if run.Replay != nil {
			evaluation.Cost = replayCostForModel(run.Replay.ExternalRequests + run.Replay.UnresolvedReservations)
		}
		return s.evaluations.Create(persistCtx, evaluation)
	}
	if guard, ok := s.decider.semantic.(semanticResultGuard); ok && result.answer.Available {
		var evaluation contracts.ReplayEvaluation
		reason, err := guard.WithSemanticResult(persistCtx, result.input, result.answer, func() error {
			var saveErr error
			evaluation, saveErr = save(run)
			return saveErr
		})
		if err != nil {
			return contracts.ReplayEvaluation{}, err
		}
		if reason == "" {
			return evaluation, nil
		}
		run = notComparableRun(decisionID, reason, result.answer)
	}
	return save(run)
}

// SaveRecalculateEvaluation runs the free local recalculation and persists an
// independent evaluation record. It never calls a model or sends a message.
// SaveRecalculateEvaluation 执行免费的本地重算并保存独立评估记录；
// 绝不调用模型或发送消息。
func (s *ReplayService) SaveRecalculateEvaluation(ctx context.Context, decisionID int64, name, note string) (contracts.ReplayEvaluation, error) {
	if s.evaluations == nil || s.records == nil {
		return contracts.ReplayEvaluation{}, ErrReplayEvaluationStoreDown
	}
	before, after, changedResult, err := s.Recalculate(ctx, decisionID)
	if err != nil {
		return contracts.ReplayEvaluation{}, err
	}
	record, err := s.records.Get(ctx, decisionID)
	if err != nil {
		return contracts.ReplayEvaluation{}, err
	}
	snapshot, err := s.snapshots.Snapshot(ctx, decisionID)
	if err != nil {
		return contracts.ReplayEvaluation{}, err
	}
	runtime := s.decider.runtimeFor(ctx)
	params := contracts.ReplayParameters{
		Provider: runtime.Provider, Model: runtime.Model,
		PromptID: snapshot.Prompt.ID, PromptVersion: snapshot.Prompt.Version, PromptHash: snapshot.Prompt.Hash,
		RulesetVersion: after.RulesetVersion, SampleCount: 1,
	}
	evaluation := contracts.ReplayEvaluation{
		DecisionID: decisionID, BotID: record.BotID, Mode: contracts.ReplayModeRecalculate, Name: name, Note: note,
		Changed: changedResult, Before: before, After: after,
		Parameters: params, Cost: localReplayCost(),
		Reproduction: reproductionFrom(after, snapshot), CreatedAt: time.Now(),
	}
	return s.evaluations.Create(ctx, evaluation)
}

// ListEvaluations returns independently saved replay results.
// ListEvaluations 返回独立保存的回放结果。
func (s *ReplayService) ListEvaluations(ctx context.Context, query contracts.ReplayEvaluationQuery) ([]contracts.ReplayEvaluationSummary, error) {
	if s == nil || s.evaluations == nil {
		return nil, ErrReplayEvaluationStoreDown
	}
	return s.evaluations.List(ctx, query)
}

// GetEvaluation returns one saved replay result.
// GetEvaluation 返回一条已保存的回放结果。
func (s *ReplayService) GetEvaluation(ctx context.Context, id int64) (contracts.ReplayEvaluation, error) {
	if s == nil || s.evaluations == nil {
		return contracts.ReplayEvaluation{}, ErrReplayEvaluationStoreDown
	}
	return s.evaluations.Get(ctx, id)
}

// CompareEvaluation compares one saved evaluation with its original run or
// with another saved evaluation.
// CompareEvaluation 将一条已保存评估与原始运行或另一条已保存评估对比。
func (s *ReplayService) CompareEvaluation(ctx context.Context, evaluationID, againstEvaluationID int64) (contracts.ReplayComparison, error) {
	if s == nil || s.evaluations == nil {
		return contracts.ReplayComparison{}, ErrReplayEvaluationStoreDown
	}
	evaluation, err := s.evaluations.Get(ctx, evaluationID)
	if err != nil {
		return contracts.ReplayComparison{}, err
	}
	original := evaluation.Before
	replay := evaluation.After
	against := "original"
	changedResult := evaluation.Changed
	if againstEvaluationID > 0 {
		other, err := s.evaluations.Get(ctx, againstEvaluationID)
		if err != nil {
			return contracts.ReplayComparison{}, err
		}
		original = other.After
		against = fmt.Sprintf("evaluation:%d", againstEvaluationID)
		changedResult = (original.Replay == nil || original.Replay.Status != "not_comparable") && (replay.Replay == nil || replay.Replay.Status != "not_comparable") && runsDiffer(original, replay)
	}
	var differences []contracts.ReplayDifference
	if original.Summary.Decision == "not_comparable" || replay.Summary.Decision == "not_comparable" {
		changedResult = false
	} else {
		differences = diffRuns(original, replay)
	}
	return contracts.ReplayComparison{
		EvaluationID: evaluationID, DecisionID: evaluation.DecisionID, Against: against,
		Changed: changedResult, Original: original, Replay: replay,
		Differences: differences, Reproduction: evaluation.Reproduction,
	}, nil
}

func (s *ReplayService) modelReplayParameters(ctx context.Context, decisionID int64) (contracts.ReplayParameters, error) {
	if s == nil || s.decider == nil || s.snapshots == nil {
		return contracts.ReplayParameters{}, fmt.Errorf("intent: replay unavailable")
	}
	snapshot, err := s.snapshots.Snapshot(ctx, decisionID)
	if err != nil {
		if errors.Is(err, domain.ErrSnapshotNotFound) {
			return contracts.ReplayParameters{}, ErrSnapshotMissing
		}
		return contracts.ReplayParameters{}, err
	}
	if snapshotMissingField(snapshot.MissingFields, "rule") {
		return contracts.ReplayParameters{}, ErrSnapshotMissing
	}
	runtime := s.decider.runtimeFor(ctx)
	requests := 0
	reason, backgroundHash := "", ""
	backgroundRunes := 0
	if snapshot.Rule.NeedSemantic && !snapshot.Rule.Blocked && s.decider.semantic != nil {
		in, err := s.decider.replayInput(ctx, snapshot)
		if err != nil {
			return contracts.ReplayParameters{}, err
		}
		asked := shouldAskSemantic(in, replayRule(in, snapshot.Rule))
		if asked || frozenSemanticNeeded(snapshot) {
			reason = s.decider.fixedBackground(ctx, &in, snapshot)
			if reason == "" && in.FixedBackground {
				raw, _ := json.Marshal(in.FrozenBackground)
				backgroundHash = domain.PromptTextHash(string(raw))
				backgroundRunes = len(in.FrozenBackground.RoleLayers)*1000 + len(in.FrozenBackground.Memories)*240
			}
		}
		requests = replayRequestCount(runtime.Provider, asked && reason == "", in.Topics)
	}
	params := contracts.ReplayParameters{
		Provider: runtime.Provider, Model: runtime.Model,
		PromptID: snapshot.Prompt.ID, PromptVersion: snapshot.Prompt.Version, PromptHash: snapshot.Prompt.Hash,
		RulesetVersion: snapshot.RulesetVersion, SampleCount: 1,
		EstimatedExternalRequests: requests, TokenEstimate: estimateReplayTokens(snapshot, requests, backgroundRunes),
		BackgroundReason: reason, BackgroundFingerprint: backgroundHash,
	}
	if requests > 0 {
		if route, ok := s.decider.semantic.(interface{ ReplayDestination(context.Context) string }); ok {
			params.DestinationFingerprint = route.ReplayDestination(ctx)
			if params.DestinationFingerprint == "" {
				params.BackgroundReason = "background_destination_unavailable"
			}
		}
	}
	params.Budget = contracts.ReplayBudgetPlan{RequestLimit: requests, Destination: params.DestinationFingerprint}
	if s.budgetPlanner != nil {
		params.Budget = s.budgetPlanner(ctx, params)
	}
	if params.Budget.RequestLimit > requests {
		params.Budget.RequestLimit = requests
	}
	if params.BackgroundReason == "" && requests > 0 {
		if params.Budget.RequestLimit == 0 {
			params.BackgroundReason = "semantic_replay_budget_exhausted"
		}
		if plan := (&replayBudget{plan: params.Budget, destination: params.DestinationFingerprint}); params.BackgroundReason == "" {
			params.BackgroundReason = plan.validationReason()
		}
	}
	return params, nil
}

func (s *ReplayService) consumeConfirmation(ctx context.Context, decisionID int64, token string) (contracts.ReplayParameters, error) {
	if token == "" {
		return contracts.ReplayParameters{}, ErrReplayConfirmationRequired
	}
	s.mu.Lock()
	record, ok := s.confirmations[token]
	if ok {
		delete(s.confirmations, token)
	}
	s.mu.Unlock()
	if !ok || record.decisionID != decisionID {
		return contracts.ReplayParameters{}, ErrReplayConfirmationRequired
	}
	if time.Now().After(record.expiresAt) {
		return contracts.ReplayParameters{}, ErrReplayConfirmationExpired
	}
	params, err := s.modelReplayParameters(ctx, decisionID)
	if err != nil {
		return contracts.ReplayParameters{}, err
	}
	if params.BackgroundReason != "" {
		params.Budget = record.budget
		params.DestinationFingerprint = record.destination
		return params, nil
	}
	if replayFingerprint(decisionID, params) != record.fingerprint {
		return contracts.ReplayParameters{}, ErrReplayConfirmationStale
	}
	return params, nil
}

func replayRule(in domain.Input, frozen domain.RuleResult) domain.RuleResult {
	in, _ = topicRuleInput(in)
	if current := EvaluateRules(in); current.Blocked {
		frozen.Blocked = true
		frozen.ReasonCode = current.ReasonCode
	}
	return frozen
}

func replayRequestCount(provider string, asked bool, topics *domain.TopicContext) int {
	if !asked {
		return 0
	}
	if provider == domain.PromptProviderOpenAI {
		if topics != nil && len(topics.Candidates) > 0 {
			return 6
		}
		return 5
	}
	return 1
}

func estimateReplayTokens(snapshot domain.DecisionSnapshot, requests int, backgroundRunes int) contracts.ReplayTokenEstimate {
	if requests <= 0 {
		return contracts.ReplayTokenEstimate{Method: "no_external_call"}
	}
	input, _ := json.Marshal(snapshot.Input)
	prompt, _ := json.Marshal(snapshot.Prompt)
	runes := utf8.RuneCount(input) + utf8.RuneCount(prompt) + backgroundRunes
	inputTokens := (runes + 3) / 4
	if inputTokens < 1 {
		inputTokens = 1
	}
	inputTokens *= requests
	outputTokens := 20 * requests
	return contracts.ReplayTokenEstimate{InputTokens: inputTokens, OutputTokens: outputTokens, TotalTokens: inputTokens + outputTokens, Method: "rough_runes_div_4"}
}

func localReplayCost() contracts.ReplayCost {
	return contracts.ReplayCost{MayIncur: false, Known: true, Text: "本地重算不调用外部模型，不产生模型费用"}
}

func replayCostForModel(requests int) contracts.ReplayCost {
	if requests <= 0 {
		return contracts.ReplayCost{MayIncur: false, Known: true, Text: "本次回放不需要语义调用，不产生外部模型费用"}
	}
	return contracts.ReplayCost{MayIncur: true, Known: false, Text: "费用未知：未配置模型价格信息"}
}

func replayFingerprint(decisionID int64, params contracts.ReplayParameters) string {
	raw, _ := json.Marshal(struct {
		DecisionID int64
		Params     contracts.ReplayParameters
	}{decisionID, params})
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

func newReplayToken() string {
	var buf [32]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return uuid.NewString()
	}
	return hex.EncodeToString(buf[:])
}

func promptInfoFromDomain(prompt domain.Prompt) contracts.PromptInfo {
	finalHash := prompt.FinalHash
	if finalHash == "" {
		finalHash = prompt.Hash
	}
	return contracts.PromptInfo{
		ID: prompt.ID, Kind: prompt.Kind, Name: prompt.Name, Version: prompt.Version,
		Source: prompt.Source, SourceRef: prompt.SourceRef, Hash: prompt.Hash,
		FinalHash: finalHash, RenderedHash: prompt.RenderedHash, Content: domain.PromptContentJSON(prompt),
		TargetType: prompt.TargetType, TargetID: prompt.TargetID, ConversationID: prompt.ConversationID, Layers: prompt.Layers,
	}
}

func reproductionFrom(run contracts.Run, snapshot domain.DecisionSnapshot) contracts.ReplayReproduction {
	reasons := make([]string, 0, 4)
	if run.Replay != nil && run.Replay.Status == "not_comparable" {
		return contracts.ReplayReproduction{Reasons: []string{run.Replay.ReasonCode}}
	}
	if run.Replay != nil {
		add := func(field, source string) {
			if source != "" && source != "snapshot" {
				reasons = append(reasons, field+"_current")
			}
		}
		add("input", run.Replay.Input)
		add("semantic", run.Replay.Semantic)
		add("prompt", run.Replay.Prompt)
		add("policy", run.Replay.Policy)
		add("rules", run.Replay.Rules)
	}
	if status := snapshot.SnapshotStatus(); status != domain.SnapshotStatusComplete {
		reasons = append(reasons, "snapshot_"+status)
	}
	return contracts.ReplayReproduction{FullyReproduced: len(reasons) == 0, Reasons: reasons}
}

func runsDiffer(before, after contracts.Run) bool {
	oldTopic, newTopic := replayTopic(before), replayTopic(after)
	return before.Summary.Decision != after.Summary.Decision || before.Summary.ReasonCode != after.Summary.ReasonCode ||
		oldTopic.ID != newTopic.ID || oldTopic.Action != newTopic.Action || oldTopic.CooldownBypassed != newTopic.CooldownBypassed
}

func replayTopic(run contracts.Run) domain.TopicChange {
	for _, node := range run.Nodes {
		if node.NodeID == domain.NodeDecision {
			raw, _ := json.Marshal(node.Detail["Topic"])
			var result domain.TopicChange
			_ = json.Unmarshal(raw, &result)
			return result
		}
	}
	return domain.TopicChange{}
}

func diffRuns(before, after contracts.Run) []contracts.ReplayDifference {
	out := make([]contracts.ReplayDifference, 0)
	add := func(field, oldValue, newValue string) {
		if oldValue != newValue {
			out = append(out, contracts.ReplayDifference{Field: field, Before: oldValue, After: newValue})
		}
	}
	add("Decision", string(before.Summary.Decision), string(after.Summary.Decision))
	add("ReasonCode", before.Summary.ReasonCode, after.Summary.ReasonCode)
	add("Provider", before.Provider, after.Provider)
	add("Model", before.Model, after.Model)
	add("RulesetVersion", before.RulesetVersion, after.RulesetVersion)
	add("Prompt", promptLabel(before.Prompt), promptLabel(after.Prompt))
	add("PromptHash", before.Prompt.Hash, after.Prompt.Hash)
	oldTopic, newTopic := replayTopic(before), replayTopic(after)
	add("TopicID", strconv.FormatInt(oldTopic.ID, 10), strconv.FormatInt(newTopic.ID, 10))
	add("TopicAction", oldTopic.Action, newTopic.Action)
	add("TopicCooldownBypassed", strconv.FormatBool(oldTopic.CooldownBypassed), strconv.FormatBool(newTopic.CooldownBypassed))
	return out
}

func promptLabel(prompt contracts.PromptInfo) string {
	if prompt.ID == "" {
		return ""
	}
	if prompt.Version > 0 {
		return fmt.Sprintf("%s v%d", prompt.ID, prompt.Version)
	}
	return prompt.ID
}

func (d *Decider) replayInput(ctx context.Context, snapshot domain.DecisionSnapshot) (domain.Input, error) {
	task := snapshot.Input.Task
	policy, err := d.scopes.Resolve(ctx, task.BotID, scopedomain.ScopeType(task.Kind), task.TargetID)
	if err != nil {
		return domain.Input{}, err
	}
	in := domain.Input{
		Task:                 snapshot.Input.Task,
		BotName:              snapshot.Input.BotName,
		CallIdentity:         snapshot.Input.CallIdentity,
		Recent:               snapshot.Input.Recent,
		AwaitedRequest:       snapshot.Input.AwaitedRequest,
		Topics:               snapshot.Input.Topics,
		HistoryWindowSkipped: snapshot.Input.HistoryWindowSkipped,
		Activity:             snapshot.Input.Activity,
		Policy:               policy,
		Now:                  snapshot.Input.Now,
		Prompt:               snapshot.Prompt,
	}
	if !policy.AllowHistoryRead {
		in.Recent, in.AwaitedRequest = nil, nil
	}
	if !allowsTopicContinuation(in) {
		in.Topics = nil
	}
	return in, nil
}

func snapshotMissingField(fields []string, name string) bool {
	for _, field := range fields {
		if field == name {
			return true
		}
	}
	return false
}

func changed(before, after contracts.Run) bool {
	return runsDiffer(before, after)
}
