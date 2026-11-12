// Decision snapshots make policy recalculation and fixed-input replay possible
// without pretending that historical rows had data they never stored.
// 决策快照让策略重算和固定输入回放成为可能，同时不伪造历史行未保存的数据。
package domain

import (
	"errors"
	"time"

	scopedomain "github.com/HappyLadySauceM/Nagi/internal/scope/domain"
)

// ErrSnapshotNotFound reports a decision recorded before snapshots existed.
// ErrSnapshotNotFound 表示决策记录早于快照功能，没有可回放快照。
var ErrSnapshotNotFound = errors.New("intent: decision snapshot not found")

// Snapshot status values exposed to the console.
// 暴露给控制台的快照状态。
const (
	SnapshotStatusComplete = "complete"
	SnapshotStatusPartial  = "partial"
	SnapshotStatusMissing  = "missing"
	SnapshotStatusLegacy   = "legacy"

	SnapshotVersion = "intent-decision-snapshot-v1"
)

// SnapshotInput is the exact decision input needed by the replay paths.
// SnapshotInput 是回放路径所需的精确决策输入。
type SnapshotInput struct {
	Task     Task
	Recent   []Message
	Activity BotActivity
	Policy   scopedomain.Policy
	Now      time.Time
}

// DecisionSnapshot stores the original input, semantic answer and prompt
// version. MissingFields is non-empty when a legacy or partial row cannot be
// replayed faithfully.
// DecisionSnapshot 保存原始输入、语义回答与 Prompt 版本。
// 当历史或部分记录无法忠实回放时 MissingFields 非空。
type DecisionSnapshot struct {
	Version        string
	FlowVersion    string
	RunID          string
	Input          SnapshotInput
	Semantic       SemanticAnswer
	Prompt         Prompt
	Rule           RuleResult
	RulesetVersion string
	MissingFields  []string
}

// SnapshotStatus returns the explicit status of a snapshot.
// SnapshotStatus 返回快照的明确状态。
func (s *DecisionSnapshot) SnapshotStatus() string {
	if s == nil {
		return SnapshotStatusMissing
	}
	if len(s.MissingFields) > 0 {
		return SnapshotStatusPartial
	}
	return SnapshotStatusComplete
}
