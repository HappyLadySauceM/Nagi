// Package contracts publishes the stable interfaces of the intent context.
// intent/contracts 发布 intent 上下文对外协作的稳定接口。
package contracts

import (
	"context"
	"time"

	"github.com/HappyLadySauceM/Nagi/internal/intent/domain"
)

// ReplyExecutor acts on reply decisions, e.g. generating and sending a response.
// ReplyExecutor 对回复决策执行动作，例如生成并发送回复。
type ReplyExecutor interface {
	ExecuteReply(ctx context.Context, task domain.Task, history []domain.Message) error
}

// DecisionQuery filters the recorded decisions of one account.
// DecisionQuery 过滤某个账号的决策记录。
type DecisionQuery struct {
	ReasonCode string
	// BotID is required; decisions never span accounts.
	// BotID 必填；决策不跨账号返回。
	BotID int64
	// ConversationID narrows the list to one conversation when non-empty.
	// ConversationID 非空时把列表限制在该会话内。
	ConversationID string
	// Limit is the page size, already validated by the caller.
	// Limit 是页大小，由调用方完成校验。
	Limit int
	// BeforeID paginates backwards from a decision id; zero starts at the newest.
	// BeforeID 从某个决策 id 向前翻页；为零时从最新开始。
	BeforeID int64
}

// DecisionStep is one explainable stage of a recorded decision.
// DecisionStep 是已记录决策中的一个可解释阶段。
type DecisionStep struct {
	Index      int
	Phase      string
	ReasonCode string
	Detail     map[string]any
	CreatedAt  time.Time
}

// DecisionRecord is the read model of one recorded decision.
// DecisionRecord 是一条已记录决策的读模型。
type DecisionRecord struct {
	ID             int64
	JobID          int64
	BotID          int64
	ConversationID string
	Decision       string
	ReasonCode     string
	Provider       string
	Model          string
	RulesetVersion string
	Scores         map[string]float64
	LatencyMS      int
	CreatedAt      time.Time
	Steps          []DecisionStep
}

// DecisionReader lists and loads recorded decisions for the admin API.
// DecisionReader 为管理 API 列出与读取决策记录。
type DecisionReader interface {
	List(ctx context.Context, q DecisionQuery) ([]DecisionRecord, error)
	Get(ctx context.Context, id int64) (DecisionRecord, error)
}

type DecisionStats struct {
	Total            int64
	SemanticCalls    int64
	SemanticSuccess  int64
	Degraded         int64
	Replies          int64
	Silent           int64
	AverageLatencyMS float64
}

type DecisionStatsReader interface {
	Stats(ctx context.Context, botID int64, since time.Time) (DecisionStats, error)
}

// SimulationEvaluator evaluates a supplied rule result without recording or delivering messages.
// SimulationEvaluator 对给定规则结果求值，不记录决策或投递消息。
type SimulationEvaluator interface {
	PrepareSimulation(context.Context, domain.Input) (domain.Input, error)
	EvaluateSimulation(context.Context, domain.Input, domain.RuleResult, bool) (domain.FinalDecision, []domain.Step)
}
