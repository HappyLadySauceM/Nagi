// Tests for the PATCH status transition validation.
// PATCH 状态变更校验的测试。
package memory

import (
	"context"
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
		{name: "soft delete", status: "deleted", want: statusDelete, ok: true},
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

type handlerMemoryRepo struct{}

func (handlerMemoryRepo) Insert(context.Context, domain.Memory) (string, error) { return "", nil }
func (handlerMemoryRepo) Get(context.Context, string) (domain.Memory, bool, error) {
	return domain.Memory{}, false, nil
}
func (handlerMemoryRepo) Update(context.Context, domain.Memory) error { return nil }
func (handlerMemoryRepo) SoftDelete(context.Context, string) error    { return nil }
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
