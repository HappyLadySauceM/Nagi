// Package v1 defines HTTP request and response shapes for the management API.
// Package v1 定义管理 API 的 HTTP 请求与响应结构。
package v1

import (
	"encoding/json"
	"time"

	intentcontracts "github.com/HappyLadySauceM/Nagi/internal/intent/contracts"
	intentdomain "github.com/HappyLadySauceM/Nagi/internal/intent/domain"
	media "github.com/HappyLadySauceM/Nagi/internal/media/contracts"
)

// BuiltinRuleCatalogEntry is one read-only built-in rule definition.
// BuiltinRuleCatalogEntry 是一条只读内置规则定义。
type BuiltinRuleCatalogEntry struct {
	RuleID    string
	Order     int
	Title     string
	Condition string
	Action    string
	Editable  bool
	Source    string
}

// FixedConstraintCatalogEntry is one read-only server constraint definition.
// FixedConstraintCatalogEntry 是一条只读服务端约束定义。
type FixedConstraintCatalogEntry struct {
	ConstraintID string
	Order        int
	Title        string
	Condition    string
	Action       string
	ReasonCode   string
	Editable     bool
	Source       string
}

// RulesetInfo is the API representation of one version.
// RulesetInfo 是单个版本的 API 表示。
type RulesetInfo struct {
	ID           int64
	BotID        int64
	ScopeType    string
	ScopeID      int64
	Version      int
	Status       string
	Note         string
	Entries      []EntryInfo
	BuiltinRules map[string]bool
	Targets      []intentdomain.RuleTarget
	Questions    map[string]intentdomain.PromptQuestion
}

// EntryInfo is one rule entry.
// EntryInfo 是一条规则条目。
type EntryInfo struct {
	Enabled  *bool
	Priority int
	Match    intentdomain.RuleMatch
	Action   intentdomain.RuleAction
}

// DecisionInfo is the list representation of one decision.
// DecisionInfo 是决策的列表表示。
type DecisionInfo struct {
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
	LatencyMS      int
	CreatedAt      time.Time
}

// DecisionDetail adds the explainable steps to the list representation.
// DecisionDetail 在列表表示之上附带可解释步骤。
type DecisionDetail struct {
	DecisionInfo
	Scores  map[string]float64
	Steps   []StepInfo
	Run     intentcontracts.Run
	Ingress *MessageIngress `json:",omitempty"`
}

// MessageIngress describes the stored message that created a decision job.
// MessageIngress 描述创建决策任务的已存入站消息。
type MessageIngress struct {
	Media             *media.InboundMedia       `json:",omitempty"`
	Attachments       []media.InboundAttachment `json:",omitempty"`
	MessageID         int64
	PlatformMessageID int64
	SenderID          int64
	Text              string
	SegmentTypes      []string
	ReceivedAt        time.Time
	TraceID           string
	JobStatus         string
}

// StepInfo is one explainable stage of a decision.
// StepInfo 是决策中的一个可解释阶段。
type StepInfo struct {
	Index      int
	Phase      string
	ReasonCode string
	Detail     map[string]any
	CreatedAt  time.Time
}

// PromptSummary summarizes one prompt identity.
// PromptSummary 汇总单个 Prompt 身份。
type PromptSummary struct {
	PromptID      string
	Name          string
	ActiveVersion int
	Status        string
	Source        string
	UpdatedAt     string
}

// PromptVersionInfo is one prompt version row.
// PromptVersionInfo 是一条 Prompt 版本记录。
type PromptVersionInfo struct {
	ID          int64
	PromptID    string
	Name        string
	Provider    string
	Version     int
	Status      string
	Source      string
	SourceRef   string
	Hash        string
	Note        string
	Content     json.RawMessage
	CreatedAt   string
	PublishedAt string
}

// UnifiedPromptLayer is one layer in the unified prompt summary.
// UnifiedPromptLayer 是统一 Prompt 汇总中的一个层级。
type UnifiedPromptLayer struct {
	BotID           int64
	ScopeType       string
	ScopeID         int64
	ActiveVersion   int
	ActiveVersionID int64
	ActiveSource    string
	Status          string
	UpdatedAt       string
}

// UnifiedPromptSummary groups layers for one prompt kind.
// UnifiedPromptSummary 按 Prompt 种类归组各层级。
type UnifiedPromptSummary struct {
	PromptID       string
	Kind           string
	Name           string
	Provider       string
	BuiltinVersion int
	Layers         []UnifiedPromptLayer
}

// UnifiedPromptVersion is one unified prompt version row.
// UnifiedPromptVersion 是一条统一 Prompt 版本记录。
type UnifiedPromptVersion struct {
	ID          int64
	PromptID    string
	Kind        string
	Name        string
	Provider    string
	BotID       int64
	ScopeType   string
	ScopeID     int64
	Version     int
	Status      string
	Source      string
	SourceRef   string
	Hash        string
	Note        string
	Content     json.RawMessage
	CreatedAt   string
	PublishedAt string
	ArchivedAt  string
}
