package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLoadUsesDefaultsWithoutFileOrEnv(t *testing.T) {
	// 验证无文件、无环境变量时返回内置默认值，包括默认端点。
	cfg, err := Load(Options{LookupEnv: func(string) (string, bool) { return "", false }})
	if err != nil {
		t.Fatalf("load defaults: %v", err)
	}
	if len(cfg.Onebot.Endpoints) != 1 || cfg.Onebot.Endpoints[0].URL != "ws://127.0.0.1:3001" {
		t.Fatalf("unexpected default endpoints: %+v", cfg.Onebot.Endpoints)
	}
	if cfg.Onebot.CallTimeout.D() != 10*time.Second {
		t.Fatalf("default call timeout = %s", cfg.Onebot.CallTimeout.D())
	}
	if cfg.Server.Listen != "127.0.0.1:5080" || !cfg.Server.Enabled {
		t.Fatalf("unexpected default server: %+v", cfg.Server)
	}
	if cfg.Log.Level != "info" {
		t.Fatalf("default log level = %q", cfg.Log.Level)
	}
	if !cfg.Echo.Enabled || cfg.Echo.Prefix != "/echo" {
		t.Fatalf("unexpected default echo: %+v", cfg.Echo)
	}
	// The outbound budget must have a usable default even without a config file.
	// 即使没有配置文件，出站预算也必须有可用默认值。
	if cfg.Agent.Sending.MaxMessagesPerRun != 3 ||
		cfg.Agent.Sending.MaxCharsPerMessage != 500 ||
		cfg.Agent.Sending.MaxTotalCharsPerRun != 1500 {
		t.Fatalf("unexpected default sending budget: %+v", cfg.Agent.Sending)
	}
}

func TestLoadAppliesYamlThenOverrides(t *testing.T) {
	// 验证优先级：YAML < 显式覆盖。
	file := writeTempConfig(t, "onebot:\n  call-timeout: 3s\nserver:\n  listen: 127.0.0.1:6000\nlog:\n  level: debug\n")
	cfg, err := Load(Options{
		File:      file,
		LookupEnv: noEnv,
		Overrides: Overrides{Listen: "127.0.0.1:7000"},
	})
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.Onebot.CallTimeout.D() != 3*time.Second {
		t.Fatalf("yaml duration = %s", cfg.Onebot.CallTimeout.D())
	}
	if cfg.Server.Listen != "127.0.0.1:7000" {
		t.Fatalf("override listen = %q", cfg.Server.Listen)
	}
	if cfg.Log.Level != "debug" {
		t.Fatalf("yaml log level = %q", cfg.Log.Level)
	}
}

func TestValidateNormalizesHTTPSchemeToWS(t *testing.T) {
	// 验证 http(s) 协议头被规范化为 ws(s)。
	file := writeTempConfig(t, "onebot:\n  endpoints:\n    - url: http://127.0.0.1:3001\n")
	cfg, err := Load(Options{File: file, LookupEnv: noEnv})
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.Onebot.Endpoints[0].URL != "ws://127.0.0.1:3001" {
		t.Fatalf("normalized url = %q", cfg.Onebot.Endpoints[0].URL)
	}
}

func TestLoadRejectsUnsupportedScheme(t *testing.T) {
	// 验证非法协议的端点导致装载失败。
	file := writeTempConfig(t, "onebot:\n  endpoints:\n    - url: ftp://example.com\n")
	if _, err := Load(Options{File: file, LookupEnv: noEnv}); err == nil {
		t.Fatal("expected error for unsupported scheme")
	}
}

func TestValidateRejectsEmptyEndpoints(t *testing.T) {
	// 验证显式清空端点列表时报错：没有端点就没有账号。
	file := writeTempConfig(t, "onebot:\n  endpoints: []\n")
	if _, err := Load(Options{File: file, LookupEnv: noEnv}); err == nil {
		t.Fatal("expected error for empty endpoints")
	}
}

func TestLoadRejectsListenWithoutHost(t *testing.T) {
	// 验证未写明主机地址的监听地址被拒绝。
	if _, err := Load(Options{Overrides: Overrides{Listen: ":5080"}}); err == nil {
		t.Fatal("expected error for listen without host")
	}
}

func TestLoadRejectsNonLoopbackWithoutAdminToken(t *testing.T) {
	// 验证非回环监听且未配置 admin-token 时启动失败；配置 Token 后通过。
	cfg, err := Load(Options{LookupEnv: func(string) (string, bool) { return "", false }})
	if err != nil {
		t.Fatalf("load defaults: %v", err)
	}
	cfg.Server.Listen = "0.0.0.0:5080"
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected error for non-loopback listen without admin token")
	}
	cfg.Server.AdminToken = "secret"
	if err := cfg.Validate(); err != nil {
		t.Fatalf("validate with token: %v", err)
	}
}

func TestLoadRejectsMissingExplicitFile(t *testing.T) {
	// 验证显式指定的配置文件缺失时报错。
	if _, err := Load(Options{File: "does/not/exist.yaml"}); err == nil {
		t.Fatal("expected error for missing explicit file")
	}
}

func TestLoadRejectsAIEnabledWithoutIntent(t *testing.T) {
	// 验证只开 AI 不开意图流水线时启动失败，避免消息只入库不回复。
	file := writeTempConfig(t, "ai:\n  enabled: true\n  base-url: https://llm.example/v1\n  api-key: test-key\n  model: test-model\nintent:\n  enabled: false\n")
	if _, err := Load(Options{File: file, LookupEnv: noEnv}); err == nil {
		t.Fatal("expected error for ai.enabled without intent.enabled")
	}
}

func TestLoadAcceptsAIEnabledWithIntent(t *testing.T) {
	// 验证 AI 与意图流水线同时开启、且密钥直接在配置文件中时通过校验。
	file := writeTempConfig(t, "ai:\n  enabled: true\n  base-url: https://llm.example/v1\n  api-key: test-key\n  model: test-model\nintent:\n  enabled: true\n")
	cfg, err := Load(Options{File: file, LookupEnv: noEnv})
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if !cfg.AI.Enabled || !cfg.Intent.Enabled {
		t.Fatalf("unexpected flags: ai=%v intent=%v", cfg.AI.Enabled, cfg.Intent.Enabled)
	}
	if cfg.AI.APIKey != "test-key" {
		t.Fatalf("api key = %q", cfg.AI.APIKey)
	}
}

func TestLoadRejectsAIEnabledWithoutAPIKey(t *testing.T) {
	// 验证 ai.enabled 但未填写 api-key 时启动失败，而不是运行期才发现 401。
	file := writeTempConfig(t, "ai:\n  enabled: true\n  base-url: https://llm.example/v1\n  model: test-model\nintent:\n  enabled: true\n")
	if _, err := Load(Options{File: file, LookupEnv: noEnv}); err == nil {
		t.Fatal("expected error for ai.enabled without api-key")
	}
}

func TestLoadResolvesMultipleEndpoints(t *testing.T) {
	// 验证多端点、明文凭据、协议头归一化与 expect-qq 都被正确装载。
	file := writeTempConfig(t, `onebot:
  endpoints:
    - url: http://127.0.0.1:3001
      access-token: secret-a
      expect-qq: 10001
    - url: ws://127.0.0.1:3002
`)
	cfg, err := Load(Options{File: file, LookupEnv: noEnv})
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(cfg.Onebot.Endpoints) != 2 {
		t.Fatalf("endpoints = %d, want 2", len(cfg.Onebot.Endpoints))
	}
	first := cfg.Onebot.Endpoints[0]
	if first.URL != "ws://127.0.0.1:3001" || first.AccessToken != "secret-a" || first.ExpectQQ != 10001 {
		t.Fatalf("unexpected first endpoint: %+v", first)
	}
	if cfg.Onebot.Endpoints[1].AccessToken != "" {
		t.Fatalf("second endpoint token = %q", cfg.Onebot.Endpoints[1].AccessToken)
	}
}

func TestLoadRejectsDuplicateEndpoints(t *testing.T) {
	// 验证重复端点会让两个账号争用同一连接，必须启动失败。
	file := writeTempConfig(t, `onebot:
  endpoints:
    - url: ws://127.0.0.1:3001
    - url: http://127.0.0.1:3001
`)
	if _, err := Load(Options{File: file, LookupEnv: noEnv}); err == nil {
		t.Fatal("expected error for duplicate endpoints")
	}
}

func TestLoadRejectsNegativeExpectQQ(t *testing.T) {
	// 验证非法的预期 QQ 号被拒绝。
	file := writeTempConfig(t, `onebot:
  endpoints:
    - url: ws://127.0.0.1:3001
      expect-qq: -1
`)
	if _, err := Load(Options{File: file, LookupEnv: noEnv}); err == nil {
		t.Fatal("expected error for negative expect-qq")
	}
}

func writeTempConfig(t *testing.T, content string) string {
	t.Helper()
	file := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(file, []byte(content), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return file
}

func noEnv(string) (string, bool) { return "", false }

func TestLoadAppliesRawIdentifiersFromEnv(t *testing.T) {
	// 验证环境变量可开启原始标识，非法值回退到文件配置。
	file := writeTempConfig(t, "observability:\n  raw-identifiers: false\n")
	cfg, err := Load(Options{File: file, LookupEnv: func(key string) (string, bool) {
		if key == "NAGI_OBSERVABILITY_RAW_IDENTIFIERS" {
			return "true", true
		}
		return "", false
	}})
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if !cfg.Observability.RawIdentifiers {
		t.Fatal("env must enable raw identifiers")
	}

	cfg, err = Load(Options{File: file, LookupEnv: func(key string) (string, bool) {
		if key == "NAGI_OBSERVABILITY_RAW_IDENTIFIERS" {
			return "not-a-bool", true
		}
		return "", false
	}})
	if err != nil {
		t.Fatalf("load with invalid env: %v", err)
	}
	if cfg.Observability.RawIdentifiers {
		t.Fatal("unparsable env must keep the file setting")
	}
}

func TestLoadDatabaseDSNFromEnv(t *testing.T) {
	// 验证 NAGI_DATABASE_DSN 覆盖 YAML 中的 database.dsn。
	file := writeTempConfig(t, "database:\n  dsn: \"postgres://file@127.0.0.1:5432/file?sslmode=disable\"\n")
	env := func(key string) (string, bool) {
		if key == "NAGI_DATABASE_DSN" {
			return "postgres://env@postgresql.infrastructure-dev.svc.cluster.local:5432/nagi?sslmode=disable", true
		}
		return "", false
	}
	cfg, err := Load(Options{File: file, LookupEnv: env})
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.Database.DSN != "postgres://env@postgresql.infrastructure-dev.svc.cluster.local:5432/nagi?sslmode=disable" {
		t.Fatalf("dsn = %q", cfg.Database.DSN)
	}
}

func TestLoadDatabaseDSNEmptyEnvKeepsFileValue(t *testing.T) {
	file := writeTempConfig(t, "database:\n  dsn: \"postgres://file@127.0.0.1:5432/file?sslmode=disable\"\n")
	env := func(key string) (string, bool) {
		if key == "NAGI_DATABASE_DSN" {
			return "   ", true
		}
		return "", false
	}
	cfg, err := Load(Options{File: file, LookupEnv: env})
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.Database.DSN != "postgres://file@127.0.0.1:5432/file?sslmode=disable" {
		t.Fatalf("dsn = %q", cfg.Database.DSN)
	}
}

func TestLoadRejectsContradictorySendingBudget(t *testing.T) {
	// 验证单条字符上限大于回合总数时启动失败：该组合会让任何正长度消息都自相矛盾。
	file := writeTempConfig(t, "agent:\n  sending:\n    max-chars-per-message: 2000\n    max-total-chars-per-run: 1500\n")
	if _, err := Load(Options{File: file, LookupEnv: noEnv}); err == nil {
		t.Fatal("per-message limit above the run total must be rejected")
	}
}

func TestLoadRejectsNonPositiveSendingBudget(t *testing.T) {
	// 验证显式写入的零值不会被默认值掩盖，而是在启动阶段报错。
	file := writeTempConfig(t, "agent:\n  sending:\n    max-messages-per-run: 0\n")
	if _, err := Load(Options{File: file, LookupEnv: noEnv}); err == nil {
		t.Fatal("a zero message budget must be rejected")
	}
}

func TestLoadReadsSendingBudgetFromYaml(t *testing.T) {
	// 验证 YAML 中的发送预算被读取，未设置的相邻字段仍保留默认值。
	file := writeTempConfig(t, "agent:\n  sending:\n    max-messages-per-run: 5\n")
	cfg, err := Load(Options{File: file, LookupEnv: noEnv})
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.Agent.Sending.MaxMessagesPerRun != 5 ||
		cfg.Agent.Sending.MaxCharsPerMessage != 500 ||
		cfg.Agent.Sending.MaxTotalCharsPerRun != 1500 {
		t.Fatalf("sending budget = %+v", cfg.Agent.Sending)
	}
}

func TestValidateAllowsDedicatedReplyWithoutOtherSideEffects(t *testing.T) {
	file := writeTempConfig(t, "tools:\n  allow-send-current: true\n  enabled: [builtin.reply]\n")
	if _, err := Load(Options{File: file, LookupEnv: noEnv}); err != nil {
		t.Fatal(err)
	}
}

func TestValidateRejectsSendCurrentWithoutProvider(t *testing.T) {
	// 验证打开 allow-send-current 但 enabled 缺少 builtin.reply 时启动失败。
	file := writeTempConfig(t, "tools:\n  allow-send-current: true\n  allow-side-effects: true\n  enabled: [builtin.time]\n")
	if _, err := Load(Options{File: file, LookupEnv: noEnv}); err == nil {
		t.Fatal("expected error when builtin.reply is not enabled")
	}
}

func TestValidateAcceptsSendCurrentFullyEnabled(t *testing.T) {
	// 验证三个条件齐备时可以通过校验。
	file := writeTempConfig(t, "tools:\n  allow-send-current: true\n  allow-side-effects: true\n  enabled: [builtin.reply]\n")
	if _, err := Load(Options{File: file, LookupEnv: noEnv}); err != nil {
		t.Fatalf("load: %v", err)
	}
}
