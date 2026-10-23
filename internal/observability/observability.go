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
	"os"
	"strings"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/propagation"
	sdkresource "go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"

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

// Resolve reads the Langfuse credentials from the configured environment variable
// names; missing values disable the exporter.
// Resolve 从配置的环境变量名读取 Langfuse 凭据；缺失时关闭导出器。
func Resolve(baseURLEnv, publicKeyEnv, secretKeyEnv, ingestionVersion string, exportRawContent bool, lookup func(string) (string, bool)) Config {
	get := func(name string) string {
		if name == "" {
			return ""
		}
		if v, ok := lookup(name); ok {
			return strings.TrimSpace(v)
		}
		return ""
	}
	return Config{
		BaseURL:          get(baseURLEnv),
		PublicKey:        get(publicKeyEnv),
		SecretKey:        get(secretKeyEnv),
		IngestionVersion: ingestionVersion,
		ExportRawContent: exportRawContent,
	}
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
	if cfg.BaseURL == "" || cfg.PublicKey == "" || cfg.SecretKey == "" {
		logger.Warn("observability enabled but langfuse credentials missing; telemetry stays local no-op")
		return func(context.Context) error { return nil }, nil
	}
	endpoint, err := url.Parse(strings.TrimRight(cfg.BaseURL, "/") + "/api/public/otel/v1/traces")
	if err != nil {
		return nil, fmt.Errorf("observability: parse langfuse endpoint: %w", err)
	}
	opts := []otlptracehttp.Option{
		otlptracehttp.WithEndpoint(endpoint.Host),
		otlptracehttp.WithURLPath(endpoint.Path),
		otlptracehttp.WithHeaders(map[string]string{
			"Authorization": "Basic " + base64.StdEncoding.EncodeToString(
				[]byte(cfg.PublicKey+":"+cfg.SecretKey)),
		}),
	}
	if endpoint.Scheme == "http" {
		opts = append(opts, otlptracehttp.WithInsecure())
	}
	if cfg.IngestionVersion != "" {
		opts = append(opts, otlptracehttp.WithHeaders(map[string]string{
			"x-langfuse-ingestion-version": cfg.IngestionVersion,
		}))
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

// ResolveEnv is the default environment lookup for Resolve.
// ResolveEnv 是 Resolve 的默认环境变量查询。
func ResolveEnv(name string) (string, bool) { return os.LookupEnv(name) }
