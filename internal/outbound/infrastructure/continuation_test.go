package infrastructure

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	conversation "github.com/HappyLadySauceM/Nagi/internal/conversation/domain"
	conversationinfra "github.com/HappyLadySauceM/Nagi/internal/conversation/infrastructure"
	memory "github.com/HappyLadySauceM/Nagi/internal/memory/contracts"
	"github.com/HappyLadySauceM/Nagi/internal/outbound/domain"
	plugins "github.com/HappyLadySauceM/Nagi/internal/plugins/contracts"
)

func TestContinuationOriginalSlot(t *testing.T) {
	for _, scenario := range []string{"unrelated", "expired", "changed", "revoked", "budget", "memory_changed", "audio", "unknown", "already_sending", "before_handoff", "offline", "offline_before_write", "topic_expired"} {
		t.Run(scenario, func(t *testing.T) {
			f := newLedgerFixture(t)
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			repo, err := NewRepo(f.pool)
			if err != nil {
				t.Fatal(err)
			}
			conv, err := conversationinfra.NewRepo(f.pool)
			if err != nil {
				t.Fatal(err)
			}
			exec := func(query string, args ...any) {
				t.Helper()
				if _, err := f.pool.Exec(ctx, query, args...); err != nil {
					t.Fatal(err)
				}
			}
			var source, target int64
			if err := f.pool.QueryRow(ctx, `SELECT m.id,c.target_id FROM message_jobs j JOIN messages m ON m.id=j.incoming_message_id JOIN conversations c ON c.id=m.conversation_id WHERE j.id=$1`, f.jobID).Scan(&source, &target); err != nil {
				t.Fatal(err)
			}
			exec(`UPDATE group_topic_routes SET status='unrelated' WHERE id=$1`, source)
			exec(`INSERT INTO group_topics (id,conversation_id,version,expires_at) VALUES ($1,$2,1,clock_timestamp()+interval '5 minutes')`, f.jobID, f.conversationID)
			exec(`INSERT INTO group_topic_inputs (id,conversation_id,topic_id,version,route_id,action) VALUES (gen_random_uuid(),$1,$2,1,$3,'opened')`, f.conversationID, f.jobID, source)
			policyScenario := scenario == "revoked" || scenario == "budget" || scenario == "memory_changed"
			reserveCtx := ctx
			memoryID := uuid.NewString()
			if policyScenario {
				settingsOwner := "continuation-test-" + uuid.NewString()
				inserted, err := f.pool.Exec(ctx, `INSERT INTO system_settings(id,payload,version,updated_by)
 VALUES (1,'{}',1,$1) ON CONFLICT(id) DO NOTHING`, settingsOwner)
				if err != nil {
					t.Fatal(err)
				}
				if inserted.RowsAffected() == 1 {
					t.Cleanup(func() {
						if _, err := f.pool.Exec(context.Background(), `DELETE FROM system_settings WHERE id=1 AND updated_by=$1`, settingsOwner); err != nil {
							t.Errorf("remove continuation test settings: %v", err)
						}
					})
				}
				exec(`INSERT INTO scope_policies(bot_id,scope_type,scope_id,enabled,store_messages,allow_history_read,allow_memory_read,tool_permissions,max_chars_per_message) VALUES ($1,'group',$2,true,true,true,true,'{"qq.reply.current":true}',100)`, f.botID, target)
				tools := func() []plugins.RegisteredTool {
					return []plugins.RegisteredTool{{Descriptor: plugins.ToolDescriptor{Permission: "qq.reply.current"}}}
				}
				f.repo.SetGatePermissions(tools)
				repo.SetGatePermissions(tools)
				if scenario == "memory_changed" {
					var updated time.Time
					if err := f.pool.QueryRow(ctx, `INSERT INTO memories(memory_id,bot_id,visibility,owner_key,content,status) VALUES ($1,$2,'conversation',$3,'fixture','active') RETURNING updated_at`, memoryID, f.botID, f.conversationID).Scan(&updated); err != nil {
						t.Fatal(err)
					}
					reserveCtx = memory.WithRunReads(ctx)
					memory.TrackRead(reserveCtx, nil, memory.DetailRequest{MemoryID: memoryID}, updated, nil)
				}
			}
			action := ""
			payload := ""
			if scenario == "audio" {
				action = "qq.send_voice_current"
				payload = `{"type":"record"}`
			}
			record, err := f.repo.Reserve(reserveCtx, domain.ReserveInput{ClaimToken: f.claim, JobID: f.jobID, BotID: f.botID, ConversationID: f.conversationID, Text: "test", ToolCallID: "call-original", Action: action, Payload: payload})
			if err != nil {
				t.Fatal(err)
			}
			if scenario == "before_handoff" {
				exec(`UPDATE message_jobs SET lease_expires_at=clock_timestamp()-interval '1 second' WHERE id=$1`, f.jobID)
				if err := conv.RecoverStaleJobs(ctx); err != nil {
					t.Fatal(err)
				}
				var refunded, continuation bool
				if err := f.pool.QueryRow(ctx, `SELECT b.reserved_messages=0 AND b.reserved_chars=0 AND o.status='cancelled' AND o.error_code='lease_recovered',j.delivery_continuation FROM agent_outbound_messages o JOIN message_jobs j ON j.id=o.job_id JOIN agent_run_budgets b ON b.job_id=j.id WHERE o.id=$1`, record.ID).Scan(&refunded, &continuation); err != nil || !refunded || !continuation {
					t.Fatalf("refunded=%v continuation=%v err=%v", refunded, continuation, err)
				}
				exec(`UPDATE message_jobs SET next_run_at=clock_timestamp() WHERE id=$1`, f.jobID)
				var payload string
				if err := f.pool.QueryRow(ctx, `SELECT payload::text FROM agent_outbound_messages WHERE id=$1`, record.ID).Scan(&payload); err != nil {
					t.Fatal(err)
				}
				var sent int
				if worked, err := repo.ResumeNext(ctx, func(_ context.Context, restored domain.Record) (int64, error) {
					sent++
					if restored.ID != record.ID || restored.ToolCallID != record.ToolCallID || restored.Sequence != record.Sequence || restored.Payload != payload || restored.ClaimToken == record.ClaimToken {
						t.Fatalf("changed slot: %#v", restored)
					}
					return 9913, nil
				}); err != nil || !worked || sent != 1 {
					t.Fatalf("worked=%v sent=%d err=%v", worked, sent, err)
				}
				var receipt int64
				var reserved, slots int
				var status string
				if err := f.pool.QueryRow(ctx, `SELECT o.platform_message_id,b.reserved_messages,j.status,(SELECT count(*) FROM agent_outbound_messages WHERE job_id=j.id) FROM agent_outbound_messages o JOIN message_jobs j ON j.id=o.job_id JOIN agent_run_budgets b ON b.job_id=j.id WHERE o.id=$1`, record.ID).Scan(&receipt, &reserved, &status, &slots); err != nil || receipt != 9913 || reserved != 1 || slots != 1 || status != "done" {
					t.Fatalf("receipt=%d reserved=%d status=%s slots=%d err=%v", receipt, reserved, status, slots, err)
				}
				return
			}
			if scenario == "topic_expired" {
				exec(`UPDATE group_topics SET expires_at=clock_timestamp()-interval '1 second' WHERE id=$1`, f.jobID)
				var refusal *domain.GateError
				if err := f.repo.MarkSending(ctx, record.ID, f.claim); !errors.As(err, &refusal) || refusal.Reason != domain.GateStale {
					t.Fatalf("expected stale, got %v", err)
				}
				return
			}
			if scenario == "already_sending" {
				if err := f.repo.MarkSending(ctx, record.ID, f.claim); err != nil {
					t.Fatal(err)
				}
				record, err = f.repo.Reserve(ctx, domain.ReserveInput{ClaimToken: f.claim, JobID: f.jobID, BotID: f.botID, ConversationID: f.conversationID, Text: "second", ToolCallID: "call-second"})
				if err != nil {
					t.Fatal(err)
				}
			}
			next, err := conv.Ingest(ctx, conversation.IngestInput{BotID: f.botID, Kind: conversation.KindGroup, TargetID: target, PlatformMessageID: time.Now().UnixNano(), SenderID: f.botID + 3, Text: "update"})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _, _ = f.pool.Exec(context.Background(), `DELETE FROM message_jobs WHERE id=$1`, next.JobID) })
			if err := f.repo.MarkSending(ctx, record.ID, f.claim); !errors.Is(err, domain.ErrContinuation) {
				t.Fatalf("handoff: %v", err)
			}
			if err := f.repo.Cancel(ctx, record.ID, f.claim); !errors.Is(err, ErrLeaseLost) {
				t.Fatalf("old cancellation: %v", err)
			}
			if err := conv.FailJob(ctx, f.jobID, "late", 0, 1, f.claim); !errors.Is(err, conversationinfra.ErrLeaseLost) {
				t.Fatalf("old finalization: %v", err)
			}
			verify := func(state string, cost int) {
				t.Helper()
				var actual string
				var count int
				if err := f.pool.QueryRow(ctx, `SELECT o.status,b.reserved_messages FROM agent_outbound_messages o JOIN agent_run_budgets b USING(job_id) WHERE o.id=$1`, record.ID).Scan(&actual, &count); err != nil || actual != state || count != cost {
					t.Fatalf("state=%s cost=%d err=%v want %s %d", actual, count, err, state, cost)
				}
			}
			original := 1
			if scenario == "already_sending" {
				original = 2
			}
			verify("prepared", original)
			var calls int
			send := func(_ context.Context, got domain.Record) (int64, error) {
				calls++
				if got.ID != record.ID || got.JobID != record.JobID || got.Sequence != record.Sequence || got.ToolCallID != record.ToolCallID || got.ClaimToken == record.ClaimToken {
					t.Fatalf("changed slot: %+v", got)
				}
				switch scenario {
				case "unknown":
					return 0, context.DeadlineExceeded
				case "offline_before_write":
					return 0, domain.ErrDispatchNotWritten
				default:
					return 44, nil
				}
			}
			if scenario != "already_sending" {
				if worked, err := repo.ResumeNext(ctx, send); err != nil || !worked || calls != 0 {
					t.Fatalf("pending resume worked=%v calls=%d err=%v", worked, calls, err)
				}
			}
			wantState, wantReason, wantJob, wantCalls, wantCost := "sent", "", "done", 1, 1
			switch scenario {
			case "expired":
				exec(`UPDATE group_topic_routes SET deadline=clock_timestamp() WHERE id=$1`, next.MessageID)
			case "changed":
				exec(`UPDATE group_topics SET version=2 WHERE id=$1`, f.jobID)
				exec(`UPDATE group_topic_routes SET status='resolved' WHERE id=$1`, next.MessageID)
				wantState, wantReason, wantJob, wantCalls, wantCost = "cancelled", domain.GateStale, "failed", 0, 0
			default:
				exec(`UPDATE group_topic_routes SET status='unrelated' WHERE id=$1`, next.MessageID)
				switch scenario {
				case "revoked":
					exec(`UPDATE scope_policies SET tool_permissions='{"qq.reply.current":false}' WHERE bot_id=$1 AND scope_id=$2`, f.botID, target)
					wantState, wantReason, wantJob, wantCalls, wantCost = "cancelled", domain.GateRevoked, "failed", 0, 0
				case "budget":
					exec(`UPDATE scope_policies SET max_chars_per_message=1 WHERE bot_id=$1 AND scope_id=$2`, f.botID, target)
					wantState, wantReason, wantJob, wantCalls, wantCost = "cancelled", domain.GateBudget, "failed", 0, 0
				case "memory_changed":
					exec(`UPDATE memories SET updated_at=updated_at+interval '1 second' WHERE memory_id=$1`, memoryID)
					wantState, wantReason, wantJob, wantCalls, wantCost = "cancelled", domain.GateRevoked, "failed", 0, 0
				case "offline":
					wantState, wantReason, wantJob, wantCalls, wantCost = "cancelled", domain.GateOffline, "failed", 0, 0
				case "offline_before_write":
					wantState, wantReason, wantJob, wantCalls, wantCost = "cancelled", domain.GateOffline, "failed", 1, 0
				case "audio":
					wantState, wantReason, wantJob, wantCalls, wantCost = "cancelled", domain.GateAudio, "failed", 0, 0
				case "unknown":
					wantState, wantReason, wantJob = "unknown", "uncertain", "failed"
				case "already_sending":
					wantJob, wantCost = "failed", 2
				}
			}
			exec(`UPDATE message_jobs SET next_run_at=clock_timestamp() WHERE id=$1`, f.jobID)
			if worked, err := repo.ResumeNext(ctx, send, func(domain.Record) bool { return scenario != "offline" }); err != nil || !worked {
				t.Fatalf("resume worked=%v error=%v", worked, err)
			}
			if scenario == "already_sending" {
				if worked, err := repo.ResumeNext(ctx, send); err != nil || !worked {
					t.Fatalf("second resume worked=%v error=%v", worked, err)
				}
			}
			if calls != wantCalls {
				t.Fatalf("calls=%d want=%d", calls, wantCalls)
			}
			verify(wantState, wantCost)
			var job, state, reason, tool string
			var sequence int
			if err := f.pool.QueryRow(ctx, `SELECT j.status,o.resume_result->>'status',COALESCE(o.resume_result->>'error_code',''),o.resume_result->>'tool_call_id',(o.resume_result->>'sequence')::int FROM message_jobs j JOIN agent_outbound_messages o ON o.job_id=j.id WHERE o.id=$1`, record.ID).Scan(&job, &state, &reason, &tool, &sequence); err != nil {
				t.Fatal(err)
			}
			if job != wantJob || state != wantState || reason != wantReason || tool != record.ToolCallID || sequence != record.Sequence {
				t.Fatalf("result %s %s %s %s %d", job, state, reason, tool, sequence)
			}
			if worked, err := repo.ResumeNext(ctx, send); err != nil || worked || calls != wantCalls {
				t.Fatalf("duplicate worked=%v calls=%d err=%v", worked, calls, err)
			}
		})
	}
}
