// Replay services for stored decisions and fixed-input model evaluation.
// 存量决策回放与固定输入模型评估服务。
package application

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"

	"github.com/HappyLadySauceM/Nagi/internal/intent/contracts"
	"github.com/HappyLadySauceM/Nagi/internal/intent/domain"
)

var (
	ErrSnapshotMissing = errors.New("intent: snapshot_missing")
)

// SnapshotReader loads the immutable snapshot of one decision.
// SnapshotReader 读取一条决策的不可变快照。
type SnapshotReader interface {
	Snapshot(ctx context.Context, decisionID int64) (domain.DecisionSnapshot, error)
}

// ReplayService re-evaluates decisions without writing or delivering.
// ReplayService 在不写入、不投递的情况下重算决策。
type ReplayService struct {
	decider   *Decider
	records   contracts.DecisionReader
	snapshots SnapshotReader
}

func NewReplayService(decider *Decider, records contracts.DecisionReader, snapshots SnapshotReader) *ReplayService {
	return &ReplayService{decider: decider, records: records, snapshots: snapshots}
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
	after := BuildRunFromDecision(decision, contracts.Delivery{})
	after.DecisionID = before.DecisionID
	after.JobID = before.JobID
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
	run.Replay = &contracts.ReplayInfo{Input: "snapshot", Semantic: "current", Prompt: "snapshot", Policy: "current", Rules: "snapshot", Persisted: false}
	return run, nil
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
