// Package contracts publishes the stable interfaces of the intent context.
// intent/contracts 发布 intent 上下文对外协作的稳定接口。
package contracts

import (
	"context"
	"time"

	"github.com/HappyLadySauceM/Nagi/internal/intent/domain"
)

// SemanticCostEstimate describes a semantic evaluation that may issue more than
// one provider request. It is deliberately not a promise of one billable call.
// SemanticCostEstimate 描述一次语义判定可能发出多次提供方请求，不承诺只有一次计费调用。
const SemanticCostEstimate = "一次语义判定，可能包含多次模型请求，费用依提供方"

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
	Index       int
	Phase       string
	NodeID      string
	NodeVersion int
	Status      string
	Branch      string
	Summary     string
	ReasonCode  string
	Detail      map[string]any
	Evidence    []EvidenceItem
	DurationMS  int64
	CreatedAt   time.Time
}

// EvidenceItem is one short fact attached to a run node.
// EvidenceItem 是挂在运行节点上的一条简短事实。
type EvidenceItem struct {
	Kind string
	Text string
}

// RunSummary is the human-readable final answer of one run.
// RunSummary 是一次运行的人类可读最终结论。
type RunSummary struct {
	Decision   string
	ReasonCode string
	ReasonText string
}

// RunNode is one node result in a fixed execution flow.
// RunNode 是固定执行流程中的一个节点结果。
type RunNode struct {
	NodeID     string
	Version    int
	Status     string
	Branch     string
	ReasonCode string
	Summary    string
	Detail     map[string]any
	Evidence   []EvidenceItem
	DurationMS int64
}

// RunEdge is one edge result in a fixed execution flow.
// RunEdge 是固定执行流程中的一条边结果。
type RunEdge struct {
	EdgeID string
	Status string
}

// DeliveryItem is one outbound delivery record linked to a decision job.
// DeliveryItem 是与决策任务关联的一条投递记录。
type DeliveryItem struct {
	ID                string
	Sequence          int
	Status            string
	Text              string
	PlatformMessageID int64
	ErrorCode         string
}

// Delivery is the independent outbound outcome of a decision job.
// Delivery 是决策任务独立的出站结果。
type Delivery struct {
	Outcome string
	Reason  string
	Denied  int
	Items   []DeliveryItem
}

// ReplayInfo marks which parts of a temporary replay result came from the
// stored snapshot and which were evaluated against current configuration.
// ReplayInfo 标记临时回放结果中来自快照与来自当前配置的部分。
type ReplayInfo struct {
	Input     string `json:",omitempty"`
	Semantic  string `json:",omitempty"`
	Prompt    string `json:",omitempty"`
	Policy    string `json:",omitempty"`
	Rules     string `json:",omitempty"`
	Persisted bool
}

// PromptInfo identifies the immutable prompt version used by one run.
// PromptInfo 标识一次运行使用的不可变 Prompt 版本。
type PromptInfo struct {
	ID        string
	Name      string
	Version   int
	Source    string
	SourceRef string
	Hash      string
}

// Run is the single run contract shared by decision detail and simulation.
// Run 是决策详情与模拟共享的统一运行契约。
type Run struct {
	DecisionID     int64
	JobID          int64
	RunID          string
	FlowVersion    string
	Provider       string
	Model          string
	RulesetVersion string
	SnapshotStatus string
	MissingFields  []string
	Summary        RunSummary
	Nodes          []RunNode
	Edges          []RunEdge
	Delivery       Delivery
	Scores         map[string]float64
	LatencyMS      int64
	CreatedAt      time.Time
	Prompt         PromptInfo
	Replay         *ReplayInfo `json:",omitempty"`
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
	RunID          string
	FlowVersion    string
	SnapshotStatus string
	MissingFields  []string
	Scores         map[string]float64
	LatencyMS      int
	CreatedAt      time.Time
	Prompt         PromptInfo
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
