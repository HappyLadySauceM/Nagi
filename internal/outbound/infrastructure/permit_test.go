package infrastructure

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/HappyLadySauceM/Nagi/internal/outbound/domain"
	plugins "github.com/HappyLadySauceM/Nagi/internal/plugins/contracts"
	scoped "github.com/HappyLadySauceM/Nagi/internal/scope/domain"
	scopeinfra "github.com/HappyLadySauceM/Nagi/internal/scope/infrastructure"
	settings "github.com/HappyLadySauceM/Nagi/internal/settings/domain"
	settingsinfra "github.com/HappyLadySauceM/Nagi/internal/settings/infrastructure"
)

// TestSendPermitSourceRecreated verifies template identity survives neither deletion nor recreation.
// TestSendPermitSourceRecreated 验证模板删除重建后原槽不能沿用新来源许可。
func TestSendPermitSourceRecreated(t *testing.T) {
	for _, path := range []string{"prepared", "refunded"} {
		t.Run(path, func(t *testing.T) {
			f := newLedgerFixture(t)
			ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
			defer cancel()
			f.repo.SetGatePermissions(func() []plugins.RegisteredTool {
				return []plugins.RegisteredTool{{Descriptor: plugins.ToolDescriptor{Permission: "qq.reply.current"}}}
			})
			settingRepo, err := settingsinfra.NewRepo(f.pool)
			if err != nil {
				t.Fatal(err)
			}
			_, err = settingRepo.Insert(ctx, settings.Settings{}, "test-send-permit")
			if err != nil {
				t.Fatal(err)
			}
			policyRepo, err := scopeinfra.NewRepo(f.pool)
			if err != nil {
				t.Fatal(err)
			}
			policy := scoped.Default(scoped.ScopeGroup, 0)
			policy.Enabled = true
			policy.ToolPermissions = map[string]bool{"qq.reply.current": true}
			if err := policyRepo.Upsert(ctx, f.botID, policy); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if _, err := policyRepo.Delete(context.Background(), f.botID, scoped.ScopeGroup, 0); err != nil {
					t.Error(err)
				}
				if _, err := f.pool.Exec(context.Background(), `DELETE FROM system_settings WHERE updated_by='test-send-permit'`); err != nil {
					t.Error(err)
				}
			})
			input := domain.ReserveInput{ClaimToken: f.claim, JobID: f.jobID, BotID: f.botID, ConversationID: f.conversationID, Text: "original", ToolCallID: "original"}
			record, err := f.repo.Reserve(ctx, input)
			if err != nil {
				t.Fatal(err)
			}
			var prior string
			if err := f.pool.QueryRow(ctx, `SELECT send_permit::text FROM agent_outbound_messages WHERE id=$1`, record.ID).Scan(&prior); err != nil {
				t.Fatal(err)
			}
			if path == "refunded" {
				tx, err := f.pool.Begin(ctx)
				if err != nil {
					t.Fatal(err)
				}
				defer tx.Rollback(ctx)
				if _, err := tx.Exec(ctx, `UPDATE agent_outbound_messages SET status='cancelled',error_code='lease_recovered' WHERE id=$1`, record.ID); err != nil {
					t.Fatal(err)
				}
				if _, err := tx.Exec(ctx, `UPDATE agent_run_budgets SET reserved_messages=0,reserved_chars=0 WHERE job_id=$1`, f.jobID); err != nil {
					t.Fatal(err)
				}
				if err := tx.Commit(ctx); err != nil {
					t.Fatal(err)
				}
			}
			if removed, err := policyRepo.Delete(ctx, f.botID, scoped.ScopeGroup, 0); err != nil || !removed {
				t.Fatalf("removed=%v err=%v", removed, err)
			}
			if err := policyRepo.Upsert(ctx, f.botID, policy); err != nil {
				t.Fatal(err)
			}
			if path == "refunded" {
				_, err = f.repo.Reserve(ctx, input)
			} else {
				err = f.repo.MarkSending(ctx, record.ID, f.claim)
			}
			var denied *domain.GateError
			if !errors.As(err, &denied) || denied.Reason != domain.GateRevoked {
				t.Fatalf("expected stale permit, got %v", err)
			}
			var valid bool
			if err := f.pool.QueryRow(ctx, `SELECT o.status='cancelled' AND o.error_code='authorization_revoked' AND o.send_permit::text=$2
     AND b.reserved_messages=0 AND b.reserved_chars=0 AND j.denied_count=1
     FROM agent_outbound_messages o JOIN agent_run_budgets b ON b.job_id=o.job_id JOIN message_jobs j ON j.id=o.job_id WHERE o.id=$1`, record.ID, prior).Scan(&valid); err != nil || !valid {
				t.Fatalf("original permit/budget mismatch: valid=%v err=%v", valid, err)
			}
			input.ToolCallID = "new-request"
			fresh, err := f.repo.Reserve(ctx, input)
			if err != nil {
				t.Fatal(err)
			}
			if fresh.ID == record.ID {
				t.Fatal("fresh permit reused the original slot")
			}
			if err := f.pool.QueryRow(ctx, `SELECT send_permit::text<>$2 FROM agent_outbound_messages WHERE id=$1`, fresh.ID, prior).Scan(&valid); err != nil || !valid {
				t.Fatalf("fresh permit identity mismatch: valid=%v err=%v", valid, err)
			}
			if err := f.repo.MarkSending(ctx, fresh.ID, f.claim); err != nil {
				t.Fatal(err)
			}
		})
	}
}
