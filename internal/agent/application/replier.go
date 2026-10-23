// Package application implements LLM reply generation and delivery.
// agent/application 实现 LLM 回复生成与投递。
package application

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"

	intentdomain "github.com/HappyLadySauceM/QQBot/internal/intent/domain"
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
	Sender    ReceiptSender
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
	sender    ReceiptSender
	recorder  OutboundRecorder
	memory    MemoryFinder
	botName   string
	maxTokens int
	logger    *slog.Logger
}

// NewReplier validates dependencies and builds the replier.
// NewReplier 校验依赖并构建回复器。
func NewReplier(opts Options) (*Replier, error) {
	if opts.Model == nil || opts.Scopes == nil || opts.Sender == nil || opts.Recorder == nil || opts.Logger == nil {
		return nil, fmt.Errorf("agent: dependencies required")
	}
	if opts.MaxTokens <= 0 {
		opts.MaxTokens = 400
	}
	return &Replier{
		model: opts.Model, scopes: opts.Scopes, sender: opts.Sender,
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
	genCtx, genSpan := tracer.Start(ctx, "qqbot.agent.reply")
	genSpan.SetAttributes(attribute.String("gen_ai.system", "openai_compatible"))
	result, err := r.model.Generate(genCtx, domain.ChatRequest{Messages: messages, MaxTokens: r.maxTokens})
	if err != nil {
		genSpan.RecordError(err)
		genSpan.End()
		return fmt.Errorf("agent: generate: %w", err)
	}
	genSpan.SetAttributes(
		attribute.Int64("gen_ai.usage.prompt_tokens", result.PromptTokens),
		attribute.Int64("gen_ai.usage.completion_tokens", result.CompletionTokens),
	)
	genSpan.End()
	reply, err := domain.SanitizeReply(result.Text)
	if err != nil {
		r.logger.Info("reply dropped: empty after sanitize", "job_id", task.JobID)
		return nil
	}

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
	if task.Kind == "group" {
		return r.sender.SendGroupMessageReceipt(ctx, obdomain.GroupID(task.TargetID),
			obdomain.At(obdomain.UserID(task.Message.SenderID)), obdomain.Text(reply))
	}
	return r.sender.SendPrivateMessageReceipt(ctx, obdomain.UserID(task.TargetID), obdomain.Text(reply))
}

func chatChannel(kind string) string {
	if kind == "group" {
		return "群聊"
	}
	return "私聊"
}
