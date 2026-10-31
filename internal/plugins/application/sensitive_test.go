package application

import (
	"context"
	"io"
	"log/slog"
	"strings"
	"testing"

	"github.com/HappyLadySauceM/Nagi/internal/observability"
	"github.com/HappyLadySauceM/Nagi/internal/plugins/contracts"
	"go.opentelemetry.io/otel"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

func TestSensitiveToolReturnsCredentialWithoutExportingContent(t *testing.T) {
	ctx := observability.WithContentState(context.Background())
	previous := observability.ContentEnabled()
	_, err := observability.Setup(ctx, observability.Config{ExportRawContent: true}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	defer observability.Setup(ctx, observability.Config{ExportRawContent: previous}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	recorder := tracetest.NewSpanRecorder()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
	defer provider.Shutdown(ctx)
	old := otel.GetTracerProvider()
	otel.SetTracerProvider(provider)
	defer otel.SetTracerProvider(old)
	scope := Scope{Permissions: map[string]bool{"qq.api.get_credentials": true}, MaxResultBytes: 1024}
	d := contracts.ToolDescriptor{ID: "qq.get_credentials", Permission: "qq.api.get_credentials", Sensitive: true, ReadOnly: true}
	secret := "fixture-secret-cookie"
	out, err := Guard(scope, d, func(context.Context, string) (string, error) { return `{"cookies":"` + secret + `"}`, nil })(ctx, `{}`)
	if err != nil || !strings.Contains(out, secret) {
		t.Fatalf("credential unavailable: %s %v", out, err)
	}
	if !observability.ContentSuppressed(ctx) {
		t.Fatal("run not marked sensitive")
	}
	_, span := provider.Tracer("test").Start(ctx, "later-model-output")
	observability.RecordObservationSecure(ctx, span, "langfuse.observation.output", secret)
	span.End()
	spans := recorder.Ended()
	if len(spans) != 2 {
		t.Fatalf("spans=%d", len(spans))
	}
	for _, span := range spans {
		for _, attr := range span.Attributes() {
			if strings.Contains(attr.Value.AsString(), secret) {
				t.Fatal("credential exported")
			}
		}
	}
}
