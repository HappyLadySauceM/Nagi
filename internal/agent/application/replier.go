// Package application implements LLM reply generation and delivery.
// agent/application 实现 LLM 回复生成与投递。
package application

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"unicode/utf8"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"

	intentdomain "github.com/HappyLadySauceM/QQBot/internal/intent/domain"
	"github.com/HappyLadySauceM/QQBot/internal/observability"
	obcontracts "github.com/HappyLadySauceM/QQBot/internal/onebot/contracts"
	obdomain "github.com/HappyLadySauceM/QQBot/internal/onebot/domain"
	scopecontracts "github.com/HappyLadySauceM/QQBot/internal/scope/contracts"
	scopedomain "github.com/HappyLadySauceM/QQBot/internal/scope/domain"

	"github.com/HappyLadySauceM/QQBot/internal/agent/domain"
)

// ChatModel generates replies through an LLM provider.
// ChatModel 通过 LLM 服务生成回复。
type ChatModel interface {
	Generate(ctx context.Context, req domain.ChatRequest) (domain.ChatResult, error)
}

// ReceiptSender delivers messages and reports the platform message id assigned
// by the OneBot implementation; implemented by onebot ClientService.
// ReceiptSender 发送消息并回报 OneBot 实现分配的 platform message id；
// 由 onebot ClientService 实现。
type ReceiptSender interface {
	SendPrivateMessageReceipt(ctx context.Context, to obdomain.UserID, segments ...obdomain.Segment) (int64, error)
	SendGroupMessageReceipt(ctx context.Context, to obdomain.GroupID, segments ...obdomain.Segment) (int64, error)
}

// SenderResolver resolves the sender bound to one account. It must never fall
// back to another account when the requested one is offline.
// SenderResolver 解析账号绑定的发送能力；目标账号离线时不得回退到其他账号。
type SenderResolver interface {
	ReceiptSender(botID int64) (ReceiptSender, bool)
}

// OutboundRecorder persists the bot's outgoing reply for future context.
// OutboundRecorder 持久化机器人出站回复，保证后续上下文可见。
type OutboundRecorder interface {
	RecordOutgoing(ctx context.Context, conversationID string, botID int64, text string, platformMessageID int64, sentStatus string) error
}

// MemoryFinder retrieves authorized long-term memory snippets; optional.
// MemoryFinder 检索已授权的长期记忆片段；可选。
type MemoryFinder interface {
	Find(ctx context.Context, botID int64, kind string, targetID int64, query string, limit int) ([]string, error)
}

// Options configure the replier.
// Options 配置回复器。
type Options struct {
	Model     ChatModel
	Scopes    scopecontracts.Authorizer
	Senders   SenderResolver
	Recorder  OutboundRecorder
	Memory    MemoryFinder // 可为 nil / may be nil
	BotName   string
	MaxTokens int
	Logger    *slog.Logger
}

// Replier generates, checks, sends and records replies for intent decisions.
// Replier 为意图决策生成、检查、发送并记录回复。
type Replier struct {
	model     ChatModel
	scopes    scopecontracts.Authorizer
	senders   SenderResolver
	recorder  OutboundRecorder
	memory    MemoryFinder
	botName   string
	maxTokens int
	logger    *slog.Logger
}

// NewReplier validates dependencies and builds the replier.
// NewReplier 校验依赖并构建回复器。
func NewReplier(opts Options) (*Replier, error) {
	if opts.Model == nil || opts.Scopes == nil || opts.Senders == nil || opts.Recorder == nil || opts.Logger == nil {
		return nil, fmt.Errorf("agent: dependencies required")
	}
	if opts.MaxTokens <= 0 {
		opts.MaxTokens = 400
	}
	return &Replier{
		model: opts.Model, scopes: opts.Scopes, senders: opts.Senders,
		recorder: opts.Recorder, memory: opts.Memory,
		botName: opts.BotName, maxTokens: opts.MaxTokens, logger: opts.Logger,
	}, nil
}

// ExecuteReply re-checks the scope, builds context, generates, sanitizes, sends
// and records the reply. It fails closed: any generation error means no send.
// ExecuteReply 复核作用域、构建上下文、生成、净化、发送并记录回复。
// 采用关闭式失败：生成出错就不发送。
func (r *Replier) ExecuteReply(ctx context.Context, task intentdomain.Task, history []intentdomain.Message) error {
	policy, err := r.scopes.Resolve(ctx, task.BotID, scopedomain.ScopeType(task.Kind), task.TargetID)
	if err != nil {
		return fmt.Errorf("agent: resolve policy: %w", err)
	}
	if !policy.Enabled || policy.ReplyMode == scopedomain.ModeObserveOnly {
		// The admin may have disabled the scope while the reply was pending.
		// 管理员可能在回复排队期间关闭了该作用域。
		r.logger.Info("reply dropped: scope disabled",
			"bot_id", task.BotID, "target_id", task.TargetID)
		return nil
	}

	messages := r.buildMessages(ctx, task, history)
	tracer := otel.Tracer("github.com/HappyLadySauceM/QQBot/internal/agent")
	genCtx, genSpan := tracer.Start(ctx, "qqbot.agent.run")
	genSpan.SetAttributes(
		attribute.String("gen_ai.system", "openai_compatible"),
		// The explicit type keeps the span a generation even when the provider
		// returns no model name, so Langfuse renders structured messages. When
		// the Agent Runtime lands, this marker moves to the model child span so
		// one model call is never counted twice.
		// 显式类型保证 span 在提供商未返回模型名时仍是 generation，
		// Langfuse 才会按结构化消息渲染。接入 Agent Runtime 后，
		// 该标记应下移到模型子 span，避免一次模型调用被重复计数。
		attribute.String("langfuse.observation.type", "generation"),
	)
	truncated := false
	if observability.ContentEnabled() {
		// Bounded JSON keeps the payload below the exporter truncation limit;
		// truncated JSON would break the Langfuse session transcript.
		// 有界 JSON 保证载荷低于导出截断阈值；截断的 JSON 会破坏会话 transcript。
		if payload, clipped, jsonErr := traceMessagesJSON(messages); jsonErr == nil {
			truncated = clipped
			observability.RecordContent(genSpan, "langfuse.observation.input", payload)
		}
	}
	result, err := r.model.Generate(genCtx, domain.ChatRequest{Messages: messages, MaxTokens: r.maxTokens})
	if err != nil {
		genSpan.RecordError(err)
		genSpan.End()
		return fmt.Errorf("agent: generate: %w", err)
	}
	// Token usage is reported once, through the Langfuse-preferred detail map.
	// 用量只上报一次，使用 Langfuse 优先采用的明细结构。
	genSpan.SetAttributes(attribute.String("langfuse.observation.usage_details", usageDetailsJSON(result)))
	if result.Model != "" {
		genSpan.SetAttributes(attribute.String("langfuse.observation.model.name", result.Model))
	}
	if observability.ContentEnabled() {
		if payload, clipped, jsonErr := traceReplyJSON(result.Text); jsonErr == nil {
			truncated = truncated || clipped
			observability.RecordContent(genSpan, "langfuse.observation.output", payload)
		}
	}
	// The marker distinguishes "the model said only this much" from "the traced
	// payload was clipped".
	// 该标记用于区分"模型只说了这么多"与"追踪载荷被裁剪"。
	genSpan.SetAttributes(attribute.Bool("qqbot.trace.truncated", truncated))
	genSpan.End()

	_, validateSpan := tracer.Start(ctx, "qqbot.response.validate")
	reply, err := domain.SanitizeReply(result.Text)
	if err != nil {
		// A rejected reply is a policy outcome, not a generation failure.
		// 被拒绝的回复是策略结果，不是生成失败。
		validateSpan.SetAttributes(attribute.Bool("qqbot.reply_dropped", true))
		validateSpan.End()
		r.logger.Info("reply dropped: empty after sanitize", "job_id", task.JobID)
		return nil
	}
	validateSpan.SetAttributes(
		attribute.Bool("qqbot.reply_dropped", false),
		attribute.Int("qqbot.reply_length", len([]rune(reply))),
	)
	validateSpan.End()

	sentStatus := "sent"
	sendCtx, sendSpan := tracer.Start(ctx, "qqbot.onebot.send")
	messageID, sendErr := r.send(sendCtx, task, reply)
	if sendErr != nil {
		sendSpan.RecordError(sendErr)
		// Send failures are recorded but never retried blindly. A timeout means
		// the outcome is unknown: the message may have been delivered, so it is
		// persisted as unknown instead of failed.
		// 发送失败会记录但绝不盲目重试。超时表示结果未知：消息可能已送达，
		// 因此按 unknown 而非 failed 持久化。
		if errors.Is(sendErr, obcontracts.ErrActionTimeout) {
			sentStatus = "unknown"
		} else {
			sentStatus = "failed"
		}
		r.logger.Warn("reply send failed", "error", sendErr, "job_id", task.JobID, "sent_status", sentStatus)
	}
	// Delivery status must be attached before the span ends, otherwise the
	// outcome never reaches the trace.
	// 投递结果必须在 span 结束前写入，否则结果不会出现在 trace 中。
	sendSpan.SetAttributes(attribute.String("qqbot.sent_status", sentStatus))
	if sendErr == nil {
		sendSpan.SetAttributes(attribute.Int64("qqbot.onebot_message_id", messageID))
	}
	sendSpan.End()
	if err := r.recorder.RecordOutgoing(ctx, task.ConversationID, task.BotID, reply, messageID, sentStatus); err != nil {
		r.logger.Warn("record outgoing reply failed", "error", err, "job_id", task.JobID)
	}
	r.logger.Info("reply executed", "job_id", task.JobID, "sent_status", sentStatus,
		"prompt_tokens", result.PromptTokens, "completion_tokens", result.CompletionTokens)
	return nil
}

// buildMessages assembles system prompt, optional memory, history and current message.
// buildMessages 组装系统提示、可选记忆、历史与当前消息。
func (r *Replier) buildMessages(ctx context.Context, task intentdomain.Task, history []intentdomain.Message) []domain.ChatMessage {
	var b strings.Builder
	name := r.botName
	if name == "" {
		name = "助手"
	}
	b.WriteString(fmt.Sprintf("你是 QQ %s中的智能助手「%s」。", chatChannel(task.Kind), name))
	b.WriteString("用简体中文、自然口语、简洁回复；不要输出 Markdown、链接或 @ 全体成员；")
	b.WriteString("不要虚构事实；如果不确定就坦率说明。")
	if r.memory != nil {
		// Memory lookup errors degrade the context, never block the reply. The
		// task context keeps the lookup cancellable and shutdown-aware.
		// 记忆检索错误只降级上下文，不阻断回复。任务上下文保证检索可取消且受关停约束。
		if snippets, err := r.memory.Find(ctx, task.BotID, task.Kind, task.TargetID,
			task.Message.Text, 5); err == nil && len(snippets) > 0 {
			b.WriteString("\n以下是你已知且被授权使用的背景信息：")
			for _, s := range snippets {
				b.WriteString("\n- " + s)
			}
		}
	}
	messages := []domain.ChatMessage{{Role: "system", Content: b.String()}}
	for _, m := range history {
		// The current message is already persisted, so it is part of the recent
		// history passed in here; appending it a second time wastes tokens and
		// makes the model see its own prompt twice.
		// 当前消息已落库，因此就在传入的最近历史中；再次追加既浪费 token，
		// 也让模型看到重复的提问。
		if task.Message.MessageID != 0 && m.MessageID == task.Message.MessageID {
			continue
		}
		if m.IsBot {
			messages = append(messages, domain.ChatMessage{Role: "assistant", Content: m.Text})
			continue
		}
		messages = append(messages, domain.ChatMessage{
			Role:    "user",
			Content: fmt.Sprintf("[用户%d] %s", m.SenderID, m.Text),
		})
	}
	messages = append(messages, domain.ChatMessage{
		Role:    "user",
		Content: fmt.Sprintf("[用户%d] %s", task.Message.SenderID, task.Message.Text),
	})
	return messages
}

// send delivers the reply and returns the assigned platform message id;
// groups prepend an at segment to the sender.
// send 投递回复并返回分配的 platform message id；群聊在开头附加对发送者的 @ 段。
func (r *Replier) send(ctx context.Context, task intentdomain.Task, reply string) (int64, error) {
	sender, ok := r.senders.ReceiptSender(task.BotID)
	if !ok {
		// Sending through another account would leak messages across identities.
		// 借用其他账号发送会让消息跨身份泄漏，因此直接失败。
		return 0, fmt.Errorf("agent: bot %d is not connected", task.BotID)
	}
	if task.Kind == "group" {
		return sender.SendGroupMessageReceipt(ctx, obdomain.GroupID(task.TargetID),
			obdomain.At(obdomain.UserID(task.Message.SenderID)), obdomain.Text(reply))
	}
	return sender.SendPrivateMessageReceipt(ctx, obdomain.UserID(task.TargetID), obdomain.Text(reply))
}

func chatChannel(kind string) string {
	if kind == "group" {
		return "群聊"
	}
	return "私聊"
}

// maxTraceMessages bounds how many chat messages are exported to the trace;
// the system prompt plus the newest turns is enough to explain one reply.
// maxTraceMessages 限制导出到 trace 的消息条数；系统提示加最近若干轮足以解释一次回复。
const maxTraceMessages = 12

// maxTraceMessageRunes bounds one exported message body.
// maxTraceMessageRunes 限制单条导出消息的长度。
const maxTraceMessageRunes = 600

// traceMessagesJSON renders the prompt as a JSON message array for Langfuse.
// The payload is bounded before marshaling so it never exceeds the exporter's
// truncation limit: a half-truncated document would break the session view.
// traceMessagesJSON 把 prompt 渲染成 Langfuse 使用的 JSON 消息数组。
// 载荷在序列化前就有界，绝不触发导出截断：半截 JSON 会让会话视图解析失败。
func traceMessagesJSON(messages []domain.ChatMessage) (string, bool, error) {
	if len(messages) == 0 {
		return "", false, nil
	}
	start := 0
	truncated := false
	if len(messages) > maxTraceMessages {
		start = len(messages) - (maxTraceMessages - 1)
		truncated = true
	}
	trimmed := make([]domain.ChatMessage, 0, maxTraceMessages)
	if start > 0 && messages[0].Role == "system" {
		clipped, cut := clipChatMessage(messages[0])
		trimmed = append(trimmed, clipped)
		truncated = truncated || cut
	}
	for _, message := range messages[start:] {
		clipped, cut := clipChatMessage(message)
		trimmed = append(trimmed, clipped)
		truncated = truncated || cut
	}
	payload, err := json.Marshal(trimmed)
	if err != nil {
		return "", truncated, fmt.Errorf("agent: marshal trace input: %w", err)
	}
	return string(payload), truncated, nil
}

// traceReplyJSON renders one assistant reply as a JSON message array.
// traceReplyJSON 把一条助手回复渲染成 JSON 消息数组。
func traceReplyJSON(text string) (string, bool, error) {
	clipped, truncated := clipChatMessage(domain.ChatMessage{Role: "assistant", Content: text})
	payload, err := json.Marshal([]domain.ChatMessage{clipped})
	if err != nil {
		return "", truncated, fmt.Errorf("agent: marshal trace output: %w", err)
	}
	return string(payload), truncated, nil
}

// clipChatMessage truncates one message body and reports whether it was cut.
// clipChatMessage 截断单条消息正文并报告是否发生裁剪。
func clipChatMessage(message domain.ChatMessage) (domain.ChatMessage, bool) {
	if utf8.RuneCountInString(message.Content) <= maxTraceMessageRunes {
		return message, false
	}
	message.Content = string([]rune(message.Content)[:maxTraceMessageRunes]) + "…"
	return message, true
}

// usageDetailsJSON renders token usage in Langfuse's usage-details shape.
// usageDetailsJSON 按 Langfuse 的用量结构渲染 token 统计。
func usageDetailsJSON(result domain.ChatResult) string {
	payload, err := json.Marshal(struct {
		Input  int64 `json:"input"`
		Output int64 `json:"output"`
		Total  int64 `json:"total"`
	}{
		Input:  result.PromptTokens,
		Output: result.CompletionTokens,
		Total:  result.PromptTokens + result.CompletionTokens,
	})
	if err != nil {
		return ""
	}
	return string(payload)
}
