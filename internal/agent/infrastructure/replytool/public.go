package replytool

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	ob "github.com/HappyLadySauceM/Nagi/internal/onebot/domain"
	oa "github.com/HappyLadySauceM/Nagi/internal/outbound/application"
	od "github.com/HappyLadySauceM/Nagi/internal/outbound/domain"
	pa "github.com/HappyLadySauceM/Nagi/internal/plugins/application"
)

// IsSendAction reports whether a public action delivers a message to a
// conversation target.
// IsSendAction 报告公开动作是否向会话目标投递消息。
func IsSendAction(name ob.ActionName) bool {
	return name == ob.ActionSendPrivate || name == ob.ActionSendGroup || name == ob.ActionSendMessage
}

// TargetOf resolves the delivery target of a public send action.
// TargetOf 解析公开发送动作的投递目标。
func TargetOf(c ob.PublicCommand) (string, int64) {
	if c.Name == ob.ActionSendGroup || c.MessageType == ob.TypeGroup {
		return "group", int64(c.GroupID)
	}
	return "private", int64(c.UserID)
}

// SendPublic uses the same source job, budget and lease as current replies.
// SendPublic 与当前会话回复共用来源任务、预算与租约。
func SendPublic(ctx context.Context, opts Options, c ob.PublicCommand, send func(context.Context, ob.PublicCommand) (ob.PublicResult, error)) (ob.PublicResult, error) {
	scope, ok := pa.ScopeFrom(ctx)
	if !ok {
		return ob.PublicResult{}, fmt.Errorf("public send: missing scope")
	}
	// Policy stops on a public send must not block the job: the source
	// conversation may still receive its own reply. They are attributed without
	// setting a blocking delivery outcome.
	// 公开发送的策略拒绝不阻断任务：来源会话仍应收到自己的回复，因此只记录
	// 原因，不写入阻断性投递结论。
	deny := func(reason string) {
		recorder, ok := opts.Ledger.(interface {
			RecordRefusal(context.Context, int64, string, ...string) error
		})
		if !ok || scope.JobID <= 0 {
			return
		}
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
		defer cancel()
		_ = recorder.RecordRefusal(cleanup, scope.JobID, reason, scope.ClaimToken)
	}
	cost, err := pa.CheckMessageContent(ctx, c)
	if err != nil {
		return ob.PublicResult{}, err
	}
	action, err := c.Action()
	if err != nil {
		return ob.PublicResult{}, err
	}
	kind, target := TargetOf(c)
	authorize := func() (od.Limits, error) {
		if err := pa.RequirePermission(ctx, "qq.api."+string(c.Name)); err != nil {
			return od.Limits{}, err
		}
		if kind != scope.Kind || target != scope.TargetID {
			if err := pa.RequirePermission(ctx, "qq.cross_conversation"); err != nil {
				return od.Limits{}, err
			}
			// Target state is checked before the reservation so a refusal costs
			// nothing and cannot turn into an uncertain delivery.
			// 目标状态在预留之前检查，拒绝不消耗额度，也不会变成结果未知的投递。
			if opts.AuthorizeTarget != nil {
				if err := opts.AuthorizeTarget(ctx, kind, target); err != nil {
					return od.Limits{}, err
				}
			}
		}
		if _, err := pa.CheckMessageContent(ctx, c); err != nil {
			return od.Limits{}, err
		}
		if opts.Authorize != nil {
			return opts.Authorize(ctx, scope)
		}
		return opts.Limits, nil
	}
	limits, err := authorize()
	if err != nil {
		deny("authorization_revoked")
		return ob.PublicResult{}, err
	}
	segments := c.Segments
	if c.Text != nil {
		if c.AutoEscape {
			segments = []ob.Segment{ob.Text(*c.Text)}
		} else {
			segments = ob.ParseCQ(*c.Text)
		}
	}
	if c.Text == nil {
		action.Params["message"] = ob.WireSegments(c.Segments)
	}
	payload, err := json.Marshal(map[string]any{"params": action.Params, "segments": ob.WireSegments(segments)})
	if err != nil || len(payload) > 65536 {
		return ob.PublicResult{}, fmt.Errorf("public send: payload too large")
	}
	record, err := opts.Ledger.Reserve(ctx, oa.Request{ClaimToken: scope.ClaimToken, JobID: scope.JobID, BotID: scope.BotID, ConversationID: scope.ConversationID, RunID: fmt.Sprintf("job-%d", scope.JobID), ToolCallID: pa.ToolCallID(ctx), Text: ob.PlainText(segments), TargetKind: kind, TargetID: target, Action: string(c.Name), Payload: string(payload), ChargeChars: cost, Limits: limits})
	if err != nil {
		var budget *od.BudgetError
		if errors.As(err, &budget) {
			// A budget refusal is recorded as a delivery outcome like the narrowed
			// send tool, so the turn ends with an explicit reason.
			// 预算拒绝与收窄发送工具一致地写入投递结论，回合以明确原因结束。
			deny("budget_" + string(budget.Dimension))
		}
		return ob.PublicResult{}, err
	}
	defer func() {
		if ledger, ok := opts.Ledger.(interface {
			Cancel(context.Context, string, ...string) error
		}); ok {
			cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
			defer cancel()
			_ = ledger.Cancel(cleanup, record.ID, record.ClaimToken)
		}
	}()
	if record.Status.Delivered() {
		return ob.PublicResult{Data: map[string]any{"message_id": record.PlatformMessageID}, MessageID: record.PlatformMessageID, Async: record.Status != od.StatusSent}, nil
	}
	if record.Status != od.StatusPrepared {
		return ob.PublicResult{}, fmt.Errorf("public send: already attempted")
	}
	if !record.NotBefore.IsZero() {
		timer := time.NewTimer(time.Until(record.NotBefore))
		defer timer.Stop()
		select {
		case <-timer.C:
		case <-ctx.Done():
			return ob.PublicResult{}, ctx.Err()
		}
	}
	limits, err = authorize()
	if err != nil {
		deny("authorization_revoked")
		return ob.PublicResult{}, err
	}
	if validator, ok := opts.Ledger.(interface {
		ValidateReserved(context.Context, string, od.Limits) error
	}); ok {
		if err := validator.ValidateReserved(ctx, record.ID, limits); err != nil {
			deny("budget_changed")
			return ob.PublicResult{}, err
		}
	}
	var result ob.PublicResult
	outcome, err := oa.Deliver(ctx, opts.Ledger, record.ID, func(ctx context.Context) (int64, error) {
		var e error
		result, e = send(ctx, c)
		if e == nil && result.Async {
			return 0, nil
		}
		return result.MessageID, e
	}, record.ClaimToken)
	if outcome.Status == od.StatusUnknown {
		result.Async = true
		// Keep the platform response; only fill a placeholder when the action
		// produced none, so async and timeout stay indistinguishable to the model.
		// 保留平台响应，仅在动作未产生数据时补占位；异步与超时对模型保持不可区分。
		if result.Data == nil {
			result.Data = map[string]any{"message_id": outcome.PlatformMessageID}
		}
		return result, nil
	}
	return result, err
}
