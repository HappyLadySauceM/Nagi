// Tests for the memory workflow: dedupe, approval, visibility-filtered search.
// 记忆流程测试：去重、审核、可见性过滤检索。
package application

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/HappyLadySauceM/Nagi/internal/memory/contracts"
	"github.com/HappyLadySauceM/Nagi/internal/memory/domain"
)

// fakeMemoryRepo is an in-memory repository.
// fakeMemoryRepo 是内存版仓库。
type fakeMemoryRepo struct {
	items      map[string]domain.Memory
	seq        int
	lastSearch domain.SearchRequest
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

func (f *fakeMemoryRepo) FindByDedupeKey(_ context.Context, key string) (domain.Memory, bool, error) {
	for _, m := range f.items {
		if m.DedupeKey == key && (m.Status == domain.StatusCandidate || m.Status == domain.StatusActive) {
			return m, true, nil
		}
	}
	return domain.Memory{}, false, nil
}

func (f *fakeMemoryRepo) SearchVisible(_ context.Context, in domain.SearchRequest) ([]domain.Memory, error) {
	f.lastSearch = in
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

// TestRecallMapsVisibleMemories verifies the reply-facing read model keeps the
// identity and update timestamp needed to trace a hit, and that the requested
// limit/query reach the repository unchanged.
// TestRecallMapsVisibleMemories 验证回复侧读模型保留定位命中所需的标识与更新时间，
// 且请求的 limit/query 原样传给仓库。
func TestRecallMapsVisibleMemories(t *testing.T) {
	repo := newFakeMemoryRepo()
	repo.items["m1"] = domain.Memory{
		ID: "m1", BotID: 1, Visibility: domain.VisibilityBotShared,
		Content: "群友喜欢晚上聊天", Status: domain.StatusActive,
		UpdatedAt: time.Date(2026, 11, 10, 8, 0, 0, 0, time.UTC),
	}
	svc, _ := NewService(repo)
	got, err := svc.Recall(context.Background(), contracts.RecallRequest{
		BotID: 1, Kind: "group", TargetID: 700, ConversationID: "conv-1", Query: "晚上聊天", Limit: 2,
	})
	if err != nil {
		t.Fatalf("recall: %v", err)
	}
	if len(got) != 1 || got[0].ID != "m1" || got[0].Visibility != string(domain.VisibilityBotShared) || got[0].UpdatedAt.IsZero() {
		t.Fatalf("recall = %+v", got)
	}
	if repo.lastSearch.Query != "晚上聊天" || repo.lastSearch.Limit != 2 || repo.lastSearch.ConversationID != "conv-1" {
		t.Fatalf("search request = %+v", repo.lastSearch)
	}
}

// TestRecallRejectsInvalidScope verifies malformed scope inputs never reach the
// repository.
// TestRecallRejectsInvalidScope 验证畸形作用域不会到达仓库。
func TestRecallRejectsInvalidScope(t *testing.T) {
	svc, _ := NewService(newFakeMemoryRepo())
	cases := []contracts.RecallRequest{
		{BotID: 0, Kind: "group", TargetID: 1},
		{BotID: 1, Kind: "channel", TargetID: 1},
		{BotID: 1, Kind: "group", TargetID: 0},
	}
	for _, in := range cases {
		if _, err := svc.Recall(context.Background(), in); err == nil {
			t.Fatalf("expected rejection for %+v", in)
		}
	}
}

// TestReviseChangesNextRecall verifies an administrator edit is visible to the
// next retrieval without restarting or rebuilding any cache.
// TestReviseChangesNextRecall 验证管理员修订在下一次检索即生效，无需重启或重建缓存。
func TestReviseChangesNextRecall(t *testing.T) {
	repo := newFakeMemoryRepo()
	svc, _ := NewService(repo)
	ctx := context.Background()
	id, _, err := svc.Propose(ctx, CandidateInput{
		BotID: 1, Visibility: domain.VisibilityGroup, OwnerKey: "700", Content: "旧内容",
	})
	if err != nil {
		t.Fatalf("propose: %v", err)
	}
	if err := svc.Approve(ctx, id, "admin"); err != nil {
		t.Fatalf("approve: %v", err)
	}
	if err := svc.Revise(ctx, id, "新内容", domain.VisibilityGroup, nil); err != nil {
		t.Fatalf("revise: %v", err)
	}
	got, err := svc.Recall(ctx, contracts.RecallRequest{BotID: 1, Kind: "group", TargetID: 700, Limit: 3})
	if err != nil || len(got) != 1 || got[0].Content != "新内容" {
		t.Fatalf("recall after revise = %+v err=%v", got, err)
	}
}

// TestPrivateMemoriesStayPerUser verifies two private users never share the
// same owner key and therefore never merge into one person.
// TestPrivateMemoriesStayPerUser 验证两个私聊用户不共享 owner key，不会合并成同一个人。
func TestPrivateMemoriesStayPerUser(t *testing.T) {
	repo := newFakeMemoryRepo()
	svc, _ := NewService(repo)
	ctx := context.Background()
	for _, owner := range []string{"200", "201"} {
		id, _, err := svc.Propose(ctx, CandidateInput{
			BotID: 1, Visibility: domain.VisibilityPrivateUser, OwnerKey: owner, Content: "用户" + owner + "的偏好",
		})
		if err != nil {
			t.Fatalf("propose %s: %v", owner, err)
		}
		if err := svc.Approve(ctx, id, "admin"); err != nil {
			t.Fatalf("approve %s: %v", owner, err)
		}
	}
	got, err := svc.Recall(ctx, contracts.RecallRequest{BotID: 1, Kind: "private", TargetID: 200, Limit: 5})
	if err != nil {
		t.Fatalf("recall: %v", err)
	}
	if len(got) != 1 || got[0].Content != "用户200的偏好" {
		t.Fatalf("private recall = %+v", got)
	}
}

// TestRecallDetailEnforcesVisibility verifies the on-demand detail path only
// returns active, unexpired memories visible to the requested conversation.
// TestRecallDetailEnforcesVisibility 验证按需详情只返回对指定会话可见的
// 激活且未过期记忆。
func detailID(n int) string {
	return fmt.Sprintf("00000000-0000-4000-8000-%012d", n)
}

func TestRecallDetailEnforcesVisibility(t *testing.T) {
	repo := newFakeMemoryRepo()
	svc, _ := NewService(repo)
	ctx := context.Background()
	now := time.Now()
	items := map[string]domain.Memory{
		detailID(1): {ID: detailID(1), BotID: 1, Visibility: domain.VisibilityBotShared, Content: "共享", Status: domain.StatusActive, UpdatedAt: now},
		detailID(2): {ID: detailID(2), BotID: 1, Visibility: domain.VisibilityGroup, OwnerKey: "700", Content: "本群", Status: domain.StatusActive, UpdatedAt: now},
		detailID(3): {ID: detailID(3), BotID: 1, Visibility: domain.VisibilityGroup, OwnerKey: "701", Content: "别的群", Status: domain.StatusActive, UpdatedAt: now},
		detailID(4): {ID: detailID(4), BotID: 1, Visibility: domain.VisibilityPrivateUser, OwnerKey: "200", Content: "私聊", Status: domain.StatusActive, UpdatedAt: now},
		detailID(5): {ID: detailID(5), BotID: 1, Visibility: domain.VisibilityBotShared, Content: "候选", Status: domain.StatusCandidate, UpdatedAt: now},
		detailID(6): {ID: detailID(6), BotID: 1, Visibility: domain.VisibilityBotShared, Content: "过期", Status: domain.StatusActive, ExpiresAt: now.Add(-time.Minute), UpdatedAt: now},
		detailID(7): {ID: detailID(7), BotID: 1, Visibility: domain.VisibilityBotShared, Content: "已删除", Status: domain.StatusDeleted, UpdatedAt: now},
	}
	for id, item := range items {
		repo.items[id] = item
	}
	for _, tc := range []struct {
		name string
		req  contracts.DetailRequest
		want bool
	}{
		{name: "shared visible", req: contracts.DetailRequest{BotID: 1, Kind: "group", TargetID: 700, MemoryID: detailID(1)}, want: true},
		{name: "own group visible", req: contracts.DetailRequest{BotID: 1, Kind: "group", TargetID: 700, MemoryID: detailID(2)}, want: true},
		{name: "other group invisible", req: contracts.DetailRequest{BotID: 1, Kind: "group", TargetID: 700, MemoryID: detailID(3)}, want: false},
		{name: "private only matches same user", req: contracts.DetailRequest{BotID: 1, Kind: "private", TargetID: 200, MemoryID: detailID(4)}, want: true},
		{name: "private does not leak to group", req: contracts.DetailRequest{BotID: 1, Kind: "group", TargetID: 200, MemoryID: detailID(4)}, want: false},
		{name: "candidate invisible", req: contracts.DetailRequest{BotID: 1, Kind: "group", TargetID: 700, MemoryID: detailID(5)}, want: false},
		{name: "expired invisible", req: contracts.DetailRequest{BotID: 1, Kind: "group", TargetID: 700, MemoryID: detailID(6)}, want: false},
		{name: "deleted invisible", req: contracts.DetailRequest{BotID: 1, Kind: "group", TargetID: 700, MemoryID: detailID(7)}, want: false},
		{name: "other bot invisible", req: contracts.DetailRequest{BotID: 2, Kind: "group", TargetID: 700, MemoryID: detailID(1)}, want: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, found, err := svc.RecallDetail(ctx, tc.req)
			if err != nil {
				t.Fatalf("recall detail: %v", err)
			}
			if found != tc.want {
				t.Fatalf("found=%v, want %v, item=%+v", found, tc.want, got)
			}
		})
	}
}

// TestRecallDetailEnforcesVisibilityAndLifecycle verifies one memory detail is
// available only to its own active, unexpired scope, and that stored source
// metadata is returned without inventing a source.
// TestRecallDetailEnforcesVisibilityAndLifecycle 验证记忆详情只对自身激活且未过期的
// 作用域可用，并原样返回已存来源元数据而不编造来源。
func TestRecallDetailEnforcesVisibilityAndLifecycle(t *testing.T) {
	repo := newFakeMemoryRepo()
	svc, _ := NewService(repo)
	ctx := context.Background()
	id := detailID(10)
	repo.items[id] = domain.Memory{
		ID: id, BotID: 1, Visibility: domain.VisibilityGroup, OwnerKey: "700",
		Content: "群友喜欢晚上聊天", Source: "群聊 700", SourceMessageID: 9001,
		Status: domain.StatusActive,
	}
	got, found, err := svc.RecallDetail(ctx, contracts.DetailRequest{BotID: 1, Kind: "group", TargetID: 700, MemoryID: id})
	if err != nil || !found || got.Source != "群聊 700" || got.SourceMessageID != 9001 || got.Content != "群友喜欢晚上聊天" {
		t.Fatalf("detail = %+v found=%v err=%v", got, found, err)
	}
	for _, in := range []contracts.DetailRequest{
		{BotID: 1, Kind: "group", TargetID: 701, MemoryID: id},
		{BotID: 1, Kind: "private", TargetID: 700, MemoryID: id},
		{BotID: 2, Kind: "group", TargetID: 700, MemoryID: id},
	} {
		if _, found, err := svc.RecallDetail(ctx, in); err != nil || found {
			t.Fatalf("cross-scope detail leaked: %+v found=%v err=%v", in, found, err)
		}
	}
	candidateID := detailID(11)
	repo.items[candidateID] = domain.Memory{ID: candidateID, BotID: 1, Visibility: domain.VisibilityBotShared, Status: domain.StatusCandidate}
	if _, found, _ := svc.RecallDetail(ctx, contracts.DetailRequest{BotID: 1, Kind: "group", TargetID: 700, MemoryID: candidateID}); found {
		t.Fatal("candidate memory detail leaked")
	}
	expiredID := detailID(12)
	expired := time.Now().Add(-time.Minute)
	repo.items[expiredID] = domain.Memory{ID: expiredID, BotID: 1, Visibility: domain.VisibilityBotShared, Status: domain.StatusActive, ExpiresAt: expired}
	if _, found, _ := svc.RecallDetail(ctx, contracts.DetailRequest{BotID: 1, Kind: "group", TargetID: 700, MemoryID: expiredID}); found {
		t.Fatal("expired memory detail leaked")
	}
}

// TestRecallDetailRejectsMalformedID verifies a malformed path parameter is a
// caller error, not an infrastructure error.
// TestRecallDetailRejectsMalformedID 验证非法 ID 是调用方参数错误，不是内部错误。
func TestRecallDetailRejectsMalformedID(t *testing.T) {
	svc, _ := NewService(newFakeMemoryRepo())
	_, found, err := svc.RecallDetail(context.Background(), contracts.DetailRequest{
		BotID: 1, Kind: "group", TargetID: 700, MemoryID: "not-a-uuid",
	})
	if found {
		t.Fatal("malformed memory id must not be found")
	}
	if !errors.Is(err, ErrInvalidID) {
		t.Fatalf("err=%v, want ErrInvalidID", err)
	}
}

// TestMemoryVisibleToKeepsGroupsAndPrivateUsersSeparate verifies the domain
// visibility predicate cannot merge two real users or two groups.
// TestMemoryVisibleToKeepsGroupsAndPrivateUsersSeparate 验证领域可见性谓词不会把
// 两个真实用户或两个群合并。
func TestMemoryVisibleToKeepsGroupsAndPrivateUsersSeparate(t *testing.T) {
	group := domain.Memory{Visibility: domain.VisibilityGroup, OwnerKey: "700"}
	if !group.VisibleTo("group", 700, "") || group.VisibleTo("group", 701, "") || group.VisibleTo("private", 700, "") {
		t.Fatal("group visibility crossed a boundary")
	}
	user := domain.Memory{Visibility: domain.VisibilityPrivateUser, OwnerKey: "200"}
	if !user.VisibleTo("private", 200, "") || user.VisibleTo("private", 201, "") || user.VisibleTo("group", 200, "") {
		t.Fatal("private visibility crossed a boundary")
	}
	shared := domain.Memory{Visibility: domain.VisibilityBotShared}
	if !shared.VisibleTo("group", 700, "") || !shared.VisibleTo("private", 200, "") {
		t.Fatal("bot-shared memory must be visible to every conversation")
	}
}

// TestProposeCompleteKeyKeepsDifferentSubjectsSeparate verifies two users saying
// the same text do not collapse into one candidate.
// TestProposeCompleteKeyKeepsDifferentSubjectsSeparate 验证不同用户说同一句话
// 不会被合并成同一个候选。
func TestProposeCompleteKeyKeepsDifferentSubjectsSeparate(t *testing.T) {
	svc, _ := NewService(newFakeMemoryRepo())
	ctx := context.Background()
	first, duplicate, err := svc.Propose(ctx, CandidateInput{BotID: 1, Visibility: domain.VisibilityGroup, OwnerKey: "700", Content: "我喜欢茶", SubjectType: domain.SubjectUser, SubjectID: "1001", Origin: domain.OriginManual})
	if err != nil || duplicate {
		t.Fatalf("first propose: id=%s dup=%v err=%v", first, duplicate, err)
	}
	second, duplicate, err := svc.Propose(ctx, CandidateInput{BotID: 1, Visibility: domain.VisibilityGroup, OwnerKey: "700", Content: "我喜欢茶", SubjectType: domain.SubjectUser, SubjectID: "1002", Origin: domain.OriginManual})
	if err != nil || duplicate || second == first {
		t.Fatalf("second subject: id=%s dup=%v err=%v first=%s", second, duplicate, err, first)
	}
}

// TestProposeExtractionUnknownUsesSourceProvenance verifies the server derives
// an unknown extraction subject from the source message and sender, so retries
// dedupe while different speakers with identical text remain separate.
// TestProposeExtractionUnknownUsesSourceProvenance 验证服务器从来源消息与发送者
// 构造未知提取主体，使重试可去重，而不同发送者的相同文本不会被合并。
func TestProposeExtractionUnknownUsesSourceProvenance(t *testing.T) {
	repo := newFakeMemoryRepo()
	svc, _ := NewService(repo)
	ctx := context.Background()
	in := CandidateInput{BotID: 1, Visibility: domain.VisibilityGroup, OwnerKey: "700", Content: "来源不明的同一句话", SubjectType: domain.SubjectUnknown, Origin: domain.OriginExtraction, SourceMessageID: 11, SourceSenderID: 1001}
	first, duplicate, err := svc.Propose(ctx, in)
	if err != nil || duplicate {
		t.Fatalf("first propose: id=%s dup=%v err=%v", first, duplicate, err)
	}
	second, duplicate, err := svc.Propose(ctx, in)
	if err != nil || !duplicate || second != first {
		t.Fatalf("second propose: id=%s dup=%v err=%v first=%s", second, duplicate, err, first)
	}
	differentSender := in
	differentSender.SourceSenderID = 1002
	third, duplicate, err := svc.Propose(ctx, differentSender)
	if err != nil || duplicate || third == first {
		t.Fatalf("different sender: id=%s dup=%v err=%v first=%s", third, duplicate, err, first)
	}
	stored := repo.items[first]
	if stored.SubjectType != domain.SubjectUnknown || stored.SubjectID != domain.ExtractionUnknownSubjectID(in.SourceMessageID, in.SourceSenderID) || stored.DedupeKey == "" {
		t.Fatalf("stored provenance subject=%q/%q key=%q", stored.SubjectType, stored.SubjectID, stored.DedupeKey)
	}
}

// TestProposeExtractionUnknownRequiresSource verifies extraction unknowns cannot
// fall back to content-only dedupe without source provenance.
// TestProposeExtractionUnknownRequiresSource 验证未知提取主体缺少来源出处处不能回退到
// 仅按内容去重。
func TestProposeExtractionUnknownRequiresSource(t *testing.T) {
	svc, _ := NewService(newFakeMemoryRepo())
	_, _, err := svc.Propose(context.Background(), CandidateInput{BotID: 1, Visibility: domain.VisibilityGroup, OwnerKey: "700", Content: "来源不明的同一句话", SubjectType: domain.SubjectUnknown, Origin: domain.OriginExtraction})
	if err == nil {
		t.Fatal("extraction unknown without source provenance was accepted")
	}
}
