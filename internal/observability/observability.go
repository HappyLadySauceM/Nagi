// Package observability sets up OpenTelemetry tracing with an optional
// Langfuse OTLP/HTTP exporter; telemetry failures never block the chat pipeline.
// observability 包初始化 OpenTelemetry 追踪与可选的 Langfuse OTLP/HTTP 导出器；
// 观测故障绝不阻断聊天主链路。
package observability

import (
	"context"
	"encoding/base64"
	"encoding/json"
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
	semconv "go.opentelemetry.io/otel/semconv/v1.37.0"
	"go.opentelemetry.io/otel/trace"

	"github.com/HappyLadySauceM/Nagi/pkg/version"
)

// Config is the resolved telemetry configuration.
// Config 是解析完成的观测配置。
type Config struct {
	BaseURL          string
	PublicKey        string
	SecretKey        string
	IngestionVersion string
	ExportRawContent bool
	// Environment labels the deployment; empty omits the resource attribute.
	// Environment 标记部署环境；为空时不写该资源属性。
	Environment string
}

// exportRawContent gates span attributes that carry message bodies. It follows
// observability.export-raw-content, which is enabled by default.
// exportRawContent 控制 span 是否携带消息正文，跟随 observability.export-raw-content，
// 该开关默认开启。
var exportRawContent atomic.Bool

// maxContentRunes bounds one exported body so a long reply cannot bloat a trace.
// maxContentRunes 限制单段导出正文长度，避免超长回复撑大 trace。
const maxContentRunes = 8192

// MaxObservationBytes bounds one structured input/output payload. Callers that
// build JSON themselves should stay below it so the payload is not replaced by
// a truncation marker.
// MaxObservationBytes 限制单份结构化输入/输出载荷的大小。自行构造 JSON 的调用方
// 应保持在此预算内，避免载荷被整体替换为截断标记。
const MaxObservationBytes = 8192

// ContentEnabled reports whether raw bodies may be exported, letting callers
// skip building a payload that would be discarded.
// ContentEnabled 报告是否允许导出原始正文，供调用方跳过注定被丢弃的构造。
func ContentEnabled() bool { return exportRawContent.Load() }

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

// RecordObservation attaches a Langfuse-renderable input or output payload to
// one span. Langfuse only shows content from langfuse.observation.input and
// langfuse.observation.output, so without this a span looks empty even though
// it carries structured attributes.
// RecordObservation 为单个 span 写入 Langfuse 可渲染的输入或输出。
// Langfuse 只展示 langfuse.observation.input 与 output，缺失时即使 span 带有
// 结构化属性，详情页仍然为空。
func RecordObservation(span trace.Span, key string, value any) {
	if span == nil || !span.IsRecording() {
		return
	}
	if !exportRawContent.Load() {
		span.SetAttributes(attribute.String(key, `{"redacted":true}`))
		return
	}
	payload, err := json.Marshal(value)
	if err != nil {
		// An unrenderable payload must never break the span; the marker keeps
		// the document valid JSON.
		// 无法渲染的载荷绝不能破坏 span；标记保证文档仍是合法 JSON。
		span.SetAttributes(attribute.String(key, `{"unserializable":true}`))
		return
	}
	if len(payload) > MaxObservationBytes {
		// Truncating JSON would produce an invalid document; replace it with a
		// marker that still tells the reader how much was dropped.
		// 截断 JSON 会产生非法文档；改为写入仍能说明丢弃大小的标记。
		payload = []byte(fmt.Sprintf(`{"truncated":true,"bytes":%d}`, len(payload)))
	}
	span.SetAttributes(attribute.String(key, string(payload)))
}

// ShutdownFunc flushes and shuts down the telemetry pipeline.
// ShutdownFunc 刷新并关闭观测管线。
type ShutdownFunc func(context.Context) error

// Telemetry owns the single tracing pipeline of the process. Provider is nil
// when telemetry is disabled or credentials are missing; callers that only emit
// spans keep using the global tracer API. The explicit provider is reserved for
// the future Agent Runtime callbacks and for tests that need a real exporter.
// Telemetry 持有进程内唯一的追踪管线。未启用或缺少凭据时 Provider 为 nil；
// 只创建 span 的调用方继续使用全局 tracer API。显式 Provider 留给后续
// Agent Runtime 的 callback 以及需要真实导出器的测试。
type Telemetry struct {
	Provider *sdktrace.TracerProvider
	Shutdown ShutdownFunc
}

// Setup installs the global tracer provider and returns it for reuse; without
// credentials it keeps the no-op default so business code can always use the
// tracer API safely.
// Setup 安装全局 tracer provider 并返回该实例供复用；缺少凭据时保持 no-op 默认，
// 业务代码可始终安全使用 tracer API。
func Setup(ctx context.Context, cfg Config, logger *slog.Logger) (*Telemetry, error) {
	if logger == nil {
		return nil, fmt.Errorf("observability: logger required")
	}
	exportRawContent.Store(cfg.ExportRawContent)
	if cfg.BaseURL == "" || cfg.PublicKey == "" || cfg.SecretKey == "" {
		logger.Warn("observability enabled but langfuse credentials missing; telemetry stays local no-op")
		return &Telemetry{Shutdown: func(context.Context) error { return nil }}, nil
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
	attrs := []attribute.KeyValue{
		semconv.ServiceName("nagi"),
		semconv.ServiceVersion(version.Version),
	}
	if cfg.Environment != "" {
		attrs = append(attrs, attribute.String("deployment.environment", cfg.Environment))
	}
	resource, err := sdkresource.Merge(sdkresource.Default(), sdkresource.NewWithAttributes(
		semconv.SchemaURL,
		attrs...,
	))
	if err != nil {
		return nil, fmt.Errorf("observability: build resource: %w", err)
	}
	provider := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exporter),
		// Identity attributes must reach every span created after the identity is
		// attached, not only the span that set them, otherwise Langfuse cannot
		// filter those spans by session/user. The session id is only known after
		// persistence, so it stays on the root span and on later spans.
		// 身份属性必须落到身份附加之后创建的每个 span，而不只是最先设置它的 span，
		// 否则 Langfuse 无法按会话/用户过滤这些 span。会话 ID 只在落库后才知道，
		// 因此只出现在根 span 及其后的 span 上。
		sdktrace.WithSpanProcessor(identityProcessor{}),
		sdktrace.WithResource(resource),
	)
	otel.SetTracerProvider(provider)
	otel.SetTextMapPropagator(propagation.TraceContext{})
	logger.Info("langfuse telemetry enabled", "endpoint_host", endpoint.Host)
	return &Telemetry{
		Provider: provider,
		Shutdown: func(ctx context.Context) error {
			// Telemetry shutdown errors are logged upstream, never fatal.
			// 观测关闭错误仅在上游记录日志，不视为致命。
			return errors.Join(provider.Shutdown(ctx))
		},
	}, nil
}
