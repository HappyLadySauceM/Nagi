package assembly

import (
	"context"
	"errors"
	"time"

	"github.com/HappyLadySauceM/Nagi/cmd/app/svc"
	"github.com/HappyLadySauceM/Nagi/internal/agent/infrastructure/replytool"
	auditcontracts "github.com/HappyLadySauceM/Nagi/internal/audit/contracts"
	conversationdomain "github.com/HappyLadySauceM/Nagi/internal/conversation/domain"
	memorycontracts "github.com/HappyLadySauceM/Nagi/internal/memory/contracts"
	memorydomain "github.com/HappyLadySauceM/Nagi/internal/memory/domain"
	memoryinfra "github.com/HappyLadySauceM/Nagi/internal/memory/infrastructure"
	obdomain "github.com/HappyLadySauceM/Nagi/internal/onebot/domain"
	outboundapp "github.com/HappyLadySauceM/Nagi/internal/outbound/application"
	outbounddomain "github.com/HappyLadySauceM/Nagi/internal/outbound/domain"
	outboundinfra "github.com/HappyLadySauceM/Nagi/internal/outbound/infrastructure"
	scopeapp "github.com/HappyLadySauceM/Nagi/internal/scope/application"
	scopedomain "github.com/HappyLadySauceM/Nagi/internal/scope/domain"
	settingsapp "github.com/HappyLadySauceM/Nagi/internal/settings/application"
)

func consentPromptProcessor(enabled bool, repo *memoryinfra.Repo, accounts *svc.Accounts, scopes *scopeapp.Service, ledger *outboundapp.Coordinator, policies *outboundinfra.PoliciesRepo, settings *settingsapp.Provider) func(context.Context, conversationdomain.Job) error {
	return func(ctx context.Context, job conversationdomain.Job) error {
		if !enabled {
			return nil
		}
		ctx, cancel := context.WithTimeout(ctx, 3*time.Minute)
		defer cancel()
		delivered, err := ledger.HasDelivered(ctx, job.ID)
		if err != nil || delivered {
			return err
		}
		p, text, err := repo.ConsentPrompt(ctx, job.BotID, job.ConsentProposalID, job.ID, job.ClaimToken)
		if errors.Is(err, memorycontracts.ErrConsentMissing) || errors.Is(err, memorycontracts.ErrConsentConflict) {
			return nil
		}
		if err != nil {
			return err
		}
		if p.SourceConversationID != job.ConversationID || p.SourceGroupID != job.TargetID || job.Kind != conversationdomain.KindGroup {
			return outbounddomain.ErrConsentSendDenied
		}
		limitsFor := func() (outbounddomain.Limits, error) {
			policy, err := scopes.Resolve(ctx, job.BotID, scopedomain.ScopeGroup, job.TargetID)
			if err != nil {
				return outbounddomain.Limits{}, err
			}
			live := settings.Settings()
			allowed, exists := policy.ToolPermissions[replytool.Permission]
			if !exists {
				allowed, exists = live.Tools.PermissionDefaults[replytool.Permission]
			}
			if !policy.Enabled || !policy.StoreMessages || policy.ReplyMode == scopedomain.ModeObserveOnly || exists && !allowed {
				return outbounddomain.Limits{}, outbounddomain.ErrConsentSendDenied
			}
			limits, err := sendingLimitsFor(ctx, policies, live, job.BotID, "group", job.TargetID)
			limits.MaxMessages = 1
			if policy.MaxCharsPerMessage > 0 && (limits.MaxCharsPerMessage <= 0 || policy.MaxCharsPerMessage < limits.MaxCharsPerMessage) {
				limits.MaxCharsPerMessage = policy.MaxCharsPerMessage
			}
			if policy.MaxTotalCharsPerRun > 0 && (limits.MaxTotalChars <= 0 || policy.MaxTotalCharsPerRun < limits.MaxTotalChars) {
				limits.MaxTotalChars = policy.MaxTotalCharsPerRun
			}
			limits.StopAfterFailure = true
			if policy.MaxRepliesPerHour > 0 && (limits.MaxPerHour <= 0 || policy.MaxRepliesPerHour < limits.MaxPerHour) {
				limits.MaxPerHour = policy.MaxRepliesPerHour
			}
			limits.MinIntervalMS = max(limits.MinIntervalMS, policy.MinReplyIntervalMS)
			return limits, err
		}
		limits, err := limitsFor()
		if err != nil {
			return err
		}
		sender, ok := accounts.ReceiptSender(job.BotID)
		if !ok {
			return errors.New("consent worker: OneBot account unavailable")
		}
		record, err := ledger.Reserve(ctx, outboundapp.Request{JobID: job.ID, BotID: job.BotID, ConversationID: job.ConversationID, RunID: auditcontracts.RunID(ctx), ClaimToken: job.ClaimToken, ToolCallID: memorydomain.ConsentPromptKind, TargetKind: "group", TargetID: job.TargetID, Text: text, Limits: limits})
		if err != nil {
			return err
		}
		if record.Status != outbounddomain.StatusPrepared {
			return nil
		}
		defer func() {
			cleanup, stop := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
			defer stop()
			_ = ledger.Cancel(cleanup, record.ID, record.ClaimToken)
		}()
		if wait := time.Until(record.NotBefore); wait > 0 {
			timer := time.NewTimer(wait)
			defer timer.Stop()
			select {
			case <-timer.C:
			case <-ctx.Done():
				return ctx.Err()
			}
		}
		recheck := func() error {
			current, err := limitsFor()
			if err != nil {
				return err
			}
			return ledger.ValidateReserved(ctx, record.ID, current)
		}
		if err := recheck(); err != nil {
			if errors.Is(err, outbounddomain.ErrConsentSendDenied) {
				return nil
			}
			return err
		}
		_, err = outboundapp.DeliverWithRecheck(ctx, ledger, record.ID, recheck, func(sendCtx context.Context) (int64, error) {
			auditcontracts.MarkSideEffect(sendCtx)
			return sender.SendGroupMessageReceipt(sendCtx, obdomain.GroupID(job.TargetID), obdomain.Text(text))
		}, record.ClaimToken)
		if errors.Is(err, outbounddomain.ErrConsentSendDenied) {
			return nil
		}
		return err
	}
}
