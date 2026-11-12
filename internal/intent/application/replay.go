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
	"sync"
	"time"
	"unicode/utf8"

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
}

func NewReplayService(decider *Decider, records contracts.DecisionReader, snapshots SnapshotReader, evaluations ...ReplayEvaluationRepository) *ReplayService {
	svc := &ReplayService{decider: decider, records: records, snapshots: snapshots, confirmations: make(map[string]replayConfirmation)}
	if len(evaluations) > 0 {
		svc.evaluations = evaluations[0]
	}
	return svc
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
	rule, version := s.decider.applyRules(ctx, in)
	runtime := s.decider.runtimeFor(ctx)
	decision := s.decider.applyPolicy(ctx, in, rule, snapshot.Semantic, 0, runtime)
	decision.RulesetVersion = version
	decision.RunID = "recalc-" + uuid.NewString()
	decision.FlowVersion = domain.FlowVersion
	asked := rule.NeedSemantic && !rule.Blocked
	decision.Steps = decisionSteps(in, rule, snapshot.Semantic, decision, 0, asked, runtime.Thresholds, false)
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
	rule := snapshot.Rule
	runtime := s.decider.runtimeFor(ctx)
	semantic := domain.SemanticAnswer{Available: false, FailureCode: "semantic_unavailable"}
	latency := int64(0)
	asked := rule.NeedSemantic && !rule.Blocked && s.decider.semantic != nil
	if asked {
		semantic, latency = s.decider.decideSemantic(ctx, in, runtime)
	}
	decision := s.decider.applyPolicy(ctx, in, rule, semantic, latency, runtime)
	decision.RulesetVersion = snapshot.RulesetVersion
	decision.RunID = "replay-" + uuid.NewString()
	decision.FlowVersion = domain.FlowVersion
	decision.Steps = decisionSteps(in, rule, semantic, decision, latency, asked, runtime.Thresholds, false)
	run := BuildRunFromDecision(decision, contracts.Delivery{})
	run.DecisionID = decisionID
	run.Prompt = promptInfoFromDomain(snapshot.Prompt)
	run.Replay = &contracts.ReplayInfo{Input: "snapshot", Semantic: "current", Prompt: "snapshot", Policy: "current", Rules: "snapshot", Persisted: false}
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
	s.confirmations[token] = replayConfirmation{decisionID: decisionID, fingerprint: fingerprint, expiresAt: expires}
	s.mu.Unlock()
	return contracts.ReplayCostEstimate{
		DecisionID:                decisionID,
		Provider:                  params.Provider,
		Model:                     params.Model,
		SampleCount:               params.SampleCount,
		EstimatedExternalRequests: params.EstimatedExternalRequests,
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
	run, err := s.ReplayModel(ctx, decisionID)
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
	before := BuildRunFromRecord(record, contracts.Delivery{})
	before.Prompt = promptInfoFromDomain(snapshot.Prompt)
	run.JobID = before.JobID
	evaluation := contracts.ReplayEvaluation{
		DecisionID: decisionID, BotID: record.BotID, Mode: contracts.ReplayModeModel, Name: name, Note: note,
		Changed: changed(before, run), Before: before, After: run,
		Parameters: params, Cost: replayCostForModel(params.EstimatedExternalRequests),
		Reproduction: reproductionFrom(run, snapshot), CreatedAt: time.Now(),
	}
	return s.evaluations.Create(ctx, evaluation)
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
		changedResult = runsDiffer(original, replay)
	}
	return contracts.ReplayComparison{
		EvaluationID: evaluationID, DecisionID: evaluation.DecisionID, Against: against,
		Changed: changedResult, Original: original, Replay: replay,
		Differences: diffRuns(original, replay), Reproduction: evaluation.Reproduction,
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
	if snapshot.Rule.NeedSemantic && !snapshot.Rule.Blocked && s.decider.semantic != nil {
		requests = replayExternalRequests(runtime.Provider)
	}
	return contracts.ReplayParameters{
		Provider: runtime.Provider, Model: runtime.Model,
		PromptID: snapshot.Prompt.ID, PromptVersion: snapshot.Prompt.Version, PromptHash: snapshot.Prompt.Hash,
		RulesetVersion: snapshot.RulesetVersion, SampleCount: 1,
		EstimatedExternalRequests: requests, TokenEstimate: estimateReplayTokens(snapshot, requests),
	}, nil
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
	if replayFingerprint(decisionID, params) != record.fingerprint {
		return contracts.ReplayParameters{}, ErrReplayConfirmationStale
	}
	return params, nil
}

func replayExternalRequests(provider string) int {
	if provider == domain.PromptProviderOpenAI {
		return 5
	}
	return 1
}

func estimateReplayTokens(snapshot domain.DecisionSnapshot, requests int) contracts.ReplayTokenEstimate {
	if requests <= 0 {
		return contracts.ReplayTokenEstimate{Method: "no_external_call"}
	}
	input, _ := json.Marshal(snapshot.Input)
	prompt, _ := json.Marshal(snapshot.Prompt)
	runes := utf8.RuneCount(input) + utf8.RuneCount(prompt)
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
	return contracts.PromptInfo{
		ID: prompt.ID, Name: prompt.Name, Version: prompt.Version,
		Source: prompt.Source, SourceRef: prompt.SourceRef, Hash: prompt.Hash,
	}
}

func reproductionFrom(run contracts.Run, snapshot domain.DecisionSnapshot) contracts.ReplayReproduction {
	reasons := make([]string, 0, 4)
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
	return before.Summary.Decision != after.Summary.Decision || before.Summary.ReasonCode != after.Summary.ReasonCode
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
	policy, err := d.scopes.Resolve(ctx, snapshot.Input.Task.BotID, snapshot.Input.Policy.ScopeType, snapshot.Input.Policy.ScopeID)
	if err != nil {
		return domain.Input{}, err
	}
	return domain.Input{
		Task:     snapshot.Input.Task,
		Recent:   snapshot.Input.Recent,
		Activity: snapshot.Input.Activity,
		Policy:   policy,
		Now:      snapshot.Input.Now,
		Prompt:   snapshot.Prompt,
	}, nil
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
	return before.Summary.Decision != after.Summary.Decision || before.Summary.ReasonCode != after.Summary.ReasonCode
}
