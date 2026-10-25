// Tests for the no-credential fallback.
// 观测无凭据回退的测试。
package observability

import (
	"context"
	"io"
	"log/slog"
	"testing"

	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// TestSetupWithoutCredentialsIsNoop verifies that missing credentials keep the
// no-op tracer provider and never fail the app.
// TestSetupWithoutCredentialsIsNoop 验证缺少凭据时保持 no-op tracer provider
// 且不会导致应用失败。
func TestSetupWithoutCredentialsIsNoop(t *testing.T) {
	telemetry, err := Setup(context.Background(), Config{}, testLogger())
	if err != nil {
		t.Fatalf("setup without credentials should not fail: %v", err)
	}
	if telemetry.Provider != nil {
		t.Fatal("missing credentials must leave the provider nil")
	}
	if err := telemetry.Shutdown(context.Background()); err != nil {
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

// TestRecordContentRespectsExportSwitch verifies that message bodies only reach
// spans when export-raw-content is enabled.
// TestRecordContentRespectsExportSwitch 验证只有开启正文导出时消息正文才会进入 span。
func TestRecordContentRespectsExportSwitch(t *testing.T) {
	recorder := tracetest.NewSpanRecorder()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
	_, span := provider.Tracer("test").Start(context.Background(), "record")

	exportRawContent.Store(false)
	RecordContent(span, "gen_ai.prompt", "不会导出")
	exportRawContent.Store(true)
	RecordContent(span, "gen_ai.completion", "会导出")
	span.End()

	ended := recorder.Ended()
	if len(ended) != 1 {
		t.Fatalf("ended spans = %d", len(ended))
	}
	attrs := ended[0].Attributes()
	if len(attrs) != 1 || string(attrs[0].Key) != "gen_ai.completion" {
		t.Fatalf("attributes = %v", attrs)
	}
}
