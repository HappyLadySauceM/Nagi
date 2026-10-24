package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLoadUsesDefaultsWithoutFileOrEnv(t *testing.T) {
	// 验证无文件、无环境变量时返回内置默认值。
	cfg, err := Load(Options{LookupEnv: func(string) (string, bool) { return "", false }})
	if err != nil {
		t.Fatalf("load defaults: %v", err)
	}
	if cfg.Onebot.URL != "ws://127.0.0.1:3001" {
		t.Fatalf("default onebot url = %q", cfg.Onebot.URL)
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
}

func TestLoadAppliesYamlThenEnvThenOverrides(t *testing.T) {
	// 验证优先级：YAML < 环境变量 < 显式覆盖。
	dir := t.TempDir()
	file := filepath.Join(dir, "config.yaml")
	content := "onebot:\n  url: ws://10.0.0.1:9000\n  call-timeout: 3s\nserver:\n  listen: 127.0.0.1:6000\nlog:\n  level: debug\n"
	if err := os.WriteFile(file, []byte(content), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	env := map[string]string{"QQBOT_ONEBOT_URL": "ws://10.0.0.2:9001"}
	cfg, err := Load(Options{
		File:      file,
		LookupEnv: func(key string) (string, bool) { v, ok := env[key]; return v, ok },
		Overrides: Overrides{Listen: "127.0.0.1:7000"},
	})
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.Onebot.URL != "ws://10.0.0.2:9001" {
		t.Fatalf("env should override yaml, url = %q", cfg.Onebot.URL)
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
	cfg, err := Load(Options{Overrides: Overrides{OnebotURL: "http://127.0.0.1:3001"}})
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.Onebot.URL != "ws://127.0.0.1:3001" {
		t.Fatalf("normalized url = %q", cfg.Onebot.URL)
	}
}

func TestLoadRejectsUnsupportedScheme(t *testing.T) {
	// 验证非法协议的 URL 导致装载失败。
	if _, err := Load(Options{Overrides: Overrides{OnebotURL: "ftp://example.com"}}); err == nil {
		t.Fatal("expected error for unsupported scheme")
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
	file := writeTempConfig(t, "ai:\n  enabled: true\n  base-url: https://llm.example/v1\n  model: test-model\nintent:\n  enabled: false\n")
	if _, err := Load(Options{File: file, LookupEnv: noEnv}); err == nil {
		t.Fatal("expected error for ai.enabled without intent.enabled")
	}
}

func TestLoadAcceptsAIEnabledWithIntent(t *testing.T) {
	// 验证 AI 与意图流水线同时开启时通过校验。
	file := writeTempConfig(t, "ai:\n  enabled: true\n  base-url: https://llm.example/v1\n  model: test-model\nintent:\n  enabled: true\n")
	cfg, err := Load(Options{File: file, LookupEnv: noEnv})
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if !cfg.AI.Enabled || !cfg.Intent.Enabled {
		t.Fatalf("unexpected flags: ai=%v intent=%v", cfg.AI.Enabled, cfg.Intent.Enabled)
	}
}

func TestLoadToleratesRemovedLegacyKeys(t *testing.T) {
	// 验证已删除的 coalesce-window / active-window 仍可出现在旧配置文件中而不报错。
	file := writeTempConfig(t, "worker:\n  coalesce-window: 2s\nintent:\n  enabled: true\n  active-window: 180s\n")
	cfg, err := Load(Options{File: file, LookupEnv: noEnv})
	if err != nil {
		t.Fatalf("load with legacy keys: %v", err)
	}
	if !cfg.Intent.Enabled {
		t.Fatal("legacy yaml should still apply overrides")
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
