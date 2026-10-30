// Built-in reply tool: the agent decides to send a message inside its own turn.
// The target is always the current conversation, never a model-supplied id.
// 内置回复工具：由 Agent 在自己的回合内决定发送一条消息。
// 目标始终是当前会话，绝不接受模型传入的目标。
package replytool

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	agentdomain "github.com/HappyLadySauceM/Nagi/internal/agent/domain"
	obdomain "github.com/HappyLadySauceM/Nagi/internal/onebot/domain"
	outboundapp "github.com/HappyLadySauceM/Nagi/internal/outbound/application"
	outbounddomain "github.com/HappyLadySauceM/Nagi/internal/outbound/domain"
	pluginsapp "github.com/HappyLadySauceM/Nagi/internal/plugins/application"
	"github.com/HappyLadySauceM/Nagi/internal/plugins/contracts"
)

// ProviderID is the provider that exposes the reply tool.
// ProviderID 是暴露回复工具的提供方标识。
const ProviderID = "builtin.reply"

// ToolID is the model-facing tool name.
// ToolID 是模型看到的工具名。
const ToolID = "qq.send_current"

// Permission gates the reply tool independently from other write tools.
// Permission 独立于其它写工具对回复工具授权。
const Permission = "qq.reply.current"

// Sender is the narrow send surface of one account.
// Sender 是单个账号的窄发送接口。
type Sender interface {
	SendPrivateMessageReceipt(ctx context.Context, to obdomain.UserID, segments ...obdomain.Segment) (int64, error)
	SendGroupMessageReceipt(ctx context.Context, to obdomain.GroupID, segments ...obdomain.Segment) (int64, error)
}

// SenderResolver resolves the sender bound to one account.
// SenderResolver 解析账号绑定的发送能力。
type SenderResolver interface {
	ReceiptSender(botID int64) (Sender, bool)
}

// Ledger reserves and settles delivery records.
// Ledger 预留并落定投递记录。
type Ledger interface {
	Reserve(ctx context.Context, req outboundapp.Request) (outbounddomain.Record, error)
	MarkSending(ctx context.Context, id string) error
	Settle(ctx context.Context, id string, outcome outboundapp.Outcome) error
}

// Recorder persists the outgoing message for later context.
// Recorder 持久化出站消息，供后续上下文使用。
type Recorder interface {
	RecordOutgoing(ctx context.Context, conversationID string, botID int64, text string, platformMessageID int64, sentStatus string, outboundID string) error
}

// Options wires the tool dependencies.
// Options 装配工具依赖。
type Options struct {
	Senders   SenderResolver
	Ledger    Ledger
	Recorder  Recorder
	Limits    outbounddomain.Limits
	Authorize func(context.Context, pluginsapp.Scope) (outbounddomain.Limits, error)
}

// provider exposes qq.send_current.
// provider 暴露 qq.send_current。
type provider struct {
	opts Options
}

// NewProvider builds the reply provider; every dependency is required.
// NewProvider 构建回复提供方；所有依赖都必需。
func NewProvider(opts Options) contracts.ToolProvider {
	return provider{opts: opts}
}

// Manifest identifies the provider.
// Manifest 标识提供方。
func (provider) Manifest() contracts.Manifest {
	return contracts.Manifest{ID: ProviderID, Version: "1", Description: "当前会话内的受限发送"}
}

// Close releases nothing; the provider holds no resources.
// Close 不释放任何资源；提供方不持有资源。
func (provider) Close(context.Context) error { return nil }

// Tools returns the reply tool.
// Tools 返回回复工具。
func (p provider) Tools(context.Context) ([]contracts.RegisteredTool, error) {
	return []contracts.RegisteredTool{{
		Descriptor: contracts.ToolDescriptor{
			ID:           ToolID,
			ReplyCurrent: true,
			Parameters:   map[string]contracts.Parameter{"text": {Type: "string", Description: "当前会话内要发送的一条消息", Required: true}},
			Description:  "在当前会话发送一条消息（仅本会话、受单轮预算限制）；返回第几条与发送状态",
			Category:     contracts.CategoryReply,
			RiskLevel:    contracts.RiskWrite,
			ReadOnly:     false,
			SideEffect:   true,
			Permission:   Permission,
		},
		Invoke: p.send,
	}}, nil
}

// sendArgs is the model-supplied payload.
// sendArgs 是模型提供的参数。
type sendArgs struct {
	Text string `json:"text"`
}

// receipt tells the model what happened without leaking other conversations.
// receipt 告诉模型本次投递结果，不泄露其它会话信息。
type receipt struct {
	Sequence          int    `json:"sequence"`
	Status            string `json:"status"`
	PlatformMessageID int64  `json:"platform_message_id,omitempty"`
	Error             string `json:"error,omitempty"`
}

// send reserves, sends and settles one message of the current turn.
// send 预留、发送并落定当前回合的一条消息。
func (p provider) send(ctx context.Context, argumentsInJSON string) (result string, returnErr error) {
	scope, ok := pluginsapp.ScopeFrom(ctx)
	if !ok {
		return "", fmt.Errorf("reply tool: missing run scope")
	}
	if scope.JobID <= 0 || scope.ConversationID == "" || scope.TargetID <= 0 {
		return "error: 当前运行缺少会话信息，无法发送", nil
	}
	denied := false
	var denialRecordErr error
	defer func() {
		if denialRecordErr != nil {
			returnErr = denialRecordErr
		}
	}()
	deny := func(reason string) {
		denied = true
		if recorder, ok := p.opts.Ledger.(interface {
			RecordDenied(context.Context, int64, string) error
		}); ok {
			cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
			defer cancel()
			denialRecordErr = recorder.RecordDenied(cleanup, scope.JobID, reason)
		}
	}
	var args sendArgs
	if err := json.Unmarshal([]byte(argumentsInJSON), &args); err != nil {
		deny("invalid_arguments")
		return "", fmt.Errorf("reply tool: invalid arguments")
	}
	text, err := agentdomain.SanitizeReply(args.Text)
	if err != nil {
		deny("output_rejected")
		return "", err
	}
	sender, ok := p.opts.Senders.ReceiptSender(scope.BotID)
	if !ok {
		deny("account_offline")
		return "", fmt.Errorf("reply tool: account offline")
	}

	limits := p.opts.Limits
	if p.opts.Authorize != nil {
		limits, err = p.opts.Authorize(ctx, scope)
		if err != nil {
			deny("authorization_revoked")
			return "", err
		}
	}
	record, err := p.opts.Ledger.Reserve(ctx, outboundapp.Request{
		JobID:          scope.JobID,
		BotID:          scope.BotID,
		ConversationID: scope.ConversationID,
		Text:           text,
		Limits:         limits,
		RunID:          fmt.Sprintf("job-%d", scope.JobID),
		ToolCallID:     pluginsapp.ToolCallID(ctx),
	})
	if err != nil {
		var budget *outbounddomain.BudgetError
		if errors.As(err, &budget) {
			// A exhausted budget ends this turn's sending without failing the job.
			// 预算用尽只结束本回合的发送，不让任务失败。
			deny("budget_" + string(budget.Dimension))
			return fmt.Sprintf("error: 本轮发送预算已用尽（%s）", budget.Dimension), err
		}
		return "", fmt.Errorf("reply tool: reserve: %w", err)
	}
	platformStarted := record.Status != outbounddomain.StatusPrepared
	defer func() {
		if !platformStarted && !denied {
			deny("cancelled_before_send")
		}
		if ledger, ok := p.opts.Ledger.(interface {
			Cancel(context.Context, string) error
		}); ok {
			cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
			defer cancel()
			_ = ledger.Cancel(cleanup, record.ID)
		}
	}()
	if record.Status.Delivered() {
		return jsonString(receipt{Sequence: record.Sequence, Status: string(record.Status), PlatformMessageID: record.PlatformMessageID}), nil
	}
	if !record.NotBefore.IsZero() {
		timer := time.NewTimer(time.Until(record.NotBefore))
		defer timer.Stop()
		select {
		case <-timer.C:
		case <-ctx.Done():
			return "", ctx.Err()
		}
	}
	if p.opts.Authorize != nil {
		if limits, err = p.opts.Authorize(ctx, scope); err != nil {
			deny("authorization_revoked")
			return "", err
		}
	}
	if validator, ok := p.opts.Ledger.(interface {
		ValidateReserved(context.Context, string, outbounddomain.Limits) error
	}); ok {
		if err := validator.ValidateReserved(ctx, record.ID, limits); err != nil {
			deny("budget_changed")
			return "", err
		}
	}
	outcome, sendErr := outboundapp.Deliver(ctx, p.opts.Ledger, record.ID, func(sendCtx context.Context) (int64, error) {
		platformStarted = true
		return p.deliver(sendCtx, sender, scope, text)
	})
	status, errorCode, messageID := outcome.Status, outcome.ErrorCode, outcome.PlatformMessageID
	if !status.Settleable() {
		return "", sendErr
	}
	cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
	defer cancel()
	if recordErr := p.opts.Recorder.RecordOutgoing(cleanup, scope.ConversationID, scope.BotID,
		text, messageID, string(status), record.ID); recordErr != nil {
		// The delivery already happened; failing the tool would make the model
		// retry and duplicate the message.
		// 消息已发出；让工具失败会诱使模型重试并产生重复消息。
		_ = recordErr
	}
	if sendErr != nil {
		return jsonString(receipt{Sequence: record.Sequence, Status: string(status), Error: errorCode}), sendErr
	}
	return jsonString(receipt{Sequence: record.Sequence, Status: string(status), PlatformMessageID: messageID}), nil
}

// deliver sends through the account that received the message.
// deliver 通过接收消息的账号发送。
func (p provider) deliver(ctx context.Context, sender Sender, scope pluginsapp.Scope, text string) (int64, error) {
	if scope.Kind == contracts.ScopeKindGroup {
		return sender.SendGroupMessageReceipt(ctx, obdomain.GroupID(scope.TargetID), obdomain.Text(text))
	}
	return sender.SendPrivateMessageReceipt(ctx, obdomain.UserID(scope.TargetID), obdomain.Text(text))
}

// jsonString marshals a receipt; marshalling cannot fail for this struct.
// jsonString 序列化回执；该结构体的序列化不会失败。
func jsonString(r receipt) string {
	data, err := json.Marshal(r)
	if err != nil {
		return fmt.Sprintf(`{"status":%q}`, r.Status)
	}
	return strings.TrimSpace(string(data))
}
