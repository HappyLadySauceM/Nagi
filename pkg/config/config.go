// Package config represents the parsed runtime configuration model.
// Package config 表示解析完成的运行时配置模型及其装载逻辑。
// 优先级：默认值 → YAML → 环境变量（NAGI_ 前缀）→ 显式覆盖（Flags）。
package config

import (
	"fmt"
	"log/slog"
	"net"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/HappyLadySauceM/Nagi/pkg/logging"
)

// Duration 支持在 YAML 中书写 "10s" 字符串或纳秒整数。
type Duration time.Duration

// D 返回标准库时长。
func (d Duration) D() time.Duration { return time.Duration(d) }

// UnmarshalYAML 实现 yaml.v3 自定义解码。
func (d *Duration) UnmarshalYAML(value *yaml.Node) error {
	var text string
	if err := value.Decode(&text); err != nil {
		return fmt.Errorf("config: 无法解析时长 %q: %w", value.Value, err)
	}
	text = strings.TrimSpace(text)
	if parsed, err := time.ParseDuration(text); err == nil {
		*d = Duration(parsed)
		return nil
	}
	if nanos, err := strconv.ParseInt(text, 10, 64); err == nil {
		*d = Duration(nanos)
		return nil
	}
	return fmt.Errorf("config: 无法解析时长 %q", text)
}

// Reconnect 是断线重连退避参数。
type Reconnect struct {
	Initial Duration `yaml:"initial"`
	Max     Duration `yaml:"max"`
}

// OnebotEndpoint 描述一个 SnowLuma OneBot v11 实例端点。
// 每个 QQ 账号在 SnowLuma 中拥有独立的 OneBot 实例，因此端点与账号是多对一关系：
// 端点只声明如何连接，账号身份在连接后经 get_login_info 自动识别。
type OnebotEndpoint struct {
	URL string `yaml:"url"`
	// AccessToken 与 OneBot 实现保持一致；空表示不鉴权。
	AccessToken string `yaml:"access-token"`
	// ExpectQQ 是配置声明的预期 QQ 号；非零时与登录信息不一致即拒绝该连接。
	ExpectQQ int64 `yaml:"expect-qq"`
}

// Onebot 是 OneBot v11 连接配置域。
type Onebot struct {
	// Endpoints 是全部 OneBot 端点；每个 QQ 账号对应一个端点，账号身份连接后自动识别。
	Endpoints []OnebotEndpoint `yaml:"endpoints"`
	// CallTimeout 是动作调用的响应超时。
	CallTimeout Duration `yaml:"call-timeout"`
	// HeartbeatTimeout 是心跳看门狗窗口；窗口内没有入站帧即断开重连，<= 0 关闭。
	HeartbeatTimeout Duration  `yaml:"heartbeat-timeout"`
	Reconnect        Reconnect `yaml:"reconnect"`
}

// Server 是管理 HTTP API 配置域。
type Server struct {
	Listen  string `yaml:"listen"`
	Enabled bool   `yaml:"enabled"`
	// AdminToken 为管理 API 的 Bearer Token；为空时仅允许本机（回环）监听且不鉴权，
	// 非回环监听必须显式配置 Token，否则启动失败。
	// AdminToken is the management API bearer token; empty disables auth and is
	// only accepted for loopback listeners, otherwise startup fails.
	AdminToken string `yaml:"admin-token"`
}

// Log 是日志配置域。
type Log struct {
	Level string `yaml:"level"`
}

// Echo 是内置示例上下文配置域。
type Echo struct {
	Enabled bool   `yaml:"enabled"`
	Prefix  string `yaml:"prefix"`
}

// Database 是 PostgreSQL 连接配置域；dsn 为空时数据库功能不启用。
// Database is the PostgreSQL config domain; an empty dsn disables database features.
type Database struct {
	// DSN 是连接字符串，直接写在配置文件中。
	// DSN is the connection string, written directly in the config file.
	DSN string `yaml:"dsn"`
	// MaxConns 是连接池上限。
	// MaxConns is the pool size limit.
	MaxConns int32 `yaml:"max-conns"`
}

// Worker 是决策任务工作器配置域。
// Worker is the decision-job worker config domain.
type Worker struct {
	// Concurrency 是跨会话的全局并发上限。
	// Concurrency is the global cross-conversation concurrency bound.
	Concurrency int `yaml:"concurrency"`
	// MaxPendingJobs 是内存中待处理任务上限；超出部分留在数据库。
	// MaxPendingJobs is the in-memory pending limit; excess stays in the database.
	MaxPendingJobs int `yaml:"max-pending-jobs"`
	// PollInterval 是任务轮询间隔。
	// PollInterval is the job polling interval.
	PollInterval Duration `yaml:"poll-interval"`
}

// AI 是 LLM 生成配置域；api-key 直接写在配置文件中（本地配置已 gitignore）。
// AI is the LLM generation config domain; api-key is written directly in the config file.
type AI struct {
	Enabled           bool     `yaml:"enabled"`
	BaseURL           string   `yaml:"base-url"`
	APIKey            string   `yaml:"api-key"`
	Model             string   `yaml:"model"`
	GenerationTimeout Duration `yaml:"generation-timeout"`
	MaxOutputTokens   int      `yaml:"max-output-tokens"`
}

// Intent 是意图决策配置域。
// Intent is the intent decision config domain.
type Intent struct {
	Enabled               bool     `yaml:"enabled"`
	Provider              string   `yaml:"provider"`
	Endpoint              string   `yaml:"endpoint"`
	APIKey                string   `yaml:"api-key"`
	Model                 string   `yaml:"model"`
	BotName               string   `yaml:"bot-name"`
	Timeout               Duration `yaml:"timeout"`
	ActiveWindow          Duration `yaml:"active-window"`
	ContinuationThreshold float64  `yaml:"continuation-threshold"`
	InterjectionThreshold float64  `yaml:"interjection-threshold"`
	AddressedOtherMax     float64  `yaml:"addressed-other-max"`
	HistoryLimit          int      `yaml:"history-limit"`
}

// Agent 是 Agent 运行时的执行限制。
// Agent holds the runtime limits of one reply generation.
type Agent struct {
	// MaxSteps 限制 ReAct 循环轮数，防止工具调用死循环。
	// MaxSteps bounds the ReAct loop so tool calls cannot spin forever.
	MaxSteps int `yaml:"max-steps"`
	// MaxToolCalls 限制单次运行的累计工具调用次数。
	// MaxToolCalls bounds the tool calls of one run.
	MaxToolCalls int `yaml:"max-tool-calls"`
	// MaxRunDuration 是单次运行的总时长上限（含模型与工具调用）。
	// MaxRunDuration bounds one run including model and tool calls.
	MaxRunDuration Duration `yaml:"max-run-duration"`
	// Sending 限制单次运行可发送的出站消息。
	// Sending bounds the outbound messages of one run.
	Sending Sending `yaml:"sending"`
}

// Sending 是单次 Agent 运行的出站消息预算；字符数按 Unicode 字符计。
// Sending is the outbound budget of one agent run; character counts are runes.
type Sending struct {
	// MaxMessagesPerRun 限制一次运行的消息条数。
	// MaxMessagesPerRun bounds the message count of one run.
	MaxMessagesPerRun int `yaml:"max-messages-per-run"`
	// MaxCharsPerMessage 限制单条消息的字符数。
	// MaxCharsPerMessage bounds one message in runes.
	MaxCharsPerMessage int `yaml:"max-chars-per-message"`
	// MaxTotalCharsPerRun 限制一次运行的字符总数。
	// MaxTotalCharsPerRun bounds the summed rune count of one run.
	MaxTotalCharsPerRun int `yaml:"max-total-chars-per-run"`
}

// Tools 是工具注册与调用限制。
// Tools configures tool registration and per-call limits.
type Tools struct {
	// Enabled 列出启用的提供方 ID（如 builtin.time）；为空时不暴露任何工具。
	// Enabled lists provider IDs (e.g. builtin.time); empty exposes no tools.
	Enabled []string `yaml:"enabled"`
	// PermissionDefaults 按权限标识覆盖描述符兜底；未列出的权限保持兜底规则
	// （只读允许、其余拒绝），作用域覆盖的优先级更高。
	// PermissionDefaults overrides the descriptor fallback per permission;
	// unlisted permissions keep the fallback rule (read-only allowed, others
	// denied), and scope overrides take precedence.
	PermissionDefaults map[string]bool `yaml:"permission-defaults"`
	// AllowSendCurrent 为 true 时才暴露 qq.send_current；默认 false。
	// AllowSendCurrent exposes qq.send_current at all; false keeps it hidden.
	AllowSendCurrent bool `yaml:"allow-send-current"`
	// AllowSideEffects 为 true 时才可能暴露非只读工具；默认 false。
	// AllowSideEffects exposes non read-only tools at all; false keeps them hidden.
	AllowSideEffects bool `yaml:"allow-side-effects"`
	// Timeout 限制单次工具调用，默认 5s。
	// Timeout bounds one tool call; defaults to 5s.
	Timeout Duration `yaml:"timeout"`
	// MaxResultBytes 限制回给模型的结果大小，超出即拒绝。
	// MaxResultBytes bounds the result size returned to the model.
	MaxResultBytes int `yaml:"max-result-bytes"`
}

// Observability 是 OpenTelemetry/Langfuse 观测配置域；地址与凭据直接写在配置文件中。
// Observability is the OpenTelemetry/Langfuse config domain; endpoint and credentials are plain values.
type Observability struct {
	// Enabled 为 false 时不初始化任何导出器，业务零开销。
	// When Enabled is false no exporter is initialized; business runs at zero cost.
	Enabled bool `yaml:"enabled"`
	// BaseURL 是 Langfuse 服务地址。
	// BaseURL is the Langfuse base URL.
	BaseURL string `yaml:"base-url"`
	// PublicKey 与 SecretKey 是 Langfuse 项目凭据。
	// PublicKey and SecretKey are the Langfuse project credentials.
	PublicKey string `yaml:"public-key"`
	SecretKey string `yaml:"secret-key"`
	// IngestionVersion 为 "4" 时携带 Langfuse v4 ingestion header。
	// IngestionVersion "4" adds the Langfuse v4 ingestion header.
	IngestionVersion string `yaml:"ingestion-version"`
	// ExportRawContent 控制是否上传消息正文；默认开启，关闭后 span 结构保留、
	// 正文替换为脱敏标记。
	// ExportRawContent controls whether message bodies are exported; it is on by
	// default, and disabling it keeps the span shape while replacing content
	// with a redaction marker.
	ExportRawContent bool `yaml:"export-raw-content"`
	// UserIDSecret 是生成发送者与账号假名的 HMAC 密钥；为空时不导出用户维度。
	// UserIDSecret keys the HMAC pseudonyms of senders and accounts; empty omits
	// the user dimension from traces.
	UserIDSecret string `yaml:"user-id-secret"`
	// RawIdentifiers 为 true 时直接导出原始 QQ 号，便于对照真实用户调试；
	// 默认 false，使用 HMAC 假名。
	// RawIdentifiers exports raw QQ numbers for debugging; false keeps the
	// default HMAC pseudonyms.
	RawIdentifiers bool `yaml:"raw-identifiers"`
	// Environment 标记部署环境（如 local/prod），为空时不写资源属性。
	// Environment labels the deployment; empty omits the resource attribute.
	Environment string `yaml:"environment"`
}

// Config 是解析完成的运行时配置模型。
type Config struct {
	Onebot        Onebot        `yaml:"onebot"`
	Server        Server        `yaml:"server"`
	Log           Log           `yaml:"log"`
	Echo          Echo          `yaml:"echo"`
	Database      Database      `yaml:"database"`
	Worker        Worker        `yaml:"worker"`
	AI            AI            `yaml:"ai"`
	Agent         Agent         `yaml:"agent"`
	Tools         Tools         `yaml:"tools"`
	Intent        Intent        `yaml:"intent"`
	Observability Observability `yaml:"observability"`
}

// defaults 返回内置默认值。
func defaults() Config {
	return Config{
		Onebot: Onebot{
			Endpoints:        []OnebotEndpoint{{URL: "ws://127.0.0.1:3001"}},
			CallTimeout:      Duration(10 * time.Second),
			HeartbeatTimeout: Duration(60 * time.Second),
			Reconnect:        Reconnect{Initial: Duration(time.Second), Max: Duration(30 * time.Second)},
		},
		Server: Server{Listen: "127.0.0.1:5080", Enabled: true},
		Log:    Log{Level: "info"},
		Echo:   Echo{Enabled: true, Prefix: "/echo"},
		Database: Database{
			MaxConns: 10,
		},
		Worker: Worker{
			Concurrency:    4,
			MaxPendingJobs: 1000,
			PollInterval:   Duration(200 * time.Millisecond),
		},
		AI: AI{
			GenerationTimeout: Duration(25 * time.Second),
			MaxOutputTokens:   400,
		},
		Agent: Agent{
			MaxSteps:       6,
			MaxToolCalls:   5,
			MaxRunDuration: Duration(30 * time.Second),
			Sending: Sending{
				MaxMessagesPerRun:   3,
				MaxCharsPerMessage:  500,
				MaxTotalCharsPerRun: 1500,
			},
		},
		Tools: Tools{
			Timeout:        Duration(5 * time.Second),
			MaxResultBytes: 8192,
		},
		Observability: Observability{
			ExportRawContent: true,
		},
		Intent: Intent{
			Provider:              "jev",
			Endpoint:              "https://api.typesafe.ai/v1/systemone",
			Model:                 "jev-latest",
			Timeout:               Duration(3 * time.Second),
			ActiveWindow:          Duration(180 * time.Second),
			ContinuationThreshold: 0.80,
			InterjectionThreshold: 0.95,
			AddressedOtherMax:     0.60,
			HistoryLimit:          30,
		},
	}
}

// Overrides 是命令行 Flags 的显式覆盖，优先级最高。
type Overrides struct {
	Listen   string
	LogLevel string
}

// Options 是装载配置的输入。
type Options struct {
	File      string
	Overrides Overrides
	// LookupEnv 用于测试注入；为空时使用 os.LookupEnv。
	LookupEnv func(string) (string, bool)
}

// Load 按优先级装配配置并校验。
func Load(opts Options) (*Config, error) {
	lookup := opts.LookupEnv
	if lookup == nil {
		lookup = os.LookupEnv
	}
	cfg := defaults()
	if opts.File != "" {
		data, err := os.ReadFile(opts.File)
		if err != nil {
			return nil, fmt.Errorf("config: 读取配置文件 %s 失败: %w", opts.File, err)
		}
		if err := yaml.Unmarshal(data, &cfg); err != nil {
			return nil, fmt.Errorf("config: 解析配置文件 %s 失败: %w", opts.File, err)
		}
	}
	applyEnv(&cfg, lookup)
	applyOverrides(&cfg, opts.Overrides)
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return &cfg, nil
}

func applyEnv(cfg *Config, lookup func(string) (string, bool)) {
	if v, ok := lookup("NAGI_SERVER_LISTEN"); ok && strings.TrimSpace(v) != "" {
		cfg.Server.Listen = strings.TrimSpace(v)
	}
	if v, ok := lookup("NAGI_LOG_LEVEL"); ok && strings.TrimSpace(v) != "" {
		cfg.Log.Level = strings.TrimSpace(v)
	}
	// 数据库连接串由部署注入，避免把密码写进镜像或 ConfigMap。
	// Database DSN is injected by the deployment so passwords never live in the image.
	if v, ok := lookup("NAGI_DATABASE_DSN"); ok && strings.TrimSpace(v) != "" {
		cfg.Database.DSN = strings.TrimSpace(v)
	}
	// The pseudonym key is a runtime secret; the environment variable wins over
	// the config file so deployments never have to store it on disk.
	// 假名密钥属于运行时密钥；环境变量优先于配置文件，部署时无需落盘。
	if v, ok := lookup("NAGI_OBSERVABILITY_USER_ID_SECRET"); ok && strings.TrimSpace(v) != "" {
		cfg.Observability.UserIDSecret = strings.TrimSpace(v)
	}
	// Raw identifiers are a per-deployment debugging switch; an unparsable value
	// leaves the file setting untouched instead of silently relaxing privacy.
	// 原始标识属于按部署切换的调试开关；无法解析时保留文件配置，
	// 不因拼写错误悄悄放宽隐私设置。
	if v, ok := lookup("NAGI_OBSERVABILITY_RAW_IDENTIFIERS"); ok && strings.TrimSpace(v) != "" {
		if enabled, err := strconv.ParseBool(strings.TrimSpace(v)); err == nil {
			cfg.Observability.RawIdentifiers = enabled
		}
	}
}

func applyOverrides(cfg *Config, o Overrides) {
	if o.Listen != "" {
		cfg.Server.Listen = o.Listen
	}
	if o.LogLevel != "" {
		cfg.Log.Level = o.LogLevel
	}
}

// Validate 校验配置，并规范化 OneBot URL 的协议头（http/https 视作 ws/wss）。
// replyProviderID mirrors the reply tool provider id. Config validation must not
// import the tool package, which would invert the dependency direction.
// replyProviderID 与回复工具提供方 ID 保持一致；配置校验不能导入工具包，
// 否则会颠倒依赖方向。
const replyProviderID = "builtin.reply"

// containsString reports whether list holds want.
// containsString 报告列表中是否包含目标值。
func containsString(list []string, want string) bool {
	for _, item := range list {
		if item == want {
			return true
		}
	}
	return false
}

func (c *Config) Validate() error {
	if err := c.Onebot.normalizeEndpoints(); err != nil {
		return err
	}

	if c.Onebot.CallTimeout.D() <= 0 {
		return fmt.Errorf("config: onebot.call-timeout 必须为正数")
	}
	if c.Onebot.Reconnect.Initial.D() <= 0 {
		return fmt.Errorf("config: onebot.reconnect.initial 必须为正数")
	}
	if c.Onebot.Reconnect.Max.D() < c.Onebot.Reconnect.Initial.D() {
		return fmt.Errorf("config: onebot.reconnect.max 不得小于 initial")
	}

	if c.Server.Enabled {
		host, port, err := net.SplitHostPort(c.Server.Listen)
		if err != nil {
			return fmt.Errorf("config: server.listen 无效: %w", err)
		}
		if host == "" {
			return fmt.Errorf("config: server.listen 需要显式主机地址；默认仅监听本机应使用 127.0.0.1")
		}
		portNum, err := strconv.Atoi(port)
		if err != nil || portNum <= 0 || portNum > 65535 {
			return fmt.Errorf("config: server.listen 端口无效: %s", port)
		}
		if strings.TrimSpace(c.Server.AdminToken) == "" && !isLoopbackHost(host) {
			return fmt.Errorf("config: server.listen 非本机监听（%s）时必须配置 server.admin-token", host)
		}
	}

	if _, err := logging.ParseLevel(c.Log.Level); err != nil {
		return err
	}
	if c.Echo.Enabled && strings.TrimSpace(c.Echo.Prefix) == "" {
		return fmt.Errorf("config: echo.enabled 时 echo.prefix 不能为空")
	}
	if c.Database.MaxConns <= 0 {
		return fmt.Errorf("config: database.max-conns 必须为正数")
	}
	if c.Worker.Concurrency <= 0 {
		return fmt.Errorf("config: worker.concurrency 必须为正数")
	}
	if c.Worker.MaxPendingJobs <= 0 {
		return fmt.Errorf("config: worker.max-pending-jobs 必须为正数")
	}
	if c.Worker.PollInterval.D() <= 0 {
		return fmt.Errorf("config: worker.poll-interval 必须为正数")
	}
	if c.AI.Enabled {
		// AI replies are dispatched by the intent worker, so generation alone
		// never sends anything. Fail early instead of silently degrading.
		// AI 回复由意图决策流水线触发；只开启生成不会发出任何消息，故直接报错而非静默降级。
		if !c.Intent.Enabled {
			return fmt.Errorf("config: ai.enabled 必须同时设置 intent.enabled=true，否则消息只入库不会回复")
		}
		if strings.TrimSpace(c.AI.BaseURL) == "" {
			return fmt.Errorf("config: ai.enabled 时 ai.base-url 不能为空")
		}
		if strings.TrimSpace(c.AI.Model) == "" {
			return fmt.Errorf("config: ai.enabled 时 ai.model 不能为空")
		}
		if strings.TrimSpace(c.AI.APIKey) == "" {
			return fmt.Errorf("config: ai.enabled 时 ai.api-key 不能为空")
		}
		if c.AI.GenerationTimeout.D() <= 0 || c.AI.MaxOutputTokens <= 0 {
			return fmt.Errorf("config: ai 超时与输出上限必须为正数")
		}
	}
	if c.Agent.MaxSteps <= 0 {
		return fmt.Errorf("config: agent.max-steps 必须为正数")
	}
	if c.Agent.MaxToolCalls <= 0 {
		return fmt.Errorf("config: agent.max-tool-calls 必须为正数")
	}
	if c.Agent.MaxRunDuration.D() <= 0 {
		return fmt.Errorf("config: agent.max-run-duration 必须为正数")
	}
	if c.Agent.Sending.MaxMessagesPerRun <= 0 || c.Agent.Sending.MaxCharsPerMessage <= 0 ||
		c.Agent.Sending.MaxTotalCharsPerRun <= 0 {
		return fmt.Errorf("config: agent.sending 的各项上限必须为正数")
	}
	if c.Agent.Sending.MaxCharsPerMessage > c.Agent.Sending.MaxTotalCharsPerRun {
		return fmt.Errorf("config: agent.sending.max-chars-per-message 不得大于 max-total-chars-per-run")
	}
	if c.Tools.AllowSendCurrent && !c.Tools.AllowSideEffects {
		return fmt.Errorf("config: tools.allow-send-current 需要同时打开 tools.allow-side-effects")
	}
	if c.Tools.AllowSendCurrent && !containsString(c.Tools.Enabled, replyProviderID) {
		return fmt.Errorf("config: tools.allow-send-current 需要 %s 出现在 tools.enabled 中", replyProviderID)
	}
	if c.Tools.Timeout.D() <= 0 || c.Tools.MaxResultBytes <= 0 {
		return fmt.Errorf("config: tools.timeout 与 tools.max-result-bytes 必须为正数")
	}
	if c.Intent.Enabled {
		if c.Intent.Provider != "jev" {
			return fmt.Errorf("config: intent.provider 仅支持 jev")
		}
		if strings.TrimSpace(c.Intent.Endpoint) == "" || strings.TrimSpace(c.Intent.Model) == "" {
			return fmt.Errorf("config: intent.enabled 时 endpoint 与 model 不能为空")
		}
		if c.Intent.Timeout.D() <= 0 {
			return fmt.Errorf("config: intent.timeout 必须为正数")
		}
		if c.Intent.ContinuationThreshold <= 0 || c.Intent.ContinuationThreshold > 1 ||
			c.Intent.InterjectionThreshold <= 0 || c.Intent.InterjectionThreshold > 1 ||
			c.Intent.AddressedOtherMax <= 0 || c.Intent.AddressedOtherMax > 1 {
			return fmt.Errorf("config: intent 阈值必须在 (0,1] 区间")
		}
	}
	return nil
}

// normalizeEndpoints 校验端点列表：至少一个端点，统一 ws/wss 协议头，
// 拒绝重复端点与非法 expect-qq。
// normalizeEndpoints validates the endpoint list: at least one endpoint,
// normalized ws/wss scheme, no duplicates and a non-negative expect-qq.
func (o *Onebot) normalizeEndpoints() error {
	if len(o.Endpoints) == 0 {
		return fmt.Errorf("config: onebot.endpoints 至少需要配置一个 SnowLuma OneBot 端点")
	}
	seen := make(map[string]struct{}, len(o.Endpoints))
	for i := range o.Endpoints {
		endpoint := &o.Endpoints[i]
		normalized, err := normalizeWSURL(endpoint.URL)
		if err != nil {
			return fmt.Errorf("config: onebot.endpoints[%d].url 无效: %w", i, err)
		}
		endpoint.URL = normalized
		if _, ok := seen[normalized]; ok {
			return fmt.Errorf("config: onebot.endpoints[%d] 与已有端点重复: %s", i, normalized)
		}
		seen[normalized] = struct{}{}
		if endpoint.ExpectQQ < 0 {
			return fmt.Errorf("config: onebot.endpoints[%d].expect-qq 不能为负数", i)
		}
	}
	return nil
}

func isLoopbackHost(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func normalizeWSURL(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", fmt.Errorf("不能为空")
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "", err
	}
	switch strings.ToLower(u.Scheme) {
	case "ws", "wss":
	case "http":
		u.Scheme = "ws"
	case "https":
		u.Scheme = "wss"
	default:
		return "", fmt.Errorf("协议 %q 不受支持，请使用 ws:// 或 wss://", u.Scheme)
	}
	if u.Host == "" {
		return "", fmt.Errorf("缺少主机地址")
	}
	return u.String(), nil
}

// compile-time 保证 slog 被用于校验而非其他用途
var _ = slog.LevelInfo
