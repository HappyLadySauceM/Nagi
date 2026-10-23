// Tests for telemetry resolution and the no-credential fallback.
// 观测配置解析与无凭据回退的测试。
package observability

import (
	"context"
	"io"
	"log/slog"
	"testing"
)

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// TestResolveReadsConfiguredEnvNames verifies env indirection.
// TestResolveReadsConfiguredEnvNames 验证环境变量间接引用。
func TestResolveReadsConfiguredEnvNames(t *testing.T) {
	env := map[string]string{
		"LANGFUSE_BASE_URL":   "https://langfuse.example.com/",
		"LANGFUSE_PUBLIC_KEY": "pk",
		"LANGFUSE_SECRET_KEY": "sk",
	}
	cfg := Resolve("LANGFUSE_BASE_URL", "LANGFUSE_PUBLIC_KEY", "LANGFUSE_SECRET_KEY", "4", false,
		func(name string) (string, bool) { v, ok := env[name]; return v, ok })
	if cfg.BaseURL != "https://langfuse.example.com/" || cfg.PublicKey != "pk" || cfg.SecretKey != "sk" {
		t.Fatalf("resolved = %+v", cfg)
	}
	if cfg.IngestionVersion != "4" {
		t.Fatalf("ingestion version = %q", cfg.IngestionVersion)
	}
}

// TestSetupWithoutCredentialsIsNoop verifies that missing credentials keep the
// no-op tracer provider and never fail the app.
// TestSetupWithoutCredentialsIsNoop 验证缺少凭据时保持 no-op tracer provider
// 且不会导致应用失败。
func TestSetupWithoutCredentialsIsNoop(t *testing.T) {
	shutdown, err := Setup(context.Background(), Config{}, testLogger())
	if err != nil {
		t.Fatalf("setup without credentials should not fail: %v", err)
	}
	if err := shutdown(context.Background()); err != nil {
		t.Fatalf("noop shutdown: %v", err)
	}
}

// TestSetupRequiresLogger verifies dependency validation.
// TestSetupRequiresLogger 验证依赖校验。
func TestSetupRequiresLogger(t *testing.T) {
	if _, err := Setup(context.Background(), Config{}, nil); err == nil {
		t.Fatal("expected error for nil logger")
	}
}
