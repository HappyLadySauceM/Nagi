// Package domain defines the runtime-editable settings model shared by the
// management API and every component that reads configuration per run.
// Package domain 定义运行时可编辑的配置模型；
// 管理 API 与所有按次读取配置的组件共享同一模型。
package domain

import (
	"fmt"
	"net/url"
	"strings"
	"time"
)

// Revision is one stored revision of the settings row.
// Revision 是已存储的配置行修订。
type Revision struct {
	Settings  Settings
	Version   int64
	UpdatedAt time.Time
	UpdatedBy string
}

// Settings is the process-wide configuration that operators may change from
// the console without restarting the process. Secrets never live here: API
// keys, database DSNs and the admin token stay in the config file or env.
// Settings 是运维可在控制台修改且无需重启的进程级配置。
// 密钥类配置绝不进入这里：API Key、数据库 DSN 与管理令牌仍由配置文件或环境变量提供。
type Settings struct {
	AI     AI     `json:"AI"`
	Intent Intent `json:"Intent"`
	Agent  Agent  `json:"Agent"`
	Tools  Tools  `json:"Tools"`
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

// Intent is the decision pipeline section; api-key stays in the config file.
// Intent 是意图决策分区；api-key 仍由配置文件提供。
type Intent struct {
	Enabled  bool   `json:"Enabled"`
	Provider string `json:"Provider"`
	Endpoint string `json:"Endpoint"`
	Model    string `json:"Model"`
	BotName  string `json:"BotName"`
	// TimeoutSeconds bounds one semantic decision call.
	// TimeoutSeconds 限制单次语义判定调用。
	TimeoutSeconds        int     `json:"TimeoutSeconds"`
	ContinuationThreshold float64 `json:"ContinuationThreshold"`
	InterjectionThreshold float64 `json:"InterjectionThreshold"`
	AddressedOtherMax     float64 `json:"AddressedOtherMax"`
	HistoryLimit          int     `json:"HistoryLimit"`
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
// Sending 是最终回复与发送工具共享的出站预算。
type Sending struct {
	MaxMessagesPerRun   int `json:"MaxMessagesPerRun"`
	MaxCharsPerMessage  int `json:"MaxCharsPerMessage"`
	MaxTotalCharsPerRun int `json:"MaxTotalCharsPerRun"`
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
	MaxStepsMin            = 1
	MaxStepsMax            = 50
	MaxToolCallsMin        = 1
	MaxToolCallsMax        = 100
	MaxRunDurationMin      = 1
	MaxRunDurationMax      = 600
	MaxMessagesPerRunMin   = 1
	MaxMessagesPerRunMax   = 20
	MaxCharsPerMessageMin  = 1
	MaxCharsPerMessageMax  = 4000
	MaxTotalCharsPerRunMin = 1
	MaxTotalCharsPerRunMax = 20000
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
)

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
	s.Intent.BotName = strings.TrimSpace(s.Intent.BotName)
	if s.Tools.PermissionDefaults == nil {
		s.Tools.PermissionDefaults = map[string]bool{}
	}
}

// Clone returns a deep copy so the defaults baseline can never be mutated by
// an in-flight edit.
// Clone 返回深拷贝，默认基线因此不会被进行中的编辑改动。
func (s Settings) Clone() Settings {
	clone := s
	if s.Tools.PermissionDefaults != nil {
		clone.Tools.PermissionDefaults = make(map[string]bool, len(s.Tools.PermissionDefaults))
		for key, value := range s.Tools.PermissionDefaults {
			clone.Tools.PermissionDefaults[key] = value
		}
	}
	return clone
}

// Validate checks every range, URL and cross-field rule. knownPermissions is
// the registry snapshot; nil skips that membership check so the settings service
// still works when tools are not registered.
// Validate 校验全部范围、URL 与跨字段规则。knownPermissions 来自注册表快照；
// 传 nil 表示跳过成员校验，使未注册任何工具时配置服务仍可工作。
func (s Settings) Validate(knownPermissions map[string]bool) error {
	problems := &ValidationError{}
	s.validateAI(problems)
	s.validateIntent(problems)
	s.validateAgent(problems)
	s.validateTools(problems, knownPermissions)
	if len(problems.Fields) > 0 {
		return problems
	}
	return nil
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
	if !inRange(s.Intent.HistoryLimit, HistoryLimitMin, HistoryLimitMax) {
		problems.Add("Intent.HistoryLimit", rangeHint(HistoryLimitMin, HistoryLimitMax))
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
	if s.Agent.Sending.MaxCharsPerMessage > s.Agent.Sending.MaxTotalCharsPerRun {
		problems.Add("Agent.Sending.MaxCharsPerMessage", "单条字数不得超过整轮字数")
	}
}

func (s Settings) validateTools(problems *ValidationError, knownPermissions map[string]bool) {
	if !inRange(s.Tools.TimeoutSeconds, ToolTimeoutMin, ToolTimeoutMax) {
		problems.Add("Tools.TimeoutSeconds", rangeHint(ToolTimeoutMin, ToolTimeoutMax))
	}
	if !inRange(s.Tools.MaxResultBytes, MaxResultBytesMin, MaxResultBytesMax) {
		problems.Add("Tools.MaxResultBytes", rangeHint(MaxResultBytesMin, MaxResultBytesMax))
	}
	for permission := range s.Tools.PermissionDefaults {
		if knownPermissions != nil && !knownPermissions[permission] {
			problems.Add("Tools.PermissionDefaults", fmt.Sprintf("未知的权限 %q", permission))
			break
		}
	}
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
