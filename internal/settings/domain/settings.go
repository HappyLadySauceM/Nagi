// Package domain defines the runtime-editable settings model shared by the
// management API and every component that reads configuration per run.
// Package domain 定义运行时可编辑的配置模型；
// 管理 API 与所有按次读取配置的组件共享同一模型。
package domain

import (
	"encoding/json"
	"fmt"
	models "github.com/HappyLadySauceM/Nagi/internal/models/contracts"
	"net/url"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"
)

// Revision is one stored revision of the settings row.
// Revision 是已存储的配置行修订。
type Revision struct {
	Settings  Settings
	Version   int64
	UpdatedAt time.Time
	UpdatedBy string
}

// Settings stores runtime-editable configuration and encrypted model credentials.
// Public management views remove all ciphertext before serialization.
// Settings 保存运行时可编辑配置和加密模型凭证；
// 公开管理视图在序列化前移除全部密文。
type Extraction struct {
	Enabled          bool
	Concurrency      int
	BatchMaxMessages int
	BatchMaxRunes    int
	MaxWaitMS        int64
	PollIntervalMS   int64
	LeaseDurationMS  int64
	MaxAttempts      int
	RetryDelaysMS    []int64
}

type Settings struct {
	Extraction *Extraction    `json:"Extraction,omitempty"`
	Models     models.Catalog `json:"Models"`
	Role       Role           `json:"Role"`
	Context    Context        `json:"Context"`
	AI         AI             `json:"AI"`
	Intent     Intent         `json:"Intent"`
	Agent      Agent          `json:"Agent"`
	Messages   Messages       `json:"Messages"`
	Tools      Tools          `json:"Tools"`
}

// PromptVariable is one user-defined prompt variable substituted at runtime.
// PromptVariable 是一条运行时替换的用户自定义 Prompt 变量。
type PromptVariable struct {
	Name  string `json:"Name"`
	Value string `json:"Value"`
}

// Role holds the bot identity shared by intent judgment and reply generation.
// Role 保存意图判断与回复生成共用的机器人身份。
type Role struct {
	BotName string `json:"BotName"`
	// Variables are ordered custom prompt variables; names are unique.
	// Variables 是有序的自定义 Prompt 变量，名称唯一。
	Variables []PromptVariable `json:"Variables,omitempty"`
}

// Context holds the conversation history budget shared by intent and generation.
// Context 保存意图判断与生成共用的会话历史预算。
type Context struct {
	HistoryLimit int `json:"HistoryLimit"`
}

// AI is the generation model section; api-key is intentionally absent.
// AI 是生成模型分区；api-key 刻意不在此模型中。
type AI struct {
	Enabled bool `json:"Enabled"`
	// BaseURL and Model are sent to the OpenAI-compatible endpoint.
	// BaseURL 与 Model 直接传给 OpenAI 兼容端点。
	BaseURL string `json:"BaseURL"`
	Model   string `json:"Model"`
	// MaxOutputTokens bounds one generation.
	// MaxOutputTokens 限制单次生成的输出长度。
	MaxOutputTokens int `json:"MaxOutputTokens"`
	// GenerationTimeoutSeconds bounds one HTTP generation call.
	// GenerationTimeoutSeconds 限制单次生成请求的时长。
	GenerationTimeoutSeconds int `json:"GenerationTimeoutSeconds"`
}

// Intent holds decision behavior and the selected model connection snapshot.
// Intent 保存决策行为及所选模型的连接快照。
type Intent struct {
	Enabled  bool   `json:"Enabled"`
	Provider string `json:"Provider"`
	Endpoint string `json:"Endpoint"`
	Model    string `json:"Model"`
	// TimeoutSeconds bounds one semantic decision call.
	// TimeoutSeconds 限制单次语义判定调用。
	TimeoutSeconds           int     `json:"TimeoutSeconds"`
	ContinuationThreshold    float64 `json:"ContinuationThreshold"`
	InterjectionThreshold    float64 `json:"InterjectionThreshold"`
	AddressedOtherMax        float64 `json:"AddressedOtherMax"`
	TopicContinuationEnabled bool    `json:"TopicContinuationEnabled"`
	TopicIdleSeconds         int     `json:"TopicIdleSeconds"`

	// BotName and HistoryLimit are legacy persisted fields. UnmarshalJSON reads
	// them for old rows, Normalize migrates them into Role/Context, and the
	// public model never serializes them.
	// BotName 与 HistoryLimit 是旧版持久化字段；UnmarshalJSON 读取旧行，
	// Normalize 将其迁移到 Role/Context，公开模型永不序列化这两个字段。
	BotName      string `json:"-"`
	HistoryLimit int    `json:"-"`
}

// UnmarshalJSON reads old settings payloads that still carry Intent.BotName and
// Intent.HistoryLimit; all other fields decode normally.
// UnmarshalJSON 读取仍带 Intent.BotName 与 Intent.HistoryLimit 的旧设置载荷；
// 其它字段保持不变。
func (i *Intent) UnmarshalJSON(data []byte) error {
	type plain Intent
	var raw struct {
		plain
		BotName      string `json:"BotName"`
		HistoryLimit int    `json:"HistoryLimit"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	*i = Intent(raw.plain)
	i.BotName = raw.BotName
	i.HistoryLimit = raw.HistoryLimit
	return nil
}

// ActiveTopicIdleSeconds returns zero while group topic continuation is disabled.
// ActiveTopicIdleSeconds 在群聊话题续接关闭时返回零。
func (i Intent) ActiveTopicIdleSeconds() int {
	if !i.TopicContinuationEnabled {
		return 0
	}
	if i.TopicIdleSeconds == 0 {
		return 300
	}
	return i.TopicIdleSeconds
}

// Agent holds the limits of one reply run.
// Agent 持有单次回复运行的各项上限。
type Agent struct {
	MaxSteps              int     `json:"MaxSteps"`
	MaxToolCalls          int     `json:"MaxToolCalls"`
	MaxRunDurationSeconds int     `json:"MaxRunDurationSeconds"`
	Sending               Sending `json:"Sending"`
}

// Sending is the outbound budget shared by the final reply and the send tool.
// Zero means unlimited for every field.
// Sending 是最终回复与发送工具共享的出站预算，各字段 0 表示不限。
type Sending struct {
	MaxMessagesPerRun   int `json:"MaxMessagesPerRun"`
	MaxCharsPerMessage  int `json:"MaxCharsPerMessage"`
	MaxTotalCharsPerRun int `json:"MaxTotalCharsPerRun"`
}

// Messages is the global message retention section; retention is no longer
// configured per conversation.
// Messages 是全局消息留存分区；留存不再按会话配置。
type Messages struct {
	// RetentionDays keeps stored messages for this many days; 0 disables
	// cleanup entirely.
	// RetentionDays 是消息在库中的保留天数；0 表示不清理。
	RetentionDays int `json:"RetentionDays"`
}

// Tools is the tool registry section. Disabled providers keep their tools out
// of every run; there is no process-level side-effect switch any more.
// Tools 是工具注册表分区。停用的提供方不会向任何运行暴露工具；
// 进程级的副作用总开关已被移除。
type Tools struct {
	// Every registered provider is available; there is no enable list. Which
	// conversation may call which tool is decided by its capability grant.
	// 每个已注册提供方都可用，不存在启用列表；
	// 哪个会话可以调用哪个工具由该会话的能力授权决定。
	// TimeoutSeconds and MaxResultBytes bound one tool call.
	// TimeoutSeconds 与 MaxResultBytes 限制单次工具调用。
	TimeoutSeconds int `json:"TimeoutSeconds"`
	MaxResultBytes int `json:"MaxResultBytes"`
	// PermissionDefaults overrides the descriptor fallback per permission.
	// PermissionDefaults 按权限标识覆盖描述符兜底。
	PermissionDefaults map[string]bool `json:"PermissionDefaults"`
}

// Field limits double as API validation rules and as the hints the console
// shows next to every number input.
// 字段边界同时作为接口校验规则与界面上的数值提示。
const (
	MaxStepsMin       = 1
	MaxStepsMax       = 50
	MaxToolCallsMin   = 1
	MaxToolCallsMax   = 100
	MaxRunDurationMin = 1
	MaxRunDurationMax = 600
	// Sending limits use 0 as "unlimited", so their minimums are 0.
	// 发送上限用 0 表示不限，因此下限为 0。
	MaxMessagesPerRunMin   = 0
	MaxMessagesPerRunMax   = 100
	MaxCharsPerMessageMin  = 0
	MaxCharsPerMessageMax  = 100000
	MaxTotalCharsPerRunMin = 0
	MaxTotalCharsPerRunMax = 1000000
	RetentionDaysMin       = 0
	RetentionDaysMax       = 3650
	ToolTimeoutMin         = 1
	ToolTimeoutMax         = 120
	MaxResultBytesMin      = 256
	MaxResultBytesMax      = 1048576
	GenerationTimeoutMin   = 1
	GenerationTimeoutMax   = 300
	IntentTimeoutMin       = 1
	IntentTimeoutMax       = 120
	HistoryLimitMin        = 1
	HistoryLimitMax        = 200
	MaxOutputTokensMin     = 1
	MaxOutputTokensMax     = 32000
	// Custom prompt variable limits keep the runtime substitution bounded.
	// 自定义 Prompt 变量上限让运行时替换保持有界。
	maxPromptVariables        = 50
	maxPromptVariableNameLen  = 32
	maxPromptVariableValueLen = 500
)

// promptVariableNamePattern matches a legal custom prompt variable identifier.
// promptVariableNamePattern 匹配合法的自定义 Prompt 变量标识符。
var promptVariableNamePattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// reservedPromptVariables cannot be redefined as custom variables; the runtime
// provides them. reservedPromptVariables 不可作为自定义变量，由运行时提供。
var reservedPromptVariables = map[string]struct{}{
	"bot_name": {}, "scope_type": {}, "target_id": {}, "sender_id": {}, "bot_id": {},
	"messages_json": {}, "state": {}, "question": {},
}

// ValidationError carries one message per invalid field so the console can
// point at the exact input instead of showing a single opaque failure.
// ValidationError 为每个非法字段携带一条消息，
// 界面因此能定位到具体输入，而不是只显示一条笼统错误。
type ValidationError struct {
	Fields map[string]string
}

// Error summarizes how many fields failed validation.
// Error 汇总校验失败字段的数量。
func (e *ValidationError) Error() string {
	return fmt.Sprintf("settings: %d 项校验未通过", len(e.Fields))
}

// Add records one field failure; the first message per field wins.
// Add 记录一个字段错误；同一字段仅保留第一条消息。
func (e *ValidationError) Add(field, message string) {
	if e.Fields == nil {
		e.Fields = make(map[string]string)
	}
	if _, exists := e.Fields[field]; !exists {
		e.Fields[field] = message
	}
}

// Normalize trims text fields and fills the permission map so two semantically
// identical payloads always compare equal.
// Normalize 去空格并补全权限映射，
// 使语义相同的两份配置始终可以逐字段比较。
func (s *Settings) Normalize() {
	s.AI.BaseURL = strings.TrimSpace(s.AI.BaseURL)
	s.AI.Model = strings.TrimSpace(s.AI.Model)
	s.Intent.Provider = strings.ToLower(strings.TrimSpace(s.Intent.Provider))
	s.Intent.Endpoint = strings.TrimSpace(s.Intent.Endpoint)
	s.Intent.Model = strings.TrimSpace(s.Intent.Model)
	if s.Intent.TopicIdleSeconds == 0 {
		s.Intent.TopicIdleSeconds = 300
	}
	s.Role.BotName = strings.TrimSpace(s.Role.BotName)
	if s.Role.BotName == "" {
		s.Role.BotName = strings.TrimSpace(s.Intent.BotName)
	}
	for i := range s.Role.Variables {
		s.Role.Variables[i].Name = strings.TrimSpace(s.Role.Variables[i].Name)
		s.Role.Variables[i].Value = strings.TrimSpace(s.Role.Variables[i].Value)
	}
	if s.Context.HistoryLimit == 0 && s.Intent.HistoryLimit > 0 {
		s.Context.HistoryLimit = s.Intent.HistoryLimit
	}
	s.Intent.BotName = ""
	s.Intent.HistoryLimit = 0
	if s.Tools.PermissionDefaults == nil {
		s.Tools.PermissionDefaults = map[string]bool{}
	}
}

// Clone returns a deep copy so the defaults baseline can never be mutated by
// an in-flight edit.
// Clone 返回深拷贝，默认基线因此不会被进行中的编辑改动。
func (s Settings) Clone() Settings {
	clone := s
	clone.Models = s.Models.Clone()
	if s.Extraction != nil {
		extraction := *s.Extraction
		extraction.RetryDelaysMS = append([]int64(nil), extraction.RetryDelaysMS...)
		clone.Extraction = &extraction
	}
	if s.Role.Variables != nil {
		clone.Role.Variables = make([]PromptVariable, len(s.Role.Variables))
		copy(clone.Role.Variables, s.Role.Variables)
	}
	if s.Tools.PermissionDefaults != nil {
		clone.Tools.PermissionDefaults = make(map[string]bool, len(s.Tools.PermissionDefaults))
		for key, value := range s.Tools.PermissionDefaults {
			clone.Tools.PermissionDefaults[key] = value
		}
	}
	return clone
}

// PromptVariableMap returns the custom prompt variables by trimmed name.
// Empty names are skipped; values are trimmed to match Normalize.
// PromptVariableMap 按去除空格的名称返回自定义 Prompt 变量；
// 空名称跳过，取值同样去空格以与 Normalize 一致。
func (r Role) PromptVariableMap() map[string]string {
	if len(r.Variables) == 0 {
		return nil
	}
	out := make(map[string]string, len(r.Variables))
	for _, variable := range r.Variables {
		name := strings.TrimSpace(variable.Name)
		if name == "" {
			continue
		}
		out[name] = strings.TrimSpace(variable.Value)
	}
	return out
}

// Validate checks every range, URL and cross-field rule. knownPermissions is
// the registry snapshot; nil skips that membership check so the settings service
// still works when tools are not registered.
// Validate 校验全部范围、URL 与跨字段规则。knownPermissions 来自注册表快照；
// 传 nil 表示跳过成员校验，使未注册任何工具时配置服务仍可工作。
func (s Settings) Validate(knownPermissions map[string]bool) error {
	problems := &ValidationError{}
	s.validateAI(problems)
	s.validateContext(problems)
	s.validateIntent(problems)
	s.validateRole(problems)
	s.validateAgent(problems)
	s.validateMessages(problems)
	s.validateTools(problems, knownPermissions)
	if len(problems.Fields) > 0 {
		return problems
	}
	return nil
}

// validateRole checks the ordered custom prompt variables: legal unique names,
// bounded values, no reserved names and a bounded count.
// validateRole 校验有序的自定义 Prompt 变量：名称合法且唯一、取值有界、
// 不含保留名，且数量有界。
func (s Settings) validateRole(problems *ValidationError) {
	if len(s.Role.Variables) > maxPromptVariables {
		problems.Add("Role.Variables", fmt.Sprintf("自定义变量最多 %d 条", maxPromptVariables))
	}
	seen := make(map[string]struct{}, len(s.Role.Variables))
	for _, variable := range s.Role.Variables {
		name := strings.TrimSpace(variable.Name)
		value := strings.TrimSpace(variable.Value)
		if len(name) == 0 || len(name) > maxPromptVariableNameLen || !promptVariableNamePattern.MatchString(name) {
			problems.Add("Role.Variables", "变量名需匹配 ^[A-Za-z_][A-Za-z0-9_]*$ 且不超过 32 字符")
			continue
		}
		if _, reserved := reservedPromptVariables[name]; reserved {
			problems.Add("Role.Variables", "变量名为内置保留名："+name)
			continue
		}
		if utf8.RuneCountInString(value) > maxPromptVariableValueLen {
			problems.Add("Role.Variables", "变量值不能超过 500 字符")
		}
		if _, duplicated := seen[name]; duplicated {
			problems.Add("Role.Variables", "变量名重复："+name)
		}
		seen[name] = struct{}{}
	}
}

func (s Settings) validateAI(problems *ValidationError) {
	if !s.AI.Enabled {
		return
	}
	// Generation without the pipeline would store messages and never reply.
	// 只开生成不开流水线会让消息只入库、永不回复。
	if !s.Intent.Enabled {
		problems.Add("AI.Enabled", "启用生成模型前必须启用意图流水线")
	}
	if !validHTTPURL(s.AI.BaseURL) {
		problems.Add("AI.BaseURL", "需要以 http:// 或 https:// 开头的地址")
	}
	if s.AI.Model == "" {
		problems.Add("AI.Model", "模型名不能为空")
	}
	if !inRange(s.AI.MaxOutputTokens, MaxOutputTokensMin, MaxOutputTokensMax) {
		problems.Add("AI.MaxOutputTokens", rangeHint(MaxOutputTokensMin, MaxOutputTokensMax))
	}
	if !inRange(s.AI.GenerationTimeoutSeconds, GenerationTimeoutMin, GenerationTimeoutMax) {
		problems.Add("AI.GenerationTimeoutSeconds", rangeHint(GenerationTimeoutMin, GenerationTimeoutMax))
	}
}

func (s Settings) validateIntent(problems *ValidationError) {
	if s.Intent.TopicIdleSeconds != 0 && !inRange(s.Intent.TopicIdleSeconds, 30, 3600) {
		problems.Add("Intent.TopicIdleSeconds", rangeHint(30, 3600))
	}
	if s.Intent.Provider != "jev" && s.Intent.Provider != "openai" {
		problems.Add("Intent.Provider", "仅支持 jev 或 openai")
	}
	if !s.Intent.Enabled {
		return
	}
	if !validHTTPURL(s.Intent.Endpoint) {
		problems.Add("Intent.Endpoint", "需要以 http:// 或 https:// 开头的地址")
	}
	if s.Intent.Model == "" {
		problems.Add("Intent.Model", "模型名不能为空")
	}
	if !inRange(s.Intent.TimeoutSeconds, IntentTimeoutMin, IntentTimeoutMax) {
		problems.Add("Intent.TimeoutSeconds", rangeHint(IntentTimeoutMin, IntentTimeoutMax))
	}
	for field, value := range map[string]float64{
		"Intent.ContinuationThreshold": s.Intent.ContinuationThreshold,
		"Intent.InterjectionThreshold": s.Intent.InterjectionThreshold,
		"Intent.AddressedOtherMax":     s.Intent.AddressedOtherMax,
	} {
		if value <= 0 || value > 1 {
			problems.Add(field, "阈值必须落在 (0,1] 区间")
		}
	}
}

// validateContext checks the shared conversation history budget even when the
// intent pipeline is disabled, because generation also reads the same value.
// validateContext 校验共享会话历史预算；即使意图流水线关闭，生成也读取该值。
func (s Settings) validateContext(problems *ValidationError) {
	if !inRange(s.Context.HistoryLimit, HistoryLimitMin, HistoryLimitMax) {
		problems.Add("Context.HistoryLimit", rangeHint(HistoryLimitMin, HistoryLimitMax))
	}
}

func (s Settings) validateAgent(problems *ValidationError) {
	if !inRange(s.Agent.MaxSteps, MaxStepsMin, MaxStepsMax) {
		problems.Add("Agent.MaxSteps", rangeHint(MaxStepsMin, MaxStepsMax))
	}
	if !inRange(s.Agent.MaxToolCalls, MaxToolCallsMin, MaxToolCallsMax) {
		problems.Add("Agent.MaxToolCalls", rangeHint(MaxToolCallsMin, MaxToolCallsMax))
	}
	if !inRange(s.Agent.MaxRunDurationSeconds, MaxRunDurationMin, MaxRunDurationMax) {
		problems.Add("Agent.MaxRunDurationSeconds", rangeHint(MaxRunDurationMin, MaxRunDurationMax))
	}
	if !inRange(s.Agent.Sending.MaxMessagesPerRun, MaxMessagesPerRunMin, MaxMessagesPerRunMax) {
		problems.Add("Agent.Sending.MaxMessagesPerRun", rangeHint(MaxMessagesPerRunMin, MaxMessagesPerRunMax))
	}
	if !inRange(s.Agent.Sending.MaxCharsPerMessage, MaxCharsPerMessageMin, MaxCharsPerMessageMax) {
		problems.Add("Agent.Sending.MaxCharsPerMessage", rangeHint(MaxCharsPerMessageMin, MaxCharsPerMessageMax))
	}
	if !inRange(s.Agent.Sending.MaxTotalCharsPerRun, MaxTotalCharsPerRunMin, MaxTotalCharsPerRunMax) {
		problems.Add("Agent.Sending.MaxTotalCharsPerRun", rangeHint(MaxTotalCharsPerRunMin, MaxTotalCharsPerRunMax))
	}
	// A zero run budget means unlimited, so the cross-field rule only applies
	// when the run is actually bounded.
	// 整轮预算为 0 表示不限，因此只有存在整轮上限时才校验单条上限。
	if s.Agent.Sending.MaxTotalCharsPerRun > 0 &&
		s.Agent.Sending.MaxCharsPerMessage > s.Agent.Sending.MaxTotalCharsPerRun {
		problems.Add("Agent.Sending.MaxCharsPerMessage", "单条字数不得超过整轮字数")
	}
}

func (s Settings) validateMessages(problems *ValidationError) {
	if !inRange(s.Messages.RetentionDays, RetentionDaysMin, RetentionDaysMax) {
		problems.Add("Messages.RetentionDays", rangeHint(RetentionDaysMin, RetentionDaysMax))
	}
}

func (s Settings) validateTools(problems *ValidationError, _ map[string]bool) {
	if !inRange(s.Tools.TimeoutSeconds, ToolTimeoutMin, ToolTimeoutMax) {
		problems.Add("Tools.TimeoutSeconds", rangeHint(ToolTimeoutMin, ToolTimeoutMax))
	}
	if !inRange(s.Tools.MaxResultBytes, MaxResultBytesMin, MaxResultBytesMax) {
		problems.Add("Tools.MaxResultBytes", rangeHint(MaxResultBytesMin, MaxResultBytesMax))
	}
	// Permission defaults are an internal baseline now; stale keys from a
	// provider that is no longer registered must not block saving settings.
	// 权限默认值现在是内部基线；提供方下线留下的陈旧键不应阻塞保存。
}

func inRange(value, min, max int) bool { return value >= min && value <= max }

func rangeHint(min, max int) string { return fmt.Sprintf("取值范围 %d-%d", min, max) }

// validHTTPURL reports whether raw is an absolute http(s) URL.
// validHTTPURL 判断 raw 是否为绝对的 http(s) 地址。
func validHTTPURL(raw string) bool {
	parsed, err := url.Parse(raw)
	if err != nil {
		return false
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return false
	}
	return parsed.Host != ""
}
