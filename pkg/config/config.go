// Package config represents the parsed runtime configuration model.
// Package config 表示解析完成的运行时配置模型及其装载逻辑。
// 优先级：默认值 → YAML → 环境变量（QQBOT_ 前缀）→ 显式覆盖（Flags）。
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

	"github.com/HappyLadySauceM/QQBot/pkg/logging"
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

// Onebot 是 OneBot v11 连接配置域。
type Onebot struct {
	URL         string    `yaml:"url"`
	AccessToken string    `yaml:"access-token"`
	CallTimeout Duration  `yaml:"call-timeout"`
	Reconnect   Reconnect `yaml:"reconnect"`
}

// Server 是管理 HTTP API 配置域。
type Server struct {
	Listen  string `yaml:"listen"`
	Enabled bool   `yaml:"enabled"`
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

// Config 是解析完成的运行时配置模型。
type Config struct {
	Onebot Onebot `yaml:"onebot"`
	Server Server `yaml:"server"`
	Log    Log    `yaml:"log"`
	Echo   Echo   `yaml:"echo"`
}

// defaults 返回内置默认值。
func defaults() Config {
	return Config{
		Onebot: Onebot{
			URL:         "ws://127.0.0.1:3001",
			CallTimeout: Duration(10 * time.Second),
			Reconnect:   Reconnect{Initial: Duration(time.Second), Max: Duration(30 * time.Second)},
		},
		Server: Server{Listen: "127.0.0.1:5080", Enabled: true},
		Log:    Log{Level: "info"},
		Echo:   Echo{Enabled: true, Prefix: "/echo"},
	}
}

// Overrides 是命令行 Flags 的显式覆盖，优先级最高。
type Overrides struct {
	OnebotURL   string
	AccessToken string
	Listen      string
	LogLevel    string
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
	if v, ok := lookup("QQBOT_ONEBOT_URL"); ok && strings.TrimSpace(v) != "" {
		cfg.Onebot.URL = strings.TrimSpace(v)
	}
	if v, ok := lookup("QQBOT_ONEBOT_ACCESS_TOKEN"); ok {
		cfg.Onebot.AccessToken = v
	}
	if v, ok := lookup("QQBOT_SERVER_LISTEN"); ok && strings.TrimSpace(v) != "" {
		cfg.Server.Listen = strings.TrimSpace(v)
	}
	if v, ok := lookup("QQBOT_LOG_LEVEL"); ok && strings.TrimSpace(v) != "" {
		cfg.Log.Level = strings.TrimSpace(v)
	}
}

func applyOverrides(cfg *Config, o Overrides) {
	if o.OnebotURL != "" {
		cfg.Onebot.URL = o.OnebotURL
	}
	if o.AccessToken != "" {
		cfg.Onebot.AccessToken = o.AccessToken
	}
	if o.Listen != "" {
		cfg.Server.Listen = o.Listen
	}
	if o.LogLevel != "" {
		cfg.Log.Level = o.LogLevel
	}
}

// Validate 校验配置，并规范化 OneBot URL 的协议头（http/https 视作 ws/wss）。
func (c *Config) Validate() error {
	normalized, err := normalizeWSURL(c.Onebot.URL)
	if err != nil {
		return fmt.Errorf("config: onebot.url 无效: %w", err)
	}
	c.Onebot.URL = normalized

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
	}

	if _, err := logging.ParseLevel(c.Log.Level); err != nil {
		return err
	}
	if c.Echo.Enabled && strings.TrimSpace(c.Echo.Prefix) == "" {
		return fmt.Errorf("config: echo.enabled 时 echo.prefix 不能为空")
	}
	return nil
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
