// Tests for the memory workflow: dedupe, approval, visibility-filtered search.
// 记忆流程测试：去重、审核、可见性过滤检索。
package application

import (
	"context"
	"testing"

	"github.com/HappyLadySauceM/Nagi/internal/memory/domain"
)

// fakeMemoryRepo is an in-memory repository.
// fakeMemoryRepo 是内存版仓库。
type fakeMemoryRepo struct {
	items map[string]domain.Memory
	seq   int
}

func newFakeMemoryRepo() *fakeMemoryRepo {
	return &fakeMemoryRepo{items: make(map[string]domain.Memory)}
}

func (f *fakeMemoryRepo) Insert(_ context.Context, m domain.Memory) (string, error) {
	f.seq++
	if m.ID == "" {
		m.ID = string(rune('a' + f.seq))
	}
	f.items[m.ID] = m
	return m.ID, nil
}

func (f *fakeMemoryRepo) Get(_ context.Context, id string) (domain.Memory, bool, error) {
	m, ok := f.items[id]
	return m, ok, nil
}

func (f *fakeMemoryRepo) Update(_ context.Context, m domain.Memory) error {
	f.items[m.ID] = m
	return nil
}

func (f *fakeMemoryRepo) SoftDelete(_ context.Context, id string) error {
	m := f.items[id]
	m.Status = domain.StatusDeleted
	f.items[id] = m
	return nil
}

func (f *fakeMemoryRepo) List(_ context.Context, _ domain.ListFilter) ([]domain.Memory, int64, error) {
	return nil, 0, nil
}

func (f *fakeMemoryRepo) FindSame(_ context.Context, botID int64, v domain.Visibility, ownerKey, content string) (domain.Memory, bool, error) {
	for _, m := range f.items {
		if m.BotID == botID && m.Visibility == v && m.OwnerKey == ownerKey && m.Content == content &&
			(m.Status == domain.StatusCandidate || m.Status == domain.StatusActive) {
			return m, true, nil
		}
	}
	return domain.Memory{}, false, nil
}

func (f *fakeMemoryRepo) SearchVisible(_ context.Context, in domain.SearchRequest) ([]domain.Memory, error) {
	out := []domain.Memory{}
	for _, m := range f.items {
		if m.Status != domain.StatusActive {
			continue
		}
		if m.Visibility == domain.VisibilityBotShared ||
			(in.Kind == "group" && m.Visibility == domain.VisibilityGroup && m.OwnerKey == itoaMem(in.TargetID)) ||
			(in.Kind == "private" && m.Visibility == domain.VisibilityPrivateUser && m.OwnerKey == itoaMem(in.TargetID)) ||
			(in.ConversationID != "" && m.Visibility == domain.VisibilityConversation && m.OwnerKey == in.ConversationID) {
			out = append(out, m)
		}
	}
	return out, nil
}

func itoaMem(v int64) string {
	if v == 0 {
		return "0"
	}
	var b []byte
	for v > 0 {
		b = append([]byte{byte('0' + v%10)}, b...)
		v /= 10
	}
	return string(b)
}

// TestProposeDedupesExactContent verifies duplicates return the existing id.
// TestProposeDedupesExactContent 验证重复内容返回既有 ID。
func TestProposeDedupesExactContent(t *testing.T) {
	svc, _ := NewService(newFakeMemoryRepo())
	ctx := context.Background()
	in := CandidateInput{BotID: 1, Visibility: domain.VisibilityGroup, OwnerKey: "700",
		Content: "群友们喜欢在晚上聊天", Confidence: 0.8}
	id1, dup1, err := svc.Propose(ctx, in)
	if err != nil || dup1 {
		t.Fatalf("first propose: id=%s dup=%v err=%v", id1, dup1, err)
	}
	id2, dup2, err := svc.Propose(ctx, in)
	if err != nil || !dup2 || id2 != id1 {
		t.Fatalf("second propose: id=%s dup=%v err=%v", id2, dup2, err)
	}
}

// TestProposeRejectsBotSharedWithOwner verifies the visibility invariants.
// TestProposeRejectsBotSharedWithOwner 验证可见性不变量。
func TestProposeRejectsBotSharedWithOwner(t *testing.T) {
	svc, _ := NewService(newFakeMemoryRepo())
	_, _, err := svc.Propose(context.Background(), CandidateInput{
		BotID: 1, Visibility: domain.VisibilityBotShared, OwnerKey: "700", Content: "x",
	})
	if err == nil {
		t.Fatal("bot_shared with owner key must be rejected")
	}
	_, _, err = svc.Propose(context.Background(), CandidateInput{
		BotID: 1, Visibility: domain.VisibilityGroup, Content: "no owner",
	})
	if err == nil {
		t.Fatal("group memory without owner key must be rejected")
	}
}

// TestApproveActivatesAndSearchVisible verifies the candidate → active flow and
// that private memories never leak into group searches.
// TestApproveActivatesAndSearchVisible 验证候选到激活的流程，以及私聊记忆
// 绝不泄漏到群聊检索。
func TestApproveActivatesAndSearchVisible(t *testing.T) {
	repo := newFakeMemoryRepo()
	svc, _ := NewService(repo)
	ctx := context.Background()

	privateID, _, err := svc.Propose(ctx, CandidateInput{BotID: 1,
		Visibility: domain.VisibilityPrivateUser, OwnerKey: "200", Content: "用户200喜欢简洁回复"})
	if err != nil {
		t.Fatalf("propose private: %v", err)
	}
	sharedID, _, err := svc.Propose(ctx, CandidateInput{BotID: 1,
		Visibility: domain.VisibilityBotShared, Content: "机器人名叫小雪"})
	if err != nil {
		t.Fatalf("propose shared: %v", err)
	}

	// Candidates are invisible before approval.
	// 审核前候选不可见。
	got, err := svc.SearchVisible(ctx, domain.SearchRequest{BotID: 1, Kind: "group", TargetID: 700, Limit: 5})
	if err != nil || len(got) != 0 {
		t.Fatalf("candidates must be invisible: got=%d err=%v", len(got), err)
	}

	if err := svc.Approve(ctx, privateID, "admin"); err != nil {
		t.Fatalf("approve private: %v", err)
	}
	if err := svc.Approve(ctx, sharedID, "admin"); err != nil {
		t.Fatalf("approve shared: %v", err)
	}

	groupView, err := svc.SearchVisible(ctx, domain.SearchRequest{BotID: 1, Kind: "group", TargetID: 700, Limit: 5})
	if err != nil || len(groupView) != 1 || groupView[0].Visibility != domain.VisibilityBotShared {
		t.Fatalf("group view = %+v err=%v", groupView, err)
	}
	privateView, err := svc.SearchVisible(ctx, domain.SearchRequest{BotID: 1, Kind: "private", TargetID: 200, Limit: 5})
	if err != nil || len(privateView) != 2 {
		t.Fatalf("private view = %+v err=%v", privateView, err)
	}
}

// TestDeleteRemovesFromSearch verifies deletion takes effect in retrieval.
// TestDeleteRemovesFromSearch 验证删除后检索不再返回。
func TestDeleteRemovesFromSearch(t *testing.T) {
	repo := newFakeMemoryRepo()
	svc, _ := NewService(repo)
	ctx := context.Background()
	id, _, err := svc.Propose(ctx, CandidateInput{BotID: 1,
		Visibility: domain.VisibilityBotShared, Content: "待删除"})
	if err != nil {
		t.Fatalf("propose: %v", err)
	}
	if err := svc.Approve(ctx, id, "admin"); err != nil {
		t.Fatalf("approve: %v", err)
	}
	if err := svc.Delete(ctx, id); err != nil {
		t.Fatalf("delete: %v", err)
	}
	got, err := svc.SearchVisible(ctx, domain.SearchRequest{BotID: 1, Kind: "group", TargetID: 700, Limit: 5})
	if err != nil || len(got) != 0 {
		t.Fatalf("deleted memory still visible: got=%d err=%v", len(got), err)
	}
}
