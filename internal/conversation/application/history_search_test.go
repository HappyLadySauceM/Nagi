package application

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/HappyLadySauceM/Nagi/internal/conversation/contracts"
	"github.com/HappyLadySauceM/Nagi/internal/conversation/domain"
	scopedomain "github.com/HappyLadySauceM/Nagi/internal/scope/domain"
)

type searchFixture struct {
	ingress *domain.JobIngress
	search  func(context.Context, domain.HistorySearch) ([]contracts.HistoryHit, error)
}

func (s searchFixture) JobIngress(_ context.Context, bot, job int64) (*domain.JobIngress, error) {
	if bot != 1 || job != 40 {
		return nil, nil
	}
	return s.ingress, nil
}

func (s searchFixture) SearchHistory(ctx context.Context, query domain.HistorySearch) ([]contracts.HistoryHit, error) {
	return s.search(ctx, query)
}

type searchPolicy map[int64]bool

func (s searchPolicy) Resolve(_ context.Context, _ int64, _ scopedomain.ScopeType, id int64) (scopedomain.Policy, error) {
	return scopedomain.Policy{Enabled: true, StoreMessages: true, AllowHistoryRead: s[id]}, nil
}

func TestHistorySearchBindsJobAndRechecksGrants(t *testing.T) {
	now := time.Now().UTC()
	policy := searchPolicy{8: true, 9: true}
	source := searchFixture{ingress: &domain.JobIngress{Kind: domain.KindGroup, TargetID: 8,
		Message: domain.Message{ID: 20, ConversationID: "group-a", CreatedAt: now}},
	}
	calls, days := 0, 30
	action := func() {}
	source.search = func(ctx context.Context, query domain.HistorySearch) ([]contracts.HistoryHit, error) {
		calls++
		if query.BotID != 1 || query.ConversationID != "group-a" || query.BeforeID != 20 || !query.Before.Equal(now) || query.After.Before(now.AddDate(0, 0, -30)) || query.After.After(now.AddDate(0, 0, -29)) || query.SenderID != 77 {
			t.Fatalf("wrong search bounds: %+v", query)
		}
		if deadline, ok := ctx.Deadline(); !ok || time.Until(deadline) > historyQueryTimeout {
			t.Fatalf("missing query deadline: %v", deadline)
		}
		action()
		return []contracts.HistoryHit{{ID: 10, CreatedAt: now.Add(-48 * time.Hour), Text: "蓝棚"}}, nil
	}
	service := NewHistorySearch(source, policy, func() int { return days })
	correct := contracts.HistoryRequest{BotID: 1, TargetID: 8, JobID: 40, Kind: "group", ConversationID: "group-a", Query: "蓝棚", Limit: 5, SenderID: 77}
	wrong := correct
	wrong.TargetID = 9
	if r, err := service.Search(t.Context(), wrong); len(r) != 0 || err != nil {
		t.Fatalf("wrong scope: %v %v", r, err)
	}
	wrong = correct
	wrong.ConversationID = "group-b"
	if r, err := service.Search(t.Context(), wrong); len(r) != 0 || err != nil {
		t.Fatalf("wrong conversation: %v %v", r, err)
	}
	policy[8] = false
	if r, err := service.Search(t.Context(), correct); len(r) != 0 || err != nil || calls != 0 {
		t.Fatalf("pre-read denial: %v %v calls=%d", r, err, calls)
	}
	policy[8] = true
	action = func() { policy[8] = false }
	if r, err := service.Search(t.Context(), correct); len(r) != 0 || err != nil || calls != 1 {
		t.Fatalf("post-read denial: %v %v calls=%d", r, err, calls)
	}
	policy[8] = true
	action = func() { days = 1 }
	if r, err := service.Search(t.Context(), correct); len(r) != 0 || err != nil {
		t.Fatalf("shortened retention: %v %v", r, err)
	}
	days = 30
	action = func() {}
	if r, err := service.Search(t.Context(), correct); len(r) != 1 || err != nil {
		t.Fatalf("authorized hit: %v %v", r, err)
	}
	failed := NewHistorySearch(searchFixture{ingress: source.ingress, search: func(context.Context, domain.HistorySearch) ([]contracts.HistoryHit, error) {
		return nil, context.DeadlineExceeded
	}}, policy, func() int { return days })
	if r, err := failed.Search(t.Context(), correct); len(r) != 0 || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("query failure: %v %v", r, err)
	}
}
