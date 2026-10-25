// Tests for trace context persistence and identity pseudonymization.
// 追踪上下文持久化与身份假名化的测试。
package observability

import (
	"context"
	"strings"
	"testing"

	"go.opentelemetry.io/otel"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"
)

// newRecordingProvider installs a recorder-backed provider for the duration of
// one test and returns both the recorder and the sampler-free tracer.
// newRecordingProvider 在单个测试期间安装基于 recorder 的 provider，
// 并返回 recorder 与 tracer。
func newRecordingProvider(t *testing.T) (*tracetest.SpanRecorder, trace.Tracer) {
	t.Helper()
	recorder := tracetest.NewSpanRecorder()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
	previous := otel.GetTracerProvider()
	otel.SetTracerProvider(provider)
	t.Cleanup(func() {
		otel.SetTracerProvider(previous)
		_ = provider.Shutdown(context.Background())
	})
	return recorder, provider.Tracer("test")
}

// TestCurrentRestoreRoundTripKeepsTraceID verifies that a stored traceparent
// re-creates the same trace with a new parent span.
// TestCurrentRestoreRoundTripKeepsTraceID 验证已存储的 traceparent
// 能还原同一 trace 并产生新的父 span。
func TestCurrentRestoreRoundTripKeepsTraceID(t *testing.T) {
	_, tracer := newRecordingProvider(t)
	ctx, span := tracer.Start(context.Background(), "root")
	defer span.End()

	stored := Current(ctx)
	if !stored.Valid() {
		t.Fatalf("stored context invalid: %+v", stored)
	}

	_, child := tracer.Start(stored.Restore(context.Background()), "worker")
	defer child.End()
	if got, want := child.SpanContext().TraceID(), span.SpanContext().TraceID(); got != want {
		t.Fatalf("trace id = %s, want %s", got, want)
	}
	if got, want := child.SpanContext().SpanID(), span.SpanContext().SpanID(); got == want {
		t.Fatal("restored span must not reuse the parent span id")
	}
}

// TestCurrentWithoutRecordingReturnsEmpty verifies the zero value when no span
// is active, so callers persist nothing instead of a bogus header.
// TestCurrentWithoutRecordingReturnsEmpty 验证没有活动 span 时返回零值，
// 调用方不会持久化伪造头部。
func TestCurrentWithoutRecordingReturnsEmpty(t *testing.T) {
	if got := Current(context.Background()); got != (TraceContext{}) {
		t.Fatalf("context = %+v, want zero", got)
	}
}

// TestRestoreRejectsMalformedHeaders verifies that stored garbage never becomes
// a parent context.
// TestRestoreRejectsMalformedHeaders 验证存储的垃圾值不会成为父上下文。
func TestRestoreRejectsMalformedHeaders(t *testing.T) {
	cases := []TraceContext{
		{TraceParent: "not-a-traceparent"},
		{TraceParent: "ff-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01"},
		{TraceParent: "00-00000000000000000000000000000000-00f067aa0ba902b7-01"},
		{TraceParent: "00-4bf92f3577b34da6a3ce929d0e0e4736-0000000000000000-01"},
		{TraceParent: "00-4BF92F3577B34DA6A3CE929D0E0E4736-00F067AA0BA902B7-01"},
	}
	for _, tc := range cases {
		if tc.Valid() {
			t.Fatalf("%+v must be rejected", tc)
		}
		ctx := tc.Restore(context.Background())
		if trace.SpanContextFromContext(ctx).IsValid() {
			t.Fatalf("%+v must not become a parent", tc)
		}
	}
}

// TestTraceStateRoundTripKeepsShortValue verifies that a valid tracestate
// survives persistence and that oversized stored values are dropped while the
// traceparent keeps working.
// TestTraceStateRoundTripKeepsShortValue 验证合法 tracestate 能完整往返，
// 超长存储值被丢弃且 traceparent 仍然生效。
func TestTraceStateRoundTripKeepsShortValue(t *testing.T) {
	_, tracer := newRecordingProvider(t)
	state, err := trace.ParseTraceState("vendor=value")
	if err != nil {
		t.Fatalf("parse tracestate: %v", err)
	}
	parent := trace.NewSpanContext(trace.SpanContextConfig{
		TraceID:    trace.TraceID{1},
		SpanID:     trace.SpanID{1},
		TraceFlags: trace.FlagsSampled,
		TraceState: state,
	})
	ctx, span := tracer.Start(trace.ContextWithSpanContext(context.Background(), parent), "root")
	defer span.End()

	stored := Current(ctx)
	if stored.TraceState != "vendor=value" {
		t.Fatalf("stored tracestate = %q", stored.TraceState)
	}
	restored := trace.SpanContextFromContext(stored.Restore(context.Background()))
	if restored.TraceState().String() != "vendor=value" {
		t.Fatalf("restored tracestate = %q", restored.TraceState().String())
	}

	oversized := TraceContext{TraceParent: stored.TraceParent, TraceState: strings.Repeat("a", maxTraceStateLen+1)}
	restored = trace.SpanContextFromContext(oversized.Restore(context.Background()))
	if !restored.IsValid() {
		t.Fatal("traceparent must stay usable without the oversized tracestate")
	}
	if restored.TraceState().String() != "" {
		t.Fatalf("oversized tracestate must be dropped, got %d chars", len(restored.TraceState().String()))
	}
}

// testSecret is a valid-length key used only by tests.
// testSecret 是仅测试使用的合法长度密钥。
const testSecret = "0123456789abcdef0123456789abcdef"

// TestIdentitiesPseudonymIsStableAndKeyed verifies that pseudonymous ids are
// deterministic per account, distinct per user and never expose the raw number.
// TestIdentitiesPseudonymIsStableAndKeyed 验证假名在账号内稳定、不同用户不同，
// 且不暴露原始 QQ 号。
func TestIdentitiesPseudonymIsStableAndKeyed(t *testing.T) {
	identities := NewIdentities(testSecret, false)
	if identities == nil {
		t.Fatal("pseudonym mode must be built from a non-empty secret")
	}
	first := identities.UserID(100, 200)
	if first == "" || strings.Contains(first, "200") {
		t.Fatalf("user id leaks the raw id: %q", first)
	}
	if got := identities.UserID(100, 200); got != first {
		t.Fatalf("user id must be stable: %q != %q", got, first)
	}
	if identities.UserID(100, 201) == first {
		t.Fatal("different senders must map to different ids")
	}
	if identities.UserID(101, 200) == first {
		t.Fatal("the same sender must stay separate per account")
	}
	if NewIdentities(strings.Repeat("o", MinSecretLen), false).UserID(100, 200) == first {
		t.Fatal("a different key must produce a different pseudonym")
	}
	identity := identities.For(100, 200, 700)
	if identity.BotIDKey != BotIDHashKey || identity.BotIDValue == "" || strings.Contains(identity.BotIDValue, "100") {
		t.Fatalf("bot hash leaks the raw id: %+v", identity)
	}
	if identity.TargetID != "" || identity.SenderID != "" {
		t.Fatalf("pseudonym mode must not export raw targets: %+v", identity)
	}
}

// TestIdentitiesWithoutSecretIsInert verifies the documented contract: no
// secret and no raw mode means no identity attributes at all.
// TestIdentitiesWithoutSecretIsInert 验证既定契约：既没有密钥也没有原始模式时
// 完全不写身份属性。
func TestIdentitiesWithoutSecretIsInert(t *testing.T) {
	identities := NewIdentities("  ", false)
	if identities != nil {
		t.Fatal("empty secret must disable pseudonymization")
	}
	if identities.UserID(1, 2) != "" {
		t.Fatal("nil identities must return empty ids")
	}
	if identity := identities.For(1, 2, 3); identity != (Identity{}) {
		t.Fatalf("nil identities must resolve to zero identity: %+v", identity)
	}
}

// TestIdentitiesRawModeExportsQQNumbers verifies the debugging mode writes the
// real account numbers and needs no secret.
// TestIdentitiesRawModeExportsQQNumbers 验证调试模式写出真实账号且不需要密钥。
func TestIdentitiesRawModeExportsQQNumbers(t *testing.T) {
	identities := NewIdentities("", true)
	if identities == nil {
		t.Fatal("raw mode must work without a secret")
	}
	identity := identities.For(1341939164, 1552089234, 987654321)
	if identity.UserID != "1552089234" {
		t.Fatalf("user id = %q, want the raw QQ number", identity.UserID)
	}
	if identity.BotIDKey != BotIDKey || identity.BotIDValue != "1341939164" {
		t.Fatalf("bot identity = %q/%q", identity.BotIDKey, identity.BotIDValue)
	}
	if identity.TargetID != "987654321" || identity.SenderID != "1552089234" {
		t.Fatalf("target/sender = %q/%q", identity.TargetID, identity.SenderID)
	}

	recorder := tracetest.NewSpanRecorder()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
	_, span := provider.Tracer("test").Start(context.Background(), "root")
	identity.SessionID = "conv-1"
	SetIdentity(span, identity)
	span.End()

	attrs := map[string]string{}
	for _, recorded := range recorder.Ended() {
		for _, kv := range recorded.Attributes() {
			attrs[string(kv.Key)] = kv.Value.AsString()
		}
	}
	for key, want := range map[string]string{
		SessionIDKey: "conv-1",
		UserIDKey:    "1552089234",
		BotIDKey:     "1341939164",
		TargetIDKey:  "987654321",
		SenderIDKey:  "1552089234",
	} {
		if attrs[key] != want {
			t.Fatalf("%s = %q, want %q", key, attrs[key], want)
		}
	}
	if _, ok := attrs[BotIDHashKey]; ok {
		t.Fatal("raw mode must not also write the hash attribute")
	}
}

// TestWithIdentityReachesChildSpans verifies that bagged identity attributes
// land on every span created beneath the context.
// TestWithIdentityReachesChildSpans 验证身份属性会落到上下文下新建的每个 span 上。
func TestWithIdentityReachesChildSpans(t *testing.T) {
	recorder := tracetest.NewSpanRecorder()
	provider := sdktrace.NewTracerProvider(
		sdktrace.WithSpanProcessor(identityProcessor{}),
		sdktrace.WithSpanProcessor(recorder),
	)
	ctx := WithIdentity(context.Background(), Identity{
		SessionID: "conv-1", UserID: "u_abc", BotIDKey: BotIDHashKey, BotIDValue: "b_abc",
	})
	ctx, span := provider.Tracer("test").Start(ctx, "worker")
	_, child := provider.Tracer("test").Start(ctx, "intent")
	child.End()
	span.End()

	ended := recorder.Ended()
	if len(ended) != 2 {
		t.Fatalf("ended spans = %d", len(ended))
	}
	for _, recorded := range ended {
		found := false
		for _, kv := range recorded.Attributes() {
			if string(kv.Key) == UserIDKey && kv.Value.AsString() == "u_abc" {
				found = true
			}
		}
		if !found {
			t.Fatalf("span %s missing identity attributes: %v", recorded.Name(), recorded.Attributes())
		}
	}
}

// TestSetIdentitySkipsEmptyValues verifies no placeholder identifiers are sent
// when a value is missing.
// TestSetIdentitySkipsEmptyValues 验证缺失值不会产生占位标识。
func TestSetIdentitySkipsEmptyValues(t *testing.T) {
	recorder := tracetest.NewSpanRecorder()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
	_, span := provider.Tracer("test").Start(context.Background(), "root")
	SetIdentity(span, Identity{SessionID: "conv-1"})
	span.End()

	ended := recorder.Ended()
	if len(ended) != 1 {
		t.Fatalf("ended spans = %d", len(ended))
	}
	attrs := ended[0].Attributes()
	if len(attrs) != 1 || string(attrs[0].Key) != SessionIDKey {
		t.Fatalf("attributes = %v", attrs)
	}
}

// TestValidateSecret verifies the startup key check rejects values that cannot
// protect an enumerable QQ id space.
// TestValidateSecret 验证启动期密钥检查会拒绝无法保护可枚举 QQ 号空间的取值。
func TestValidateSecret(t *testing.T) {
	cases := []struct {
		name    string
		secret  string
		wantErr bool
	}{
		{name: "empty", secret: "", wantErr: true},
		{name: "blank", secret: "   ", wantErr: true},
		{name: "placeholder", secret: "REPLACE_WITH_RANDOM_SECRET", wantErr: true},
		{name: "placeholder lower with spaces", secret: "  replace_with_random_secret  ", wantErr: true},
		{name: "changeme", secret: "changeme", wantErr: true},
		{name: "too short", secret: strings.Repeat("a", MinSecretLen-1), wantErr: true},
		{name: "minimum length", secret: strings.Repeat("a", MinSecretLen), wantErr: false},
		{name: "hex key", secret: testSecret, wantErr: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateSecret(tc.secret)
			if tc.wantErr && err == nil {
				t.Fatal("expected the secret to be rejected")
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("unexpected rejection: %v", err)
			}
		})
	}
}

// TestNewIdentitiesRejectsWeakSecret verifies weak keys fail closed instead of
// producing reversible pseudonyms.
// TestNewIdentitiesRejectsWeakSecret 验证弱密钥关闭式失败，不产生可反查的假名。
func TestNewIdentitiesRejectsWeakSecret(t *testing.T) {
	if NewIdentities("REPLACE_WITH_RANDOM_SECRET", false) != nil {
		t.Fatal("placeholder secret must not enable pseudonyms")
	}
	if NewIdentities(strings.Repeat("a", MinSecretLen-1), false) != nil {
		t.Fatal("short secret must not enable pseudonyms")
	}
	if NewIdentities("REPLACE_WITH_RANDOM_SECRET", true) == nil {
		t.Fatal("raw mode must ignore the secret entirely")
	}
}
