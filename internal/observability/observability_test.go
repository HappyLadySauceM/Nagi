// Tests for the no-credential fallback.
// 观测无凭据回退的测试。
package observability

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"strings"
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

// TestRecordObservationWritesValidJSON verifies the payload Langfuse renders is
// a JSON document, and that an oversized payload becomes a truncated marker
// instead of invalid JSON.
// TestRecordObservationWritesValidJSON 验证 Langfuse 渲染的载荷是合法 JSON，
// 且超限载荷改为截断标记而不是非法 JSON。
func TestRecordObservationWritesValidJSON(t *testing.T) {
	recorder := tracetest.NewSpanRecorder()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
	_, span := provider.Tracer("test").Start(context.Background(), "observation")

	exportRawContent.Store(true)
	RecordObservation(span, "langfuse.observation.input", map[string]any{"text": "你好"})
	RecordObservation(span, "langfuse.observation.output", map[string]any{
		"text": strings.Repeat("长", maxContentRunes),
	})
	span.End()

	attrs := map[string]string{}
	for _, kv := range recorder.Ended()[0].Attributes() {
		attrs[string(kv.Key)] = kv.Value.AsString()
	}
	for _, key := range []string{"langfuse.observation.input", "langfuse.observation.output"} {
		if !json.Valid([]byte(attrs[key])) {
			t.Fatalf("%s is not valid JSON: %q", key, attrs[key])
		}
	}
	if !strings.Contains(attrs["langfuse.observation.input"], "你好") {
		t.Fatalf("input payload = %q", attrs["langfuse.observation.input"])
	}
	if !strings.Contains(attrs["langfuse.observation.output"], `"truncated":true`) {
		t.Fatalf("oversized output payload = %q", attrs["langfuse.observation.output"])
	}
}

// TestRecordObservationRedactsWithoutContentExport verifies that disabling content
// export keeps a placeholder instead of leaving the observation undefined.
// TestRecordObservationRedactsWithoutContentExport 验证关闭正文导出时写入占位值，
// 而不是让观测内容保持 undefined。
func TestRecordObservationRedactsWithoutContentExport(t *testing.T) {
	recorder := tracetest.NewSpanRecorder()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
	_, span := provider.Tracer("test").Start(context.Background(), "observation")

	exportRawContent.Store(false)
	RecordObservation(span, "langfuse.observation.input", map[string]any{"text": "不应导出"})
	span.End()

	attrs := recorder.Ended()[0].Attributes()
	if len(attrs) != 1 || attrs[0].Value.AsString() != `{"redacted":true}` {
		t.Fatalf("attributes = %v", attrs)
	}
}
