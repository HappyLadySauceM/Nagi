package memory

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/HappyLadySauceM/Nagi/cmd/app/svc"
	memoryapp "github.com/HappyLadySauceM/Nagi/internal/memory/application"
	memoryinfra "github.com/HappyLadySauceM/Nagi/internal/memory/infrastructure"
	scopeapp "github.com/HappyLadySauceM/Nagi/internal/scope/application"
	scopedomain "github.com/HappyLadySauceM/Nagi/internal/scope/domain"
	scopeinfra "github.com/HappyLadySauceM/Nagi/internal/scope/infrastructure"
	"github.com/HappyLadySauceM/Nagi/pkg/db/dbtest"
)

// TestExtractionManagementHTTPSurface exercises the real database-backed
// management list and retry endpoints, not just the service methods.
// TestExtractionManagementHTTPSurface 验证真实数据库支撑的管理列表与重试 HTTP 接口，
// 而不只测试服务方法。
func TestExtractionManagementHTTPSurface(t *testing.T) {
	pool := dbtest.Open(t)
	ctx := context.Background()
	const botID = 995001
	const groupID = 995002
	if _, err := pool.Exec(ctx, `INSERT INTO bot_accounts (bot_id, nickname) VALUES ($1, 'extraction-http') ON CONFLICT DO NOTHING`, botID); err != nil {
		t.Fatalf("seed bot: %v", err)
	}
	conversationID := uuid.New()
	if err := pool.QueryRow(ctx, `INSERT INTO conversations (id, bot_id, kind, target_id, last_message_at) VALUES ($1,$2,'group',$3,now()) ON CONFLICT (bot_id, kind, target_id) DO UPDATE SET last_message_at=now() RETURNING id`, conversationID, botID, groupID).Scan(&conversationID); err != nil {
		t.Fatalf("seed conversation: %v", err)
	}
	var messageID int64
	if err := pool.QueryRow(ctx, `INSERT INTO messages (conversation_id, bot_id, direction, platform_message_id, sender_id, text_content, segments) VALUES ($1,$2,'incoming',9951001,995003,'tea','[]'::jsonb) RETURNING id`, conversationID, botID).Scan(&messageID); err != nil {
		t.Fatalf("seed message: %v", err)
	}
	scopeRepo, err := scopeinfra.NewRepo(pool)
	if err != nil {
		t.Fatalf("new scope repo: %v", err)
	}
	scopeService, err := scopeapp.NewService(scopeRepo)
	if err != nil {
		t.Fatalf("new scope service: %v", err)
	}
	policy := scopedomain.Default(scopedomain.ScopeGroup, groupID)
	policy.Enabled = true
	policy.StoreMessages = true
	policy.AllowMemoryWrite = true
	if err := scopeService.Upsert(ctx, botID, policy); err != nil {
		t.Fatalf("seed policy: %v", err)
	}
	eventID, authorized, err := scopeRepo.ResolveMemoryAuthorization(ctx, botID, scopedomain.ScopeGroup, groupID)
	if err != nil || !authorized {
		t.Fatalf("resolve authorization: event=%d authorized=%v err=%v", eventID, authorized, err)
	}
	if _, err := pool.Exec(ctx, `UPDATE messages SET memory_authorization_event_id=$1 WHERE id=$2`, eventID, messageID); err != nil {
		t.Fatalf("stamp message: %v", err)
	}
	jobID := uuid.New()
	if _, err := pool.Exec(ctx, `INSERT INTO memory_extraction_jobs (id, bot_id, conversation_id, scope_type, scope_id, message_id, authorization_event_id, status, attempts, round_attempts, manual_retries) VALUES ($1,$2,$3,'group',$4,$5,$6,'failed',4,2,0)`, jobID, botID, conversationID, groupID, messageID, eventID); err != nil {
		t.Fatalf("seed job: %v", err)
	}
	defer func() {
		_, _ = pool.Exec(ctx, `DELETE FROM memory_extraction_jobs WHERE bot_id=$1`, botID)
		_, _ = pool.Exec(ctx, `DELETE FROM messages WHERE bot_id=$1`, botID)
		_, _ = pool.Exec(ctx, `DELETE FROM conversations WHERE bot_id=$1`, botID)
		_, _ = pool.Exec(ctx, `DELETE FROM scope_policies WHERE bot_id=$1`, botID)
		_, _ = pool.Exec(ctx, `DELETE FROM scope_memory_authorization_events WHERE bot_id=$1`, botID)
	}()

	memoryRepo, err := memoryinfra.NewRepo(pool)
	if err != nil {
		t.Fatalf("new memory repo: %v", err)
	}
	memoryService, err := memoryapp.NewService(memoryRepo)
	if err != nil {
		t.Fatalf("new memory service: %v", err)
	}
	memoryService.SetMemoryAuthorizer(scopeService)
	handler := NewHandler(&svc.ServiceContext{Memory: memoryService})

	listReq := httptest.NewRequest(http.MethodGet, "/api/v1/memory-extraction/jobs?bot_id=995001", nil)
	listRec := httptest.NewRecorder()
	handler.ListExtractionJobs(listRec, listReq)
	if listRec.Code != http.StatusOK || !strings.Contains(listRec.Body.String(), `"items"`) {
		t.Fatalf("list status=%d body=%s", listRec.Code, listRec.Body.String())
	}

	retryReq := httptest.NewRequest(http.MethodPost, "/api/v1/memory-extraction/jobs/"+jobID.String()+"/retry", nil)
	retryReq.SetPathValue("id", jobID.String())
	retryRec := httptest.NewRecorder()
	handler.RetryExtractionJob(retryRec, retryReq)
	if retryRec.Code != http.StatusOK {
		t.Fatalf("retry status=%d body=%s", retryRec.Code, retryRec.Body.String())
	}
	var status string
	if err := pool.QueryRow(ctx, `SELECT status FROM memory_extraction_jobs WHERE id=$1`, jobID).Scan(&status); err != nil || status != "pending" {
		t.Fatalf("retried job status=%s err=%v", status, err)
	}
}
