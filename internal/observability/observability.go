// Package observability sets up OpenTelemetry tracing with an optional
// Langfuse OTLP/HTTP exporter; telemetry failures never block the chat pipeline.
// observability 包初始化 OpenTelemetry 追踪与可选的 Langfuse OTLP/HTTP 导出器；
// 观测故障绝不阻断聊天主链路。
package observability

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"strings"
	"sync/atomic"
	"unicode/utf8"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/propagation"
	sdkresource "go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
	"go.opentelemetry.io/otel/trace"

	"github.com/HappyLadySauceM/QQBot/pkg/version"
)

// Config is the resolved telemetry configuration.
// Config 是解析完成的观测配置。
type Config struct {
	BaseURL          string
	PublicKey        string
	SecretKey        string
	IngestionVersion string
	ExportRawContent bool
}

// exportRawContent gates span attributes that carry message bodies. It is off
// unless the operator sets observability.export-raw-content.
// exportRawContent 控制 span 是否携带消息正文；仅当显式开启时才记录。
var exportRawContent atomic.Bool

// maxContentRunes bounds one exported body so a long reply cannot bloat a trace.
// maxContentRunes 限制单段导出正文长度，避免超长回复撑大 trace。
const maxContentRunes = 8192

// RecordContent attaches a raw body to a span when content export is enabled.
// RecordContent 在开启正文导出时把原始内容写入 span。
func RecordContent(span trace.Span, key, value string) {
	if span == nil || value == "" || !exportRawContent.Load() {
		return
	}
	if utf8.RuneCountInString(value) > maxContentRunes {
		value = string([]rune(value)[:maxContentRunes]) + "…(truncated)"
	}
	span.SetAttributes(attribute.String(key, value))
}

// ShutdownFunc flushes and shuts down the telemetry pipeline.
// ShutdownFunc 刷新并关闭观测管线。
type ShutdownFunc func(context.Context) error

// Setup installs the global tracer provider; without credentials it keeps the
// no-op default so business code can always use the tracer API safely.
// Setup 安装全局 tracer provider；缺少凭据时保持 no-op 默认，
// 业务代码可始终安全使用 tracer API。
func Setup(ctx context.Context, cfg Config, logger *slog.Logger) (ShutdownFunc, error) {
	if logger == nil {
		return nil, fmt.Errorf("observability: logger required")
	}
	exportRawContent.Store(cfg.ExportRawContent)
	if cfg.BaseURL == "" || cfg.PublicKey == "" || cfg.SecretKey == "" {
		logger.Warn("observability enabled but langfuse credentials missing; telemetry stays local no-op")
		return func(context.Context) error { return nil }, nil
	}
	endpoint, err := url.Parse(strings.TrimRight(cfg.BaseURL, "/") + "/api/public/otel/v1/traces")
	if err != nil {
		return nil, fmt.Errorf("observability: parse langfuse endpoint: %w", err)
	}
	// WithHeaders replaces the whole map, so both headers are set in one call.
	// WithHeaders 是整表覆盖，必须一次写全，否则后一次会丢掉 Authorization。
	headers := map[string]string{
		"Authorization": "Basic " + base64.StdEncoding.EncodeToString(
			[]byte(cfg.PublicKey+":"+cfg.SecretKey)),
	}
	if cfg.IngestionVersion != "" {
		headers["x-langfuse-ingestion-version"] = cfg.IngestionVersion
	}
	opts := []otlptracehttp.Option{
		otlptracehttp.WithEndpoint(endpoint.Host),
		otlptracehttp.WithURLPath(endpoint.Path),
		otlptracehttp.WithHeaders(headers),
	}
	if endpoint.Scheme == "http" {
		opts = append(opts, otlptracehttp.WithInsecure())
	}
	exporter, err := otlptracehttp.New(ctx, opts...)
	if err != nil {
		return nil, fmt.Errorf("observability: create exporter: %w", err)
	}
	resource, err := sdkresource.Merge(sdkresource.Default(), sdkresource.NewWithAttributes(
		semconv.SchemaURL,
		semconv.ServiceName("qqbot"),
		semconv.ServiceVersion(version.Version),
	))
	if err != nil {
		return nil, fmt.Errorf("observability: build resource: %w", err)
	}
	provider := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exporter),
		sdktrace.WithResource(resource),
	)
	otel.SetTracerProvider(provider)
	otel.SetTextMapPropagator(propagation.TraceContext{})
	logger.Info("langfuse telemetry enabled", "endpoint_host", endpoint.Host)
	return func(ctx context.Context) error {
		// Telemetry shutdown errors are logged upstream, never fatal.
		// 观测关闭错误仅在上游记录日志，不视为致命。
		return errors.Join(provider.Shutdown(ctx))
	}, nil
}
