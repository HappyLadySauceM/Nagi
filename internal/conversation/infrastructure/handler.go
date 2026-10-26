// AIIngress handler: fast scope check and atomic ingest, never blocking work.
// AIIngress 处理器：快速作用域检查与原子入库，绝不执行阻塞工作。
package infrastructure

import (
	"context"
	"fmt"
	"log/slog"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	botcontracts "github.com/HappyLadySauceM/Nagi/internal/bot/contracts"
	"github.com/HappyLadySauceM/Nagi/internal/observability"
	obdomain "github.com/HappyLadySauceM/Nagi/internal/onebot/domain"
	scopecontracts "github.com/HappyLadySauceM/Nagi/internal/scope/contracts"
	scopedomain "github.com/HappyLadySauceM/Nagi/internal/scope/domain"

	"github.com/HappyLadySauceM/Nagi/internal/conversation/application"
	"github.com/HappyLadySauceM/Nagi/internal/conversation/domain"
)

// Ingress implements bot/contracts.Handler. Match stays cheap; Handle only
// resolves the scope and persists authorized messages before returning.
// Ingress 实现 bot/contracts.Handler。Match 保持廉价；Handle 仅解析作用域
// 并在返回前持久化已授权消息。
type Ingress struct {
	ingest application.Ingestor
	scopes scopecontracts.Authorizer
	// identities may be nil when neither raw mode nor a pseudonym secret is
	// configured; traces then carry no user dimension at all.
	// 未启用原始模式且未配置假名密钥时 identities 可为 nil；
	// 此时 trace 不携带用户维度。
	identities *observability.Identities
	logger     *slog.Logger
}

// NewIngress validates dependencies and builds the handler. The originating
// account arrives with each event, so no connection-level status is needed.
// NewIngress 校验依赖并构建处理器。账号身份随事件传入，因此不需要连接级状态。
func NewIngress(ingest application.Ingestor, scopes scopecontracts.Authorizer, identities *observability.Identities, logger *slog.Logger) (*Ingress, error) {
	if ingest == nil || scopes == nil || logger == nil {
		return nil, fmt.Errorf("conversation: ingress dependencies required")
	}
	return &Ingress{ingest: ingest, scopes: scopes, identities: identities, logger: logger}, nil
}

// Name identifies the handler in logs and status output.
// Name 标识处理器，用于日志与状态输出。
func (h *Ingress) Name() string { return "ai_ingress" }

// Match accepts message events only; it must stay cheap and pure.
// Match 仅接受消息事件；必须保持廉价且无副作用。
func (h *Ingress) Match(event obdomain.Event) bool { return event.IsMessage() }

// Handle resolves the scope and ingests authorized messages. Unauthorized content
// is dropped before any persistence or telemetry; database failures fail closed.
// Handle 解析作用域并入库已授权消息。未授权内容在任何持久化或遥测之前被丢弃；
// 数据库故障按关闭处理。
func (h *Ingress) Handle(ctx context.Context, bot obdomain.BotID, event obdomain.Event) error {
	// No message content is recorded on spans, only ids and kinds.
	// span 只记录 ID 与类型，不记录消息内容。
	ctx, span := tracer().Start(ctx, "nagi.message.process")
	defer span.End()
	span.SetAttributes(attribute.Bool("in_group", event.InGroup()))
	// Listener-side identity only: the message body is excluded until the scope
	// grants storage consent, so an unauthorized conversation never reaches the
	// trace. The authorized body is recorded by the worker instead.
	// 载荷只含监听侧标识：在作用域同意存储之前不包含消息正文，
	// 未授权会话因此不会进入 trace；已授权正文由工作器记录。
	observability.RecordObservation(span, "langfuse.observation.input", map[string]any{
		"message_id": int64(event.MessageID),
		"in_group":   event.InGroup(),
		"segments":   len(event.Segments),
	})

	botID := int64(bot)
	if botID <= 0 {
		observability.RecordObservation(span, "langfuse.observation.output", map[string]any{
			"dropped": "missing_account",
		})
		return nil
	}
	if int64(event.Sender.ID) == botID {
		// Ignore the bot's own outgoing messages echoed back as events.
		// 忽略以事件形式回传的机器人自身消息。
		observability.RecordObservation(span, "langfuse.observation.output", map[string]any{
			"dropped": "own_message",
		})
		return nil
	}

	var scopeType scopedomain.ScopeType
	var targetID int64
	if event.InGroup() {
		scopeType, targetID = scopedomain.ScopeGroup, int64(event.GroupID)
	} else {
		scopeType, targetID = scopedomain.ScopePrivate, int64(event.UserID)
	}
	// Account, sender and target are known before persistence, so they are
	// attached here and ride the context into the scope/persist children. The
	// session id only exists after the row is written and stays on the root.
	// 账号、发送者与目标在落库前就已知，因此在此写入并随上下文传给
	// scope/persist 子 span；会话 ID 只在写库后存在，仅落在根 span。
	identity := h.identities.For(botID, int64(event.Sender.ID), targetID)
	ctx = observability.WithIdentity(ctx, identity)
	observability.SetIdentity(span, identity)

	scopeCtx, scopeSpan := tracer().Start(ctx, "nagi.ingress.scope")
	scopeSpan.SetAttributes(attribute.String("nagi.scope_type", string(scopeType)))
	observability.SetIdentity(scopeSpan, identity)
	policy, err := h.scopes.Resolve(scopeCtx, botID, scopeType, targetID)
	if err != nil {
		// Fail closed: without a policy decision nothing is stored or processed.
		// 关闭式失败：没有策略判定就不存储、不处理。
		scopeSpan.RecordError(err)
		scopeSpan.End()
		h.logger.Warn("ai ingress scope resolve failed", "error", err, "bot_id", botID)
		return nil
	}
	scopeSpan.SetAttributes(
		attribute.Bool("nagi.scope_enabled", policy.Enabled),
		attribute.Bool("nagi.scope_store_messages", policy.StoreMessages),
	)
	observability.RecordObservation(scopeSpan, "langfuse.observation.output", map[string]any{
		"enabled":        policy.Enabled,
		"store_messages": policy.StoreMessages,
		"reply_mode":     string(policy.ReplyMode),
	})
	scopeSpan.End()
	if !policy.Enabled || !policy.StoreMessages {
		// Default deny: scopes without explicit enablement and storage consent
		// never persist or process message content.
		// 默认拒绝：未显式开启并同意存储的作用域，绝不持久化或处理消息内容。
		observability.RecordObservation(span, "langfuse.observation.output", map[string]any{
			"dropped": "scope_disabled",
		})
		return nil
	}

	input := domain.IngestInput{
		BotID:             botID,
		Kind:              domain.Kind(scopeType),
		TargetID:          targetID,
		PlatformMessageID: int64(event.MessageID),
		SenderID:          int64(event.Sender.ID),
		Text:              event.Text(),
		Segments:          event.Segments,
	}
	// Persist the root span context so the worker continues this trace instead
	// of starting an unrelated one. An unrecorded span persists nothing.
	// 持久化根 span 上下文，让工作器延续同一条 trace 而不是另开一条。
	// span 未记录时不写入任何内容。
	if traceCtx := observability.Current(ctx); traceCtx.Valid() {
		input.TraceParent = traceCtx.TraceParent
		input.TraceState = traceCtx.TraceState
	}

	persistCtx, persistSpan := tracer().Start(ctx, "nagi.message.persist")
	observability.SetIdentity(persistSpan, identity)
	result, err := h.ingest.Ingest(persistCtx, input)
	if err != nil {
		persistSpan.RecordError(err)
		persistSpan.End()
		h.logger.Warn("ai ingress persist failed", "error", err, "bot_id", botID)
		return nil
	}
	persistSpan.SetAttributes(
		attribute.Int64("nagi.message_id", result.MessageID),
		attribute.Int64("nagi.job_id", result.JobID),
		attribute.Bool("nagi.duplicate", result.Duplicate),
	)
	observability.RecordObservation(persistSpan, "langfuse.observation.output", map[string]any{
		"message_id": result.MessageID,
		"job_id":     result.JobID,
		"duplicate":  result.Duplicate,
	})
	persistSpan.End()

	// Session and message ids are only known after the row exists; attributes
	// can still be added to the open root span, and Langfuse keeps them for
	// Sessions/Users.
	// 会话与消息 ID 只有在落库后才知道；属性仍可在根 span 结束前追加，
	// Langfuse 会保留它们供 Sessions/Users 使用。
	span.SetAttributes(
		attribute.String("nagi.conversation_id", result.ConversationID),
		attribute.Int64("nagi.message_id", result.MessageID),
		attribute.Int64("nagi.job_id", result.JobID),
	)
	observability.RecordObservation(span, "langfuse.observation.output", map[string]any{
		"stored":          true,
		"conversation_id": result.ConversationID,
		"message_id":      result.MessageID,
		"job_id":          result.JobID,
		"duplicate":       result.Duplicate,
	})
	identity.SessionID = result.ConversationID
	observability.SetIdentity(span, identity)
	return nil
}

var _ botcontracts.Handler = (*Ingress)(nil)

// tracer returns the shared no-op-safe tracer.
// tracer 返回共享的、无 SDK 时为 no-op 的 tracer。
func tracer() trace.Tracer {
	return otel.Tracer("github.com/HappyLadySauceM/Nagi/internal/conversation")
}
