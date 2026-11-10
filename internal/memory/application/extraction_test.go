package application

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/HappyLadySauceM/Nagi/internal/memory/domain"
	scopecontracts "github.com/HappyLadySauceM/Nagi/internal/scope/contracts"
	scopedomain "github.com/HappyLadySauceM/Nagi/internal/scope/domain"
)

type fakeExtractionJobRepo struct {
	*fakeMemoryRepo
	jobs        map[string]domain.ExtractionJob
	retryCalled bool
}

func newFakeExtractionJobRepo() *fakeExtractionJobRepo {
	return &fakeExtractionJobRepo{fakeMemoryRepo: newFakeMemoryRepo(), jobs: make(map[string]domain.ExtractionJob)}
}

func (f *fakeExtractionJobRepo) ListExtractionJobs(context.Context, domain.ExtractionJobFilter) ([]domain.ExtractionJob, int64, error) {
	out := make([]domain.ExtractionJob, 0, len(f.jobs))
	for _, job := range f.jobs {
		out = append(out, job)
	}
	return out, int64(len(out)), nil
}

func (f *fakeExtractionJobRepo) GetExtractionJob(_ context.Context, id string) (domain.ExtractionJob, bool, error) {
	job, ok := f.jobs[id]
	return job, ok, nil
}

func (f *fakeExtractionJobRepo) RetryExtractionJob(_ context.Context, id string, _ int) (bool, error) {
	f.retryCalled = true
	job, ok := f.jobs[id]
	if !ok || job.Status != "failed" || job.ManualRetries >= DefaultManualRetryLimit {
		return false, nil
	}
	job.Status = "pending"
	job.ManualRetries++
	f.jobs[id] = job
	return true, nil
}

type fakeMemoryAuthorizer struct {
	auth scopecontracts.MemoryAuthorization
	err  error
}

func (f fakeMemoryAuthorizer) ResolveMemoryAuthorization(context.Context, int64, scopedomain.ScopeType, int64) (scopecontracts.MemoryAuthorization, error) {
	return f.auth, f.err
}

func TestRetryExtractionJobChecksSourceAndAuthorization(t *testing.T) {
	repo := newFakeExtractionJobRepo()
	svc, err := NewService(repo)
	if err != nil {
		t.Fatalf("new service: %v", err)
	}
	jobID := uuid.NewString()
	repo.jobs[jobID] = domain.ExtractionJob{ID: jobID, BotID: 1, ScopeType: "group", ScopeID: 700,
		MessageID: 12, MessageAuthorizationEventID: 44, AuthorizationEventID: 44, Status: "failed", SourceAvailable: true}
	svc.SetMemoryAuthorizer(fakeMemoryAuthorizer{auth: scopecontracts.MemoryAuthorization{EventID: 44, Authorized: true}})
	if err := svc.RetryExtractionJob(context.Background(), jobID); err != nil {
		t.Fatalf("retry valid job: %v", err)
	}
	if !repo.retryCalled || repo.jobs[jobID].Status != "pending" || repo.jobs[jobID].ManualRetries != 1 {
		t.Fatalf("retry result = %+v called=%v", repo.jobs[jobID], repo.retryCalled)
	}

	t.Run("authorization changed", func(t *testing.T) {
		repo := newFakeExtractionJobRepo()
		svc, _ := NewService(repo)
		jobID := uuid.NewString()
		repo.jobs[jobID] = domain.ExtractionJob{ID: jobID, BotID: 1, ScopeType: "group", ScopeID: 700,
			MessageID: 12, MessageAuthorizationEventID: 44, AuthorizationEventID: 44, Status: "failed", SourceAvailable: true}
		svc.SetMemoryAuthorizer(fakeMemoryAuthorizer{auth: scopecontracts.MemoryAuthorization{EventID: 45, Authorized: true}})
		if err := svc.RetryExtractionJob(context.Background(), jobID); !errors.Is(err, ErrExtractionAuthorizationChanged) {
			t.Fatalf("authorization mismatch error = %v", err)
		}
	})

	t.Run("source missing", func(t *testing.T) {
		repo := newFakeExtractionJobRepo()
		svc, _ := NewService(repo)
		jobID := uuid.NewString()
		repo.jobs[jobID] = domain.ExtractionJob{ID: jobID, BotID: 1, ScopeType: "group", ScopeID: 700,
			AuthorizationEventID: 44, Status: "failed", SourceAvailable: false}
		svc.SetMemoryAuthorizer(fakeMemoryAuthorizer{auth: scopecontracts.MemoryAuthorization{EventID: 44, Authorized: true}})
		if err := svc.RetryExtractionJob(context.Background(), jobID); !errors.Is(err, ErrExtractionSourceMissing) {
			t.Fatalf("source missing error = %v", err)
		}
	})

	t.Run("already done", func(t *testing.T) {
		repo := newFakeExtractionJobRepo()
		svc, _ := NewService(repo)
		jobID := uuid.NewString()
		repo.jobs[jobID] = domain.ExtractionJob{ID: jobID, Status: "done", SourceAvailable: true}
		if err := svc.RetryExtractionJob(context.Background(), jobID); !errors.Is(err, ErrExtractionJobState) {
			t.Fatalf("done job error = %v", err)
		}
	})
}
