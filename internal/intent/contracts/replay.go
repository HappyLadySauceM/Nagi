// Replay evaluation contracts shared by the application, repository and admin API.
// 回放评估在应用层、仓储与管理 API 之间共享的契约。
package contracts

import "time"

// Replay evaluation modes.
// 回放评估模式。
const (
	ReplayModeRecalculate = "recalculate"
	ReplayModeModel       = "model"
)

// ReplayTokenEstimate is a deliberately rough token estimate. It is not a
// billing promise; the provider invoice remains authoritative.
// ReplayTokenEstimate 是刻意粗略的 token 估算，不构成计费承诺，以提供方账单为准。
type ReplayTokenEstimate struct {
	InputTokens  int
	OutputTokens int
	TotalTokens  int
	Method       string
}

// ReplayCost reports whether a replay may incur external model cost. Known is
// false when no price information is configured, and Text then says so.
// ReplayCost 报告回放是否可能产生外部模型费用。未配置价格信息时 Known 为 false，
// Text 会明确写“费用未知”。
type ReplayCost struct {
	MayIncur bool
	Known    bool
	Amount   *float64
	Currency string
	Text     string
}

// ReplayCostEstimate is the server-side confirmation payload for one model
// replay. ConfirmationToken is bound to ParametersFingerprint and expires.
// ReplayCostEstimate 是一次模型回放的服务端确认载荷。
// ConfirmationToken 绑定 ParametersFingerprint 并会过期。
type ReplayCostEstimate struct {
	DecisionID                int64
	Provider                  string
	Model                     string
	SampleCount               int
	EstimatedExternalRequests int
	TokenEstimate             ReplayTokenEstimate
	Cost                      ReplayCost
	ConfirmationToken         string
	ExpiresAt                 time.Time
	ParametersFingerprint     string
}

// ReplayParameters records the exact parameters used by one saved evaluation.
// ReplayParameters 记录一次已保存评估使用的精确参数。
type ReplayParameters struct {
	Provider                  string
	Model                     string
	PromptID                  string
	PromptVersion             int
	PromptHash                string
	RulesetVersion            string
	SampleCount               int
	EstimatedExternalRequests int
	TokenEstimate             ReplayTokenEstimate
}

// ReplayReproduction marks whether a replay fully reproduced the original run.
// Any use of current configuration or a partial snapshot makes it false.
// ReplayReproduction 标记回放是否完全复现原始运行。
// 使用当前配置或部分快照时均为 false。
type ReplayReproduction struct {
	FullyReproduced bool
	Reasons         []string
}

// ReplayEvaluation is one independently persisted replay result. It never
// modifies the original decision row.
// ReplayEvaluation 是一条独立持久化的回放结果，绝不修改原始决策行。
type ReplayEvaluation struct {
	ID           int64
	DecisionID   int64
	BotID        int64
	Mode         string
	Name         string
	Note         string
	Changed      bool
	Before       Run
	After        Run
	Parameters   ReplayParameters
	Cost         ReplayCost
	Reproduction ReplayReproduction
	CreatedAt    time.Time
}

// ReplayEvaluationSummary is the list projection of one saved evaluation.
// ReplayEvaluationSummary 是已保存评估的列表投影。
type ReplayEvaluationSummary struct {
	ID           int64
	DecisionID   int64
	BotID        int64
	Mode         string
	Name         string
	Changed      bool
	Summary      RunSummary
	Reproduction ReplayReproduction
	CreatedAt    time.Time
}

// ReplayEvaluationQuery filters saved evaluations.
// ReplayEvaluationQuery 过滤已保存评估。
type ReplayEvaluationQuery struct {
	BotID      int64
	DecisionID int64
	Limit      int
	BeforeID   int64
}

// ReplayDifference is one compared field between two runs.
// ReplayDifference 是两次运行之间的一项对比字段。
type ReplayDifference struct {
	Field  string
	Before string
	After  string
}

// ReplayComparison compares a saved evaluation with the original run or with
// another saved evaluation.
// ReplayComparison 将一条已保存评估与原始运行或另一条已保存评估对比。
type ReplayComparison struct {
	EvaluationID int64
	DecisionID   int64
	Against      string
	Changed      bool
	Original     Run
	Replay       Run
	Differences  []ReplayDifference
	Reproduction ReplayReproduction
}
