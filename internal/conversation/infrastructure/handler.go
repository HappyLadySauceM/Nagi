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

	botcontracts "github.com/HappyLadySauceM/QQBot/internal/bot/contracts"
	obdomain "github.com/HappyLadySauceM/QQBot/internal/onebot/domain"
	scopecontracts "github.com/HappyLadySauceM/QQBot/internal/scope/contracts"
	scopedomain "github.com/HappyLadySauceM/QQBot/internal/scope/domain"

	"github.com/HappyLadySauceM/QQBot/internal/conversation/application"
	"github.com/HappyLadySauceM/QQBot/internal/conversation/domain"
)

// Ingress implements bot/contracts.Handler. Match stays cheap; Handle only
// resolves the scope and persists authorized messages before returning.
// Ingress 实现 bot/contracts.Handler。Match 保持廉价；Handle 仅解析作用域
// 并在返回前持久化已授权消息。
type Ingress struct {
	ingest application.Ingestor
	scopes scopecontracts.Authorizer
	logger *slog.Logger
}

// NewIngress validates dependencies and builds the handler. The originating
// account arrives with each event, so no connection-level status is needed.
// NewIngress 校验依赖并构建处理器。账号身份随事件传入，因此不需要连接级状态。
func NewIngress(ingest application.Ingestor, scopes scopecontracts.Authorizer, logger *slog.Logger) (*Ingress, error) {
	if ingest == nil || scopes == nil || logger == nil {
		return nil, fmt.Errorf("conversation: ingress dependencies required")
	}
	return &Ingress{ingest: ingest, scopes: scopes, logger: logger}, nil
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
	ctx, span := tracer().Start(ctx, "qqbot.ingress")
	defer span.End()
	span.SetAttributes(attribute.Bool("in_group", event.InGroup()))

	botID := int64(bot)
	if botID <= 0 {
		return nil
	}
	if int64(event.Sender.ID) == botID {
		// Ignore the bot's own outgoing messages echoed back as events.
		// 忽略以事件形式回传的机器人自身消息。
		return nil
	}

	var scopeType scopedomain.ScopeType
	var targetID int64
	if event.InGroup() {
		scopeType, targetID = scopedomain.ScopeGroup, int64(event.GroupID)
	} else {
		scopeType, targetID = scopedomain.ScopePrivate, int64(event.UserID)
	}
	policy, err := h.scopes.Resolve(ctx, botID, scopeType, targetID)
	if err != nil {
		// Fail closed: without a policy decision nothing is stored or processed.
		// 关闭式失败：没有策略判定就不存储、不处理。
		h.logger.Warn("ai ingress scope resolve failed", "error", err, "bot_id", botID)
		return nil
	}
	if !policy.Enabled || !policy.StoreMessages {
		// Default deny: scopes without explicit enablement and storage consent
		// never persist or process message content.
		// 默认拒绝：未显式开启并同意存储的作用域，绝不持久化或处理消息内容。
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
	if _, err := h.ingest.Ingest(ctx, input); err != nil {
		h.logger.Warn("ai ingress persist failed", "error", err, "bot_id", botID)
	}
	return nil
}

var _ botcontracts.Handler = (*Ingress)(nil)

// tracer returns the shared no-op-safe tracer.
// tracer 返回共享的、无 SDK 时为 no-op 的 tracer。
func tracer() trace.Tracer {
	return otel.Tracer("github.com/HappyLadySauceM/QQBot/internal/conversation")
}
