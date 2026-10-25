// Tests for the AI ingress handler's privacy and routing behavior.
// AI 入站处理器隐私与路由行为的测试。
package infrastructure

import (
	"context"
	"io"
	"log/slog"
	"testing"

	"go.opentelemetry.io/otel"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"

	"github.com/HappyLadySauceM/QQBot/internal/conversation/domain"
	"github.com/HappyLadySauceM/QQBot/internal/observability"
	obdomain "github.com/HappyLadySauceM/QQBot/internal/onebot/domain"
	scopedomain "github.com/HappyLadySauceM/QQBot/internal/scope/domain"
)

// discardLogger drops log output in tests.
// discardLogger 在测试中丢弃日志输出。
func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// fakeAuthorizer resolves policies from a map and records calls.
// fakeAuthorizer 从映射解析策略并记录调用。
type fakeAuthorizer struct {
	policies map[string]scopedomain.Policy
	resolves int
}

func (f *fakeAuthorizer) Resolve(_ context.Context, botID int64, t scopedomain.ScopeType, id int64) (scopedomain.Policy, error) {
	f.resolves++
	return f.policies[scopedomainScopeKey(botID, string(t), id)], nil
}

// scopedomainScopeKey is a test-only policy key helper.
// scopedomainScopeKey 是仅用于测试的策略键。
func scopedomainScopeKey(botID int64, typ string, id int64) string {
	return itoa(botID) + ":" + typ + ":" + itoa(id)
}

func itoa(v int64) string {
	if v == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	neg := v < 0
	if neg {
		v = -v
	}
	for v > 0 {
		i--
		buf[i] = byte('0' + v%10)
		v /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}

// fakeIngestor records ingest inputs.
// fakeIngestor 记录入库输入。
type fakeIngestor struct {
	inputs []domain.IngestInput
}

func (f *fakeIngestor) Ingest(_ context.Context, in domain.IngestInput) (domain.IngestResult, error) {
	f.inputs = append(f.inputs, in)
	return domain.IngestResult{ConversationID: "c1", MessageID: 1, JobID: 1}, nil
}

func groupEvent(senderID obdomain.UserID, selfID obdomain.BotID) obdomain.Event {
	return obdomain.Event{
		Post:       obdomain.PostMessage,
		Type:       obdomain.TypeGroup,
		SelfID:     selfID,
		GroupID:    700,
		MessageID:  5001,
		UserID:     senderID,
		Sender:     obdomain.Sender{ID: senderID},
		RawMessage: "你好",
	}
}

func newTestIngress(t *testing.T, auth *fakeAuthorizer, ingest *fakeIngestor) *Ingress {
	t.Helper()
	h, err := NewIngress(ingest, auth, observability.NewIdentities("0123456789abcdef0123456789abcdef", false), discardLogger())
	if err != nil {
		t.Fatalf("new ingress: %v", err)
	}
	return h
}

// recordingProvider installs a span recorder for the duration of one test.
// recordingProvider 在单个测试期间安装 span 记录器。
func recordingProvider(t *testing.T) *tracetest.SpanRecorder {
	t.Helper()
	recorder := tracetest.NewSpanRecorder()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
	previous := otel.GetTracerProvider()
	otel.SetTracerProvider(provider)
	t.Cleanup(func() {
		otel.SetTracerProvider(previous)
		_ = provider.Shutdown(context.Background())
	})
	return recorder
}

// TestIngressSkipsOwnMessages verifies that events from the bot itself never
// reach scope resolution or ingest.
// TestIngressSkipsOwnMessages 验证机器人自身事件不会进入作用域解析或入库。
func TestIngressSkipsOwnMessages(t *testing.T) {
	auth := &fakeAuthorizer{policies: map[string]scopedomain.Policy{}}
	ingest := &fakeIngestor{}
	h := newTestIngress(t, auth, ingest)

	event := groupEvent(100, 100) // sender == self
	if err := h.Handle(context.Background(), 100, event); err != nil {
		t.Fatalf("handle: %v", err)
	}
	if auth.resolves != 0 || len(ingest.inputs) != 0 {
		t.Fatalf("own message must be skipped: resolves=%d ingests=%d", auth.resolves, len(ingest.inputs))
	}
}

// TestIngressDropsUnauthorizedContent verifies that disabled scopes are neither
// resolved into ingest nor persisted (privacy red line).
// TestIngressDropsUnauthorizedContent 验证未启用的作用域不进入入库（隐私红线）。
func TestIngressDropsUnauthorizedContent(t *testing.T) {
	auth := &fakeAuthorizer{policies: map[string]scopedomain.Policy{
		scopedomainScopeKey(100, "group", 700): {Enabled: false},
	}}
	ingest := &fakeIngestor{}
	h := newTestIngress(t, auth, ingest)

	if err := h.Handle(context.Background(), 100, groupEvent(200, 100)); err != nil {
		t.Fatalf("handle: %v", err)
	}
	if len(ingest.inputs) != 0 {
		t.Fatalf("unauthorized content must not be ingested: %+v", ingest.inputs)
	}
}

// TestIngressIgnoresEventsWithoutAccount verifies the handler is inert when the
// dispatcher could not resolve an account.
// TestIngressIgnoresEventsWithoutAccount 验证无法解析账号时处理器不动作。
func TestIngressIgnoresEventsWithoutAccount(t *testing.T) {
	auth := &fakeAuthorizer{policies: map[string]scopedomain.Policy{}}
	ingest := &fakeIngestor{}
	h := newTestIngress(t, auth, ingest)

	if err := h.Handle(context.Background(), 0, groupEvent(200, 0)); err != nil {
		t.Fatalf("handle: %v", err)
	}
	if auth.resolves != 0 || len(ingest.inputs) != 0 {
		t.Fatal("unknown self id must short-circuit")
	}
}

// TestIngressIngestsAuthorizedGroupMessage verifies kind, target and text mapping
// for an enabled scope with storage consent.
// TestIngressIngestsAuthorizedGroupMessage 验证已授权作用域的类型、目标与文本映射。
func TestIngressIngestsAuthorizedGroupMessage(t *testing.T) {
	auth := &fakeAuthorizer{policies: map[string]scopedomain.Policy{
		scopedomainScopeKey(100, "group", 700): {Enabled: true, StoreMessages: true},
	}}
	ingest := &fakeIngestor{}
	h := newTestIngress(t, auth, ingest)

	event := groupEvent(200, 100)
	if !h.Match(event) {
		t.Fatal("message event should match")
	}
	if err := h.Handle(context.Background(), 100, event); err != nil {
		t.Fatalf("handle: %v", err)
	}
	if len(ingest.inputs) != 1 {
		t.Fatalf("expected one ingest, got %d", len(ingest.inputs))
	}
	in := ingest.inputs[0]
	if in.BotID != 100 || in.Kind != domain.KindGroup || in.TargetID != 700 ||
		in.SenderID != 200 || in.PlatformMessageID != 5001 || in.Text != "你好" {
		t.Fatalf("ingest input = %+v", in)
	}
}

// TestIngressPersistsTraceContext verifies that an authorized message stores a
// usable W3C context for the worker and that the root span carries session and
// user attributes. Unauthorized content must store nothing.
// TestIngressPersistsTraceContext 验证已授权消息为工作器保存可用的 W3C 上下文，
// 且根 span 携带会话与用户属性；未授权内容不得写入任何上下文。
func TestIngressPersistsTraceContext(t *testing.T) {
	recorder := recordingProvider(t)
	auth := &fakeAuthorizer{policies: map[string]scopedomain.Policy{
		scopedomainScopeKey(100, "group", 700): {Enabled: true, StoreMessages: true},
	}}
	ingest := &fakeIngestor{}
	h := newTestIngress(t, auth, ingest)

	if err := h.Handle(context.Background(), 100, groupEvent(200, 100)); err != nil {
		t.Fatalf("handle: %v", err)
	}
	in := ingest.inputs[0]
	stored := observability.TraceContext{TraceParent: in.TraceParent, TraceState: in.TraceState}
	if !stored.Valid() {
		t.Fatalf("ingest must persist a valid traceparent, got %+v", in)
	}

	var root sdktrace.ReadOnlySpan
	for _, span := range recorder.Ended() {
		if span.Name() == "qqbot.message.process" {
			root = span
		}
	}
	if root == nil {
		t.Fatal("qqbot.message.process span not recorded")
	}
	restored := trace.SpanContextFromContext(stored.Restore(context.Background()))
	if restored.TraceID() != root.SpanContext().TraceID() {
		t.Fatalf("persisted trace id = %s, want %s", restored.TraceID(), root.SpanContext().TraceID())
	}
	attrs := map[string]string{}
	for _, kv := range root.Attributes() {
		attrs[string(kv.Key)] = kv.Value.Emit()
	}
	if attrs[observability.SessionIDKey] != "c1" {
		t.Fatalf("session attribute = %q", attrs[observability.SessionIDKey])
	}
	if attrs[observability.UserIDKey] == "" {
		t.Fatal("user attribute must be set when a secret is configured")
	}
	if attrs[observability.BotIDHashKey] == "" {
		t.Fatal("bot id hash attribute must be set when a secret is configured")
	}

	// Child spans must carry the identity known before persistence; the session
	// id only exists afterwards and legitimately stays on the root.
	// 子 span 必须携带落库前已知的身份；会话 ID 之后才存在，只留在根 span 上。
	childAttrs := map[string]string{}
	for _, span := range recorder.Ended() {
		if span.Name() != "qqbot.message.persist" {
			continue
		}
		for _, kv := range span.Attributes() {
			childAttrs[string(kv.Key)] = kv.Value.AsString()
		}
	}
	if childAttrs[observability.UserIDKey] == "" || childAttrs[observability.BotIDHashKey] == "" {
		t.Fatalf("persist span missing identity attributes: %v", childAttrs)
	}
	if _, ok := childAttrs[observability.SessionIDKey]; ok {
		t.Fatalf("session id must only be set after persistence: %v", childAttrs)
	}
}

// TestIngressExportsRawQQIdentifiers verifies raw mode writes the real sender,
// account and target numbers instead of pseudonyms.
// TestIngressExportsRawQQIdentifiers 验证原始模式写出真实的发送者、账号与目标号码，
// 而不是假名。
func TestIngressExportsRawQQIdentifiers(t *testing.T) {
	recorder := recordingProvider(t)
	auth := &fakeAuthorizer{policies: map[string]scopedomain.Policy{
		scopedomainScopeKey(100, "group", 700): {Enabled: true, StoreMessages: true},
	}}
	h, err := NewIngress(&fakeIngestor{}, auth, observability.NewIdentities("", true), discardLogger())
	if err != nil {
		t.Fatalf("new ingress: %v", err)
	}
	if err := h.Handle(context.Background(), 100, groupEvent(200, 100)); err != nil {
		t.Fatalf("handle: %v", err)
	}

	attrs := map[string]string{}
	for _, span := range recorder.Ended() {
		if span.Name() != "qqbot.message.process" {
			continue
		}
		for _, kv := range span.Attributes() {
			attrs[string(kv.Key)] = kv.Value.AsString()
		}
	}
	for key, want := range map[string]string{
		observability.UserIDKey:   "200",
		observability.BotIDKey:    "100",
		observability.TargetIDKey: "700",
		observability.SenderIDKey: "200",
	} {
		if attrs[key] != want {
			t.Fatalf("%s = %q, want %q", key, attrs[key], want)
		}
	}
	if _, ok := attrs[observability.BotIDHashKey]; ok {
		t.Fatal("raw mode must not also write the hash attribute")
	}
}
