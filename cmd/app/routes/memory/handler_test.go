// Tests for the PATCH status transition validation.
// PATCH 状态变更校验的测试。
package memory

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/HappyLadySauceM/Nagi/cmd/app/svc"
	memoryapp "github.com/HappyLadySauceM/Nagi/internal/memory/application"
	"github.com/HappyLadySauceM/Nagi/internal/memory/domain"
)

// TestParseStatusAction pins the accepted transitions. candidate and superseded
// are valid memory states but cannot be reached through the management API, so
// they must be rejected instead of silently ignored.
// TestParseStatusAction 固定可接受的状态变更：candidate 与 superseded 虽然合法，
// 但管理接口无法抵达，必须拒绝而不是静默忽略。
func TestParseStatusAction(t *testing.T) {
	cases := []struct {
		name   string
		status string
		want   statusAction
		ok     bool
	}{
		{name: "keep current status", status: "", want: statusNone, ok: true},
		{name: "approve", status: "active", want: statusApprove, ok: true},
		{name: "delete", status: "deleted", want: statusDelete, ok: true},
		{name: "candidate is not reachable", status: "candidate", want: statusNone, ok: false},
		{name: "superseded is not reachable", status: "superseded", want: statusNone, ok: false},
		{name: "unknown status", status: "archived", want: statusNone, ok: false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := parseStatusAction(tc.status)
			if ok != tc.ok || got != tc.want {
				t.Fatalf("parseStatusAction(%q) = (%d,%v), want (%d,%v)", tc.status, got, ok, tc.want, tc.ok)
			}
		})
	}
}

type handlerMemoryRepo struct{ deleteFound bool }

func (handlerMemoryRepo) Insert(context.Context, domain.Memory) (string, error) { return "", nil }
func (handlerMemoryRepo) Get(context.Context, string) (domain.Memory, bool, error) {
	return domain.Memory{}, false, nil
}
func (handlerMemoryRepo) Update(context.Context, domain.Memory) error    { return nil }
func (f handlerMemoryRepo) Delete(context.Context, string) (bool, error) { return f.deleteFound, nil }
func (handlerMemoryRepo) List(context.Context, domain.ListFilter) ([]domain.Memory, int64, error) {
	return nil, 0, nil
}
func (handlerMemoryRepo) FindSame(context.Context, int64, domain.Visibility, string, string) (domain.Memory, bool, error) {
	return domain.Memory{}, false, nil
}
func (handlerMemoryRepo) SearchVisible(context.Context, domain.SearchRequest) ([]domain.Memory, error) {
	return nil, nil
}
func (handlerMemoryRepo) ListExtractionJobs(context.Context, domain.ExtractionJobFilter) ([]domain.ExtractionJob, int64, error) {
	return []domain.ExtractionJob{}, 0, nil
}
func (handlerMemoryRepo) GetExtractionJob(context.Context, string) (domain.ExtractionJob, bool, error) {
	return domain.ExtractionJob{}, false, nil
}
func (handlerMemoryRepo) RetryExtractionJob(context.Context, string, int) (bool, error) {
	return false, nil
}

func TestGetRejectsMalformedMemoryID(t *testing.T) {
	service, err := memoryapp.NewService(handlerMemoryRepo{})
	if err != nil {
		t.Fatalf("new service: %v", err)
	}
	h := NewHandler(&svc.ServiceContext{Memory: service})
	req := httptest.NewRequest(http.MethodGet, "/api/v1/memories/not-a-uuid?bot_id=1&kind=group&scope_id=700", nil)
	req.SetPathValue("id", "not-a-uuid")
	rec := httptest.NewRecorder()
	h.Get(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "invalid memory id") {
		t.Fatalf("body = %s", rec.Body.String())
	}
}

func TestDeleteRejectsMalformedMemoryID(t *testing.T) {
	service, err := memoryapp.NewService(handlerMemoryRepo{})
	if err != nil {
		t.Fatalf("new service: %v", err)
	}
	h := NewHandler(&svc.ServiceContext{Memory: service})
	req := httptest.NewRequest(http.MethodDelete, "/api/v1/memories/not-a-uuid", nil)
	req.SetPathValue("id", "not-a-uuid")
	rec := httptest.NewRecorder()
	h.Delete(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", rec.Code, rec.Body.String())
	}
}

// TestDeleteReturnsNotFound verifies a valid but absent memory id maps to 404.
// TestDeleteReturnsNotFound 验证合法但不存在的记忆 ID 返回 404。
func TestDeleteReturnsNotFound(t *testing.T) {
	service, err := memoryapp.NewService(handlerMemoryRepo{})
	if err != nil {
		t.Fatalf("new service: %v", err)
	}
	h := NewHandler(&svc.ServiceContext{Memory: service})
	req := httptest.NewRequest(http.MethodDelete, "/api/v1/memories/11111111-1111-4111-8111-111111111111", nil)
	req.SetPathValue("id", "11111111-1111-4111-8111-111111111111")
	rec := httptest.NewRecorder()
	h.Delete(rec, req)
	if rec.Code != http.StatusNotFound || !strings.Contains(rec.Body.String(), "memory not found") {
		t.Fatalf("status = %d body=%s, want 404 memory not found", rec.Code, rec.Body.String())
	}
}

// TestDeleteReturnsSuccess verifies a successful physical delete maps to 200.
// TestDeleteReturnsSuccess 验证成功的物理删除返回 200。
func TestDeleteReturnsSuccess(t *testing.T) {
	service, err := memoryapp.NewService(handlerMemoryRepo{deleteFound: true})
	if err != nil {
		t.Fatalf("new service: %v", err)
	}
	h := NewHandler(&svc.ServiceContext{Memory: service})
	req := httptest.NewRequest(http.MethodDelete, "/api/v1/memories/11111111-1111-4111-8111-111111111111", nil)
	req.SetPathValue("id", "11111111-1111-4111-8111-111111111111")
	rec := httptest.NewRecorder()
	h.Delete(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "deleted") {
		t.Fatalf("status = %d body=%s, want 200 deleted", rec.Code, rec.Body.String())
	}
}

func TestListExtractionJobsRequiresBotID(t *testing.T) {
	service, err := memoryapp.NewService(handlerMemoryRepo{})
	if err != nil {
		t.Fatalf("new service: %v", err)
	}
	h := NewHandler(&svc.ServiceContext{Memory: service})
	req := httptest.NewRequest(http.MethodGet, "/api/v1/memory-extraction/jobs", nil)
	rec := httptest.NewRecorder()
	h.ListExtractionJobs(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", rec.Code, rec.Body.String())
	}
}

func TestListExtractionJobsReturnsSafeEnvelope(t *testing.T) {
	service, err := memoryapp.NewService(handlerMemoryRepo{})
	if err != nil {
		t.Fatalf("new service: %v", err)
	}
	h := NewHandler(&svc.ServiceContext{Memory: service})
	req := httptest.NewRequest(http.MethodGet, "/api/v1/memory-extraction/jobs?bot_id=1", nil)
	rec := httptest.NewRecorder()
	h.ListExtractionJobs(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"items"`) {
		t.Fatalf("body = %s", rec.Body.String())
	}
}

func TestRetryExtractionJobRejectsMalformedID(t *testing.T) {
	service, err := memoryapp.NewService(handlerMemoryRepo{})
	if err != nil {
		t.Fatalf("new service: %v", err)
	}
	h := NewHandler(&svc.ServiceContext{Memory: service})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/memory-extraction/jobs/not-a-uuid/retry", nil)
	req.SetPathValue("id", "not-a-uuid")
	rec := httptest.NewRecorder()
	h.RetryExtractionJob(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", rec.Code, rec.Body.String())
	}
}

// TestRecallPreviewExampleReturnsAudit verifies the read-only example preview
// exposes hit keywords and selection reasons without touching the repository.
// TestRecallPreviewExampleReturnsAudit 验证只读示例预览返回命中词与选入原因，
// 且不访问仓库。
func TestRecallPreviewExampleReturnsAudit(t *testing.T) {
	service, err := memoryapp.NewService(handlerMemoryRepo{})
	if err != nil {
		t.Fatalf("new service: %v", err)
	}
	h := NewHandler(&svc.ServiceContext{Memory: service})
	rec := httptest.NewRecorder()
	h.RecallPreview(rec, httptest.NewRequest(http.MethodPost, "/api/v1/memories/recall-preview", strings.NewReader(`{"preview_mode":"example","query":"咖啡","subject_type":"unknown"}`)))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	var envelope struct {
		Data struct {
			Items     []map[string]any
			NoKeyword bool
			Limit     int
			Reason    string
		}
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(envelope.Data.Items) != 1 || envelope.Data.NoKeyword {
		t.Fatalf("preview = %+v", envelope.Data)
	}
	item := envelope.Data.Items[0]
	if item["SubjectMatch"] != "none" || item["SelectionReason"] != "unknown_subject_keyword" {
		t.Fatalf("item audit = %#v", item)
	}
	keywords, _ := item["HitKeywords"].([]any)
	if len(keywords) != 1 || keywords[0] != "咖啡" {
		t.Fatalf("hit keywords = %#v", item["HitKeywords"])
	}

	rec = httptest.NewRecorder()
	h.RecallPreview(rec, httptest.NewRequest(http.MethodPost, "/api/v1/memories/recall-preview", strings.NewReader(`{"preview_mode":"example","query":"hi"}`)))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"NoKeyword":true`) {
		t.Fatalf("hi preview status = %d body=%s", rec.Code, rec.Body.String())
	}
}

type handlerReplacementRepo struct {
	handlerMemoryRepo
	supersedeResult domain.SupersedeResult
	supersedeErr    error
	management      domain.Memory
	replacements    []domain.ReplacementView
	managementFound bool
}

func (f handlerReplacementRepo) Supersede(context.Context, domain.SupersedeInput) (domain.SupersedeResult, error) {
	return f.supersedeResult, f.supersedeErr
}

func (f handlerReplacementRepo) Management(context.Context, string, int64) (domain.Memory, []domain.ReplacementView, bool, error) {
	return f.management, f.replacements, f.managementFound, nil
}

// TestSupersedeRequiresIdempotencyKey verifies the stable replacement_invalid
// mapping before the repository is reached.
// TestSupersedeRequiresIdempotencyKey 验证请求校验在到达仓储前返回稳定的
// replacement_invalid。
func TestSupersedeRequiresIdempotencyKey(t *testing.T) {
	service, err := memoryapp.NewService(handlerMemoryRepo{})
	if err != nil {
		t.Fatalf("new service: %v", err)
	}
	h := NewHandler(&svc.ServiceContext{Memory: service})
	body := `{"old_memory_id":"11111111-1111-4111-8111-111111111111"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/memories/22222222-2222-4222-8222-222222222222/supersede", strings.NewReader(body))
	req.SetPathValue("candidate_id", "22222222-2222-4222-8222-222222222222")
	rec := httptest.NewRecorder()
	h.Supersede(rec, req)
	if rec.Code != http.StatusUnprocessableEntity || !strings.Contains(rec.Body.String(), "replacement_invalid") {
		t.Fatalf("status = %d body=%s, want 422 replacement_invalid", rec.Code, rec.Body.String())
	}
}

// TestSupersedeMapsSubjectAmbiguous verifies repository conflicts map to 409.
// TestSupersedeMapsSubjectAmbiguous 验证仓储主体歧义映射为 409。
func TestSupersedeMapsSubjectAmbiguous(t *testing.T) {
	service, err := memoryapp.NewService(handlerReplacementRepo{supersedeErr: memoryapp.ErrSubjectAmbiguous})
	if err != nil {
		t.Fatalf("new service: %v", err)
	}
	h := NewHandler(&svc.ServiceContext{Memory: service})
	body := `{"old_memory_id":"11111111-1111-4111-8111-111111111111","idempotency_key":"k1"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/memories/22222222-2222-4222-8222-222222222222/supersede", strings.NewReader(body))
	req.SetPathValue("candidate_id", "22222222-2222-4222-8222-222222222222")
	rec := httptest.NewRecorder()
	h.Supersede(rec, req)
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "subject_ambiguous") {
		t.Fatalf("status = %d body=%s, want 409 subject_ambiguous", rec.Code, rec.Body.String())
	}
}

// TestManagementRequiresBotID verifies the administrator detail endpoint
// requires an explicit bot scope.
// TestManagementRequiresBotID 验证管理员详情接口必须显式携带 bot 作用域。
func TestManagementRequiresBotID(t *testing.T) {
	service, err := memoryapp.NewService(handlerReplacementRepo{})
	if err != nil {
		t.Fatalf("new service: %v", err)
	}
	h := NewHandler(&svc.ServiceContext{Memory: service})
	req := httptest.NewRequest(http.MethodGet, "/api/v1/memories/11111111-1111-4111-8111-111111111111/management", nil)
	req.SetPathValue("id", "11111111-1111-4111-8111-111111111111")
	rec := httptest.NewRecorder()
	h.Management(rec, req)
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "bot_id is required") {
		t.Fatalf("status = %d body=%s, want 400", rec.Code, rec.Body.String())
	}
}

type patchMemoryRepo struct {
	handlerMemoryRepo
	memory       domain.Memory
	updateCalled bool
}

func (f *patchMemoryRepo) Get(context.Context, string) (domain.Memory, bool, error) {
	return f.memory, true, nil
}

func (f *patchMemoryRepo) Update(context.Context, domain.Memory) error {
	f.updateCalled = true
	return nil
}

// TestPatchReviseValidationReturnsBadRequest verifies PATCH keeps the
// baseline 400 for invalid edited fields instead of mapping them to 500.
// TestPatchReviseValidationReturnsBadRequest 验证 PATCH 对非法编辑字段保持基线
// 400，而不是映射为 500。
func TestPatchReviseValidationReturnsBadRequest(t *testing.T) {
	repo := &patchMemoryRepo{memory: domain.Memory{
		ID: "11111111-1111-4111-8111-111111111111", BotID: 1,
		Visibility: domain.VisibilityGroup, OwnerKey: "700", Content: "old",
		Status: domain.StatusCandidate, Origin: domain.OriginManual,
	}}
	service, err := memoryapp.NewService(repo)
	if err != nil {
		t.Fatalf("new service: %v", err)
	}
	h := NewHandler(&svc.ServiceContext{Memory: service})
	req := httptest.NewRequest(http.MethodPatch, "/api/v1/memories/11111111-1111-4111-8111-111111111111", strings.NewReader(`{"visibility":"invalid"}`))
	req.SetPathValue("id", "11111111-1111-4111-8111-111111111111")
	rec := httptest.NewRecorder()
	h.Patch(rec, req)
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "revise memory failed") {
		t.Fatalf("status = %d body=%s, want 400 revise memory failed", rec.Code, rec.Body.String())
	}
	if repo.updateCalled {
		t.Fatal("invalid revise reached repository update")
	}
}

// TestPatchApproveRejectsNonCandidateBeforeEdit verifies an edit bundled with
// status=active cannot be committed before the status conflict is reported.
// TestPatchApproveRejectsNonCandidateBeforeEdit 验证与 status=active 同请求的编辑
// 不会在状态冲突返回前先落库。
func TestPatchApproveRejectsNonCandidateBeforeEdit(t *testing.T) {
	repo := &patchMemoryRepo{memory: domain.Memory{
		ID: "22222222-2222-4222-8222-222222222222", BotID: 1,
		Visibility: domain.VisibilityGroup, OwnerKey: "700", Content: "old",
		Status: domain.StatusActive, Origin: domain.OriginManual,
	}}
	service, err := memoryapp.NewService(repo)
	if err != nil {
		t.Fatalf("new service: %v", err)
	}
	h := NewHandler(&svc.ServiceContext{Memory: service})
	req := httptest.NewRequest(http.MethodPatch, "/api/v1/memories/22222222-2222-4222-8222-222222222222", strings.NewReader(`{"content":"new","status":"active"}`))
	req.SetPathValue("id", "22222222-2222-4222-8222-222222222222")
	rec := httptest.NewRecorder()
	h.Patch(rec, req)
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "memory_status_conflict") {
		t.Fatalf("status = %d body=%s, want 409 memory_status_conflict", rec.Code, rec.Body.String())
	}
	if repo.updateCalled {
		t.Fatal("edit before approve conflict reached repository update")
	}
}
