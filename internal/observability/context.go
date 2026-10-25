// Trace context persistence and identity pseudonymization shared by the
// business pipeline.
// 业务流水线共用的追踪上下文持久化与身份假名化。
package observability

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/baggage"
	"go.opentelemetry.io/otel/propagation"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
)

// Trace-level attribute keys understood by Langfuse over OTLP. They may appear
// on any span, but filtering only works when every span of the trace carries
// them, which is why they travel as baggage while a job is processed.
// Langfuse 通过 OTLP 识别的 trace 级属性键。它们可以出现在任意 span 上，
// 但只有整条 trace 的 span 都携带时才能用于过滤，因此在处理任务期间以
// baggage 形式向下传递。
const (
	SessionIDKey = "langfuse.session.id"
	UserIDKey    = "langfuse.user.id"
	// BotIDKey carries the raw account number and is only used in raw mode;
	// pseudo mode keeps BotIDHashKey so the two never coexist on one trace.
	// BotIDKey 携带原始账号并被原始模式使用；假名模式继续用 BotIDHashKey，
	// 两者不会同时出现在同一条 trace 上。
	BotIDKey     = "qqbot.bot_id"
	BotIDHashKey = "qqbot.bot_id_hash"
	TargetIDKey  = "qqbot.target_id"
	SenderIDKey  = "qqbot.sender_id"
)

// maxTraceStateLen is the upper bound from the W3C tracestate specification;
// longer values are dropped instead of being stored.
// maxTraceStateLen 是 W3C tracestate 规范的长度上限；超长值直接丢弃而不入库。
const maxTraceStateLen = 512

// traceParentPattern is the strict W3C traceparent shape: version-traceid-spanid-flags.
// traceParentPattern 是严格的 W3C traceparent 形态：版本-traceid-spanid-flags。
var traceParentPattern = regexp.MustCompile(`^[0-9a-f]{2}-[0-9a-f]{32}-[0-9a-f]{16}-[0-9a-f]{2}$`)

// TraceContext is the persistable W3C context of one span.
// TraceContext 是一个 span 可持久化的 W3C 上下文。
type TraceContext struct {
	TraceParent string
	TraceState  string
}

// Current extracts the W3C context of the active span; an unrecorded span or an
// invalid header yields the zero value.
// Current 提取当前 span 的 W3C 上下文；span 未记录或头部非法时返回零值。
func Current(ctx context.Context) TraceContext {
	carrier := propagation.MapCarrier{}
	propagation.TraceContext{}.Inject(ctx, carrier)
	traceParent := validTraceParent(carrier.Get("traceparent"))
	if traceParent == "" {
		return TraceContext{}
	}
	traceState := carrier.Get("tracestate")
	if len(traceState) > maxTraceStateLen {
		// Tracestate is optional; a malformed value must not invalidate the
		// traceparent or reach the database unbounded.
		// tracestate 可选；非法值不应让 traceparent 失效，也不能无限长入库。
		traceState = ""
	}
	return TraceContext{TraceParent: traceParent, TraceState: traceState}
}

// Restore re-attaches a stored context as the parent of new spans. Values
// already present in ctx are preserved; an invalid or missing header returns
// ctx unchanged, so legacy jobs simply start a new trace.
// Restore 把已存储的上下文重新挂为新建 span 的父级。ctx 中已有的值保持不变；
// 头部非法或缺失时原样返回 ctx，使历史任务直接开启新的 trace。
func (tc TraceContext) Restore(ctx context.Context) context.Context {
	if !tc.Valid() {
		return ctx
	}
	carrier := propagation.MapCarrier{"traceparent": tc.TraceParent}
	// Stored rows are not trusted input: an oversized tracestate is dropped
	// rather than handed to the propagator.
	// 已存储的行不是可信输入：超长 tracestate 直接丢弃，不交给传播器。
	if tc.TraceState != "" && len(tc.TraceState) <= maxTraceStateLen {
		carrier["tracestate"] = tc.TraceState
	}
	return propagation.TraceContext{}.Extract(ctx, carrier)
}

// Valid reports whether the stored traceparent is well formed.
// Valid 报告已存储的 traceparent 是否合法。
func (tc TraceContext) Valid() bool { return validTraceParent(tc.TraceParent) != "" }

// validTraceParent returns the header when it matches the W3C shape and carries
// non-zero identifiers, otherwise the empty string.
// validTraceParent 在头部符合 W3C 形态且标识非零时返回该头部，否则返回空串。
func validTraceParent(raw string) string {
	raw = strings.TrimSpace(raw)
	if !traceParentPattern.MatchString(raw) {
		return ""
	}
	parts := strings.Split(raw, "-")
	// Version ff is forbidden, and all-zero ids are invalid.
	// 版本 ff 非法，全零标识同样非法。
	if parts[0] == "ff" || strings.Trim(parts[1], "0") == "" || strings.Trim(parts[2], "0") == "" {
		return ""
	}
	return raw
}

// Identities resolves the trace identity of one message. In raw mode it
// exports QQ numbers directly, which is convenient for correlating traces with
// real users; otherwise it derives stable HMAC pseudonyms. A nil instance means
// neither mode is configured: every method returns empty values and callers
// must skip the attributes instead of inventing identifiers.
// Identities 解析单条消息在 trace 中的身份。原始模式直接导出 QQ 号，
// 便于把 trace 与真实用户对上；否则生成稳定的 HMAC 假名。
// 实例为 nil 表示两种模式都未配置：方法返回空值，调用方必须跳过属性，
// 不得伪造标识。
type Identities struct {
	key []byte
	raw bool
}

// MinSecretLen is the shortest accepted pseudonym key. QQ numbers are an
// enumerable space, so a short key makes the pseudonym reversible offline.
// MinSecretLen 是可接受的假名密钥最短长度。QQ 号空间可枚举，
// 过短的密钥会让假名可被离线反查。
const MinSecretLen = 32

// weakSecretPlaceholders are values shipped in examples or copied by hand;
// they are public and must never protect a pseudonym.
// weakSecretPlaceholders 是示例文件或手抄常用的值；它们公开可见，
// 绝不能用于保护假名。
var weakSecretPlaceholders = map[string]bool{
	"replace_with_random_secret": true,
	"changeme":                   true,
}

// ValidateSecret reports why a pseudonym key is unusable; nil means acceptable.
// Callers log the reason so a disabled user dimension is never silent.
// ValidateSecret 报告假名密钥为何不可用；nil 表示可用。
// 调用方记录原因，避免用户维度被静默关闭。
func ValidateSecret(secret string) error {
	trimmed := strings.TrimSpace(secret)
	if trimmed == "" {
		return fmt.Errorf("secret is empty")
	}
	if weakSecretPlaceholders[strings.ToLower(trimmed)] {
		return fmt.Errorf("secret is a public placeholder")
	}
	if len(trimmed) < MinSecretLen {
		return fmt.Errorf("secret is shorter than %d characters", MinSecretLen)
	}
	return nil
}

// NewIdentities builds the resolver. Raw mode always works without a secret;
// pseudonym mode requires an acceptable secret and fails closed otherwise.
// NewIdentities 构建解析器。原始模式无需密钥；假名模式要求可接受的密钥，
// 否则关闭式返回 nil。
func NewIdentities(secret string, raw bool) *Identities {
	if raw {
		return &Identities{raw: true}
	}
	if err := ValidateSecret(secret); err != nil {
		return nil
	}
	return &Identities{key: []byte(strings.TrimSpace(secret))}
}

// UserID returns the id of one sender: the raw QQ number in raw mode, or a
// keyed pseudonym scoped to the account otherwise.
// UserID 返回某个发送者的标识：原始模式下是 QQ 号，否则是按账号隔离的假名。
func (i *Identities) UserID(botID, senderID int64) string {
	if i == nil {
		return ""
	}
	if i.raw {
		return strconv.FormatInt(senderID, 10)
	}
	return i.sum("u_", strconv.FormatInt(botID, 10)+":"+strconv.FormatInt(senderID, 10))
}

// For resolves every identity attribute of one message.
// For 解析单条消息的全部身份属性。
func (i *Identities) For(botID, senderID, targetID int64) Identity {
	if i == nil {
		return Identity{}
	}
	if i.raw {
		return Identity{
			UserID:     strconv.FormatInt(senderID, 10),
			BotIDKey:   BotIDKey,
			BotIDValue: strconv.FormatInt(botID, 10),
			TargetID:   strconv.FormatInt(targetID, 10),
			SenderID:   strconv.FormatInt(senderID, 10),
		}
	}
	return Identity{
		UserID:     i.UserID(botID, senderID),
		BotIDKey:   BotIDHashKey,
		BotIDValue: i.sum("b_", strconv.FormatInt(botID, 10)),
	}
}

// sum applies HMAC-SHA256 over the label and returns prefix plus hex digest.
// sum 对标签做 HMAC-SHA256 并返回前缀加十六进制摘要。
func (i *Identities) sum(prefix, label string) string {
	if i == nil || len(i.key) == 0 {
		return ""
	}
	mac := hmac.New(sha256.New, i.key)
	_, _ = mac.Write([]byte(label))
	return prefix + hex.EncodeToString(mac.Sum(nil))
}

// Identity carries the trace attributes of one message; empty fields are
// skipped so a disabled identity never pollutes the trace.
// Identity 持有单条消息的 trace 属性；空字段跳过，
// 保证关闭身份时不会污染 trace。
type Identity struct {
	SessionID  string
	UserID     string
	BotIDKey   string
	BotIDValue string
	TargetID   string
	SenderID   string
}

// attributes renders the non-empty identity fields as span attributes.
// attributes 把非空身份字段渲染为 span 属性。
func (id Identity) attributes() []attribute.KeyValue {
	attrs := make([]attribute.KeyValue, 0, 6)
	if id.SessionID != "" {
		attrs = append(attrs, attribute.String(SessionIDKey, id.SessionID))
	}
	if id.UserID != "" {
		attrs = append(attrs, attribute.String(UserIDKey, id.UserID))
	}
	if id.BotIDKey != "" && id.BotIDValue != "" {
		attrs = append(attrs, attribute.String(id.BotIDKey, id.BotIDValue))
	}
	if id.TargetID != "" {
		attrs = append(attrs, attribute.String(TargetIDKey, id.TargetID))
	}
	if id.SenderID != "" {
		attrs = append(attrs, attribute.String(SenderIDKey, id.SenderID))
	}
	return attrs
}

// SetIdentity attaches the identity attributes to one span.
// SetIdentity 把身份属性附加到单个 span。
func SetIdentity(span trace.Span, id Identity) {
	if span == nil {
		return
	}
	if attrs := id.attributes(); len(attrs) > 0 {
		span.SetAttributes(attrs...)
	}
}

// WithIdentity carries the identity attributes on the context so every span
// created beneath it receives them through identityProcessor.
// WithIdentity 把身份属性放入上下文，使其下新建的每个 span 都由
// identityProcessor 自动获得这些属性。
func WithIdentity(ctx context.Context, id Identity) context.Context {
	attrs := id.attributes()
	members := make([]baggage.Member, 0, len(attrs))
	for _, kv := range attrs {
		member, err := baggage.NewMember(string(kv.Key), kv.Value.AsString())
		if err != nil {
			continue
		}
		members = append(members, member)
	}
	if len(members) == 0 {
		return ctx
	}
	bag, err := baggage.New(members...)
	if err != nil {
		return ctx
	}
	return baggage.ContextWithBaggage(ctx, bag)
}

// identityProcessor copies identity baggage onto every started span.
// identityProcessor 把身份 baggage 复制到每个新开始的 span 上。
type identityProcessor struct{}

// OnStart implements sdktrace.SpanProcessor.
// OnStart 实现 sdktrace.SpanProcessor。
func (identityProcessor) OnStart(ctx context.Context, span sdktrace.ReadWriteSpan) {
	bag := baggage.FromContext(ctx)
	attrs := make([]attribute.KeyValue, 0, 6)
	for _, key := range []string{SessionIDKey, UserIDKey, BotIDKey, BotIDHashKey, TargetIDKey, SenderIDKey} {
		if value := bag.Member(key).Value(); value != "" {
			attrs = append(attrs, attribute.String(key, value))
		}
	}
	if len(attrs) > 0 {
		span.SetAttributes(attrs...)
	}
}

// OnEnd implements sdktrace.SpanProcessor.
// OnEnd 实现 sdktrace.SpanProcessor。
func (identityProcessor) OnEnd(sdktrace.ReadOnlySpan) {}

// Shutdown implements sdktrace.SpanProcessor.
// Shutdown 实现 sdktrace.SpanProcessor。
func (identityProcessor) Shutdown(context.Context) error { return nil }

// ForceFlush implements sdktrace.SpanProcessor.
// ForceFlush 实现 sdktrace.SpanProcessor。
func (identityProcessor) ForceFlush(context.Context) error { return nil }
