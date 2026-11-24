package infrastructure

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"testing"

	memoryapp "github.com/HappyLadySauceM/Nagi/internal/memory/application"
	"github.com/HappyLadySauceM/Nagi/internal/memory/domain"
	"github.com/HappyLadySauceM/Nagi/pkg/db/dbtest"
)

// seedReplacementFixture cleans and seeds one candidate and one active memory
// in the same replacement-eligible scope.
// seedReplacementFixture 清理并种入同一可替代作用域的一条候选与一条 active 记忆。
func seedReplacementFixture(t *testing.T, queue string) (*Repo, string, string, string) {
	t.Helper()
	pool := dbtest.Open(t)
	repo, err := NewRepo(pool)
	if err != nil {
		t.Fatalf("new repo: %v", err)
	}
	ctx := context.Background()
	const botID = 993001
	if _, err := pool.Exec(ctx,
		`INSERT INTO bot_accounts (bot_id, nickname) VALUES ($1, $2) ON CONFLICT (bot_id) DO NOTHING`,
		botID, "memory-replacement-test",
	); err != nil {
		t.Fatalf("seed bot: %v", err)
	}
	if _, err := pool.Exec(ctx, `DELETE FROM memory_replacements WHERE bot_id=$1`, botID); err != nil {
		t.Fatalf("clean replacements: %v", err)
	}
	if _, err := pool.Exec(ctx, `DELETE FROM memories WHERE bot_id=$1`, botID); err != nil {
		t.Fatalf("clean memories: %v", err)
	}
	oldID, err := repo.Insert(ctx, domain.Memory{
		BotID: botID, Visibility: domain.VisibilityGroup, OwnerKey: "700",
		Content: "A 爱喝美式", Status: domain.StatusActive,
		SubjectType: domain.SubjectUser, SubjectID: "1001",
		Source: "manual", Origin: domain.OriginManual, ApprovedBy: "admin",
	})
	if err != nil {
		t.Fatalf("insert old: %v", err)
	}
	newID, err := repo.Insert(ctx, domain.Memory{
		BotID: botID, Visibility: domain.VisibilityGroup, OwnerKey: "700",
		Content: "A 爱喝拿铁", Status: domain.StatusCandidate,
		SubjectType: domain.SubjectUser, SubjectID: "1001",
		Source: "manual", Origin: domain.OriginManual,
	})
	if err != nil {
		t.Fatalf("insert new: %v", err)
	}
	return repo, oldID, newID, "memory-replacement-test-" + queue
}

// TestMemorySupersedeLifecycle verifies the explicit replacement transaction,
// idempotency, management relation view and visibility after replacement.
// TestMemorySupersedeLifecycle 验证显式替代事务、幂等、管理关系视图及替代后的可见性。
func TestMemorySupersedeLifecycle(t *testing.T) {
	repo, oldID, newID, key := seedReplacementFixture(t, "lifecycle")
	ctx := context.Background()
	result, err := repo.Supersede(ctx, domain.SupersedeInput{
		NewMemoryID: newID, OldMemoryID: oldID, Actor: "admin",
		Reason: "新事实替代旧事实", IdempotencyKey: key,
	})
	if err != nil {
		t.Fatalf("supersede: %v", err)
	}
	if result.NewStatus != domain.StatusActive || result.OldStatus != domain.StatusSuperseded || result.Idempotent {
		t.Fatalf("supersede result = %+v", result)
	}
	newMemory, found, err := repo.Get(ctx, newID)
	if err != nil || !found || newMemory.Status != domain.StatusActive || newMemory.ApprovedBy != "admin" {
		t.Fatalf("new memory = %+v found=%v err=%v", newMemory, found, err)
	}
	oldMemory, found, err := repo.Get(ctx, oldID)
	if err != nil || !found || oldMemory.Status != domain.StatusSuperseded {
		t.Fatalf("old memory = %+v found=%v err=%v", oldMemory, found, err)
	}
	oldVisible, err := repo.SearchVisible(ctx, domain.SearchRequest{BotID: 993001, Kind: "group", TargetID: 700, Query: "爱喝", Limit: 5})
	if err != nil {
		t.Fatalf("search visible: %v", err)
	}
	if len(oldVisible) != 1 || oldVisible[0].ID != newID {
		t.Fatalf("visible after supersede = %+v", oldVisible)
	}

	management, replacements, found, err := repo.Management(ctx, newID, 993001)
	if err != nil || !found || management.ID != newID || len(replacements) != 1 {
		t.Fatalf("management = %+v replacements=%+v found=%v err=%v", management, replacements, found, err)
	}
	if replacements[0].Direction != "as_new" || replacements[0].OldMemoryID != oldID || replacements[0].OldStatus != domain.StatusSuperseded || replacements[0].Counterpart.Missing {
		t.Fatalf("new-side replacement = %+v", replacements[0])
	}
	_, replacements, found, err = repo.Management(ctx, oldID, 993001)
	if err != nil || !found || len(replacements) != 1 || replacements[0].Direction != "as_old" {
		t.Fatalf("old-side management = %+v found=%v err=%v", replacements, found, err)
	}

	idempotent, err := repo.Supersede(ctx, domain.SupersedeInput{
		NewMemoryID: newID, OldMemoryID: oldID, Actor: "admin",
		Reason: "重试", IdempotencyKey: key,
	})
	if err != nil || !idempotent.Idempotent || idempotent.NewStatus != domain.StatusActive || idempotent.OldStatus != domain.StatusSuperseded {
		t.Fatalf("idempotent retry = %+v err=%v", idempotent, err)
	}
	if _, err := repo.Supersede(ctx, domain.SupersedeInput{
		NewMemoryID: newID, OldMemoryID: oldID, Actor: "admin", Reason: "different key", IdempotencyKey: key + "-other",
	}); !errors.Is(err, memoryapp.ErrIdempotencyConflict) {
		t.Fatalf("different key error = %v", err)
	}

	if deleted, err := repo.DeleteMemory(ctx, oldID); err != nil || !deleted {
		t.Fatalf("delete old: deleted=%v err=%v", deleted, err)
	}
	_, replacements, found, err = repo.Management(ctx, newID, 993001)
	if err != nil || !found || len(replacements) != 1 || !replacements[0].Counterpart.Missing || replacements[0].Counterpart.Status != "" {
		t.Fatalf("post-delete management = %+v found=%v err=%v", replacements, found, err)
	}
	afterDelete, err := repo.Supersede(ctx, domain.SupersedeInput{
		NewMemoryID: newID, OldMemoryID: oldID, Actor: "admin", Reason: "retry after delete", IdempotencyKey: key,
	})
	if err != nil || !afterDelete.Idempotent || afterDelete.OldStatus != domain.StatusDeleted {
		t.Fatalf("post-delete idempotent retry = %+v err=%v", afterDelete, err)
	}
}

// TestMemorySupersedeRejectsSubjectAndStatusConflicts verifies unknown,
// mismatched subjects and invalid lifecycle states fail closed.
// TestMemorySupersedeRejectsSubjectAndStatusConflicts 验证未知/不匹配主体与非法
// 生命周期状态都会关闭式失败。
func TestMemorySupersedeRejectsSubjectAndStatusConflicts(t *testing.T) {
	repo, oldID, newID, key := seedReplacementFixture(t, "conflicts")
	ctx := context.Background()
	if _, err := repo.Supersede(ctx, domain.SupersedeInput{
		NewMemoryID: newID, OldMemoryID: oldID, Actor: "admin", IdempotencyKey: key + "-wrong-subject",
	}); err != nil {
		t.Fatalf("unexpected initial supersede: %v", err)
	}
	// The new row is now active, so a fresh pair cannot be superseded again.
	if _, err := repo.Supersede(ctx, domain.SupersedeInput{
		NewMemoryID: newID, OldMemoryID: oldID, Actor: "admin", IdempotencyKey: key + "-status",
	}); !errors.Is(err, memoryapp.ErrIdempotencyConflict) {
		t.Fatalf("same pair different key error = %v", err)
	}

	unknownOld, err := repo.Insert(ctx, domain.Memory{
		BotID: 993001, Visibility: domain.VisibilityGroup, OwnerKey: "700",
		Content: "未知主体旧事实", Status: domain.StatusActive,
		SubjectType: domain.SubjectUnknown, SubjectID: "source:1:2", Source: "manual",
	})
	if err != nil {
		t.Fatalf("insert unknown old: %v", err)
	}
	unknownNew, err := repo.Insert(ctx, domain.Memory{
		BotID: 993001, Visibility: domain.VisibilityGroup, OwnerKey: "700",
		Content: "未知主体新事实", Status: domain.StatusCandidate,
		SubjectType: domain.SubjectUnknown, SubjectID: "source:3:4", Source: "manual",
	})
	if err != nil {
		t.Fatalf("insert unknown new: %v", err)
	}
	if _, err := repo.Supersede(ctx, domain.SupersedeInput{
		NewMemoryID: unknownNew, OldMemoryID: unknownOld, Actor: "admin", IdempotencyKey: key + "-unknown",
	}); !errors.Is(err, memoryapp.ErrSubjectAmbiguous) {
		t.Fatalf("unknown subject error = %v", err)
	}
	mismatchNew, err := repo.Insert(ctx, domain.Memory{
		BotID: 993001, Visibility: domain.VisibilityGroup, OwnerKey: "700",
		Content: "A 的其他事实", Status: domain.StatusCandidate,
		SubjectType: domain.SubjectUser, SubjectID: "2001", Source: "manual",
	})
	if err != nil {
		t.Fatalf("insert mismatch new: %v", err)
	}
	mismatchOld, err := repo.Insert(ctx, domain.Memory{
		BotID: 993001, Visibility: domain.VisibilityGroup, OwnerKey: "700",
		Content: "A 的旧事实", Status: domain.StatusActive,
		SubjectType: domain.SubjectUser, SubjectID: "1001", Source: "manual",
	})
	if err != nil {
		t.Fatalf("insert mismatch old: %v", err)
	}
	if _, err := repo.Supersede(ctx, domain.SupersedeInput{
		NewMemoryID: mismatchNew, OldMemoryID: mismatchOld, Actor: "admin", IdempotencyKey: key + "-mismatch",
	}); !errors.Is(err, memoryapp.ErrSubjectMismatch) {
		t.Fatalf("mismatch subject error = %v", err)
	}
}

// TestMemorySupersedeConcurrentApprove verifies approve and supersede cannot
// both win; a failed supersede must not create a partial relation.
// TestMemorySupersedeConcurrentApprove 验证 approve 与 supersede 不能同时成功；
// 失败的 supersede 不得留下部分关系。
func TestMemorySupersedeConcurrentApprove(t *testing.T) {
	repo, oldID, newID, key := seedReplacementFixture(t, "concurrent")
	ctx := context.Background()
	var wg sync.WaitGroup
	start := make(chan struct{})
	approveErr := make(chan error, 1)
	supersedeErr := make(chan error, 1)
	wg.Add(2)
	go func() {
		defer wg.Done()
		<-start
		approveErr <- repo.ApproveMemory(ctx, newID, "admin")
	}()
	go func() {
		defer wg.Done()
		<-start
		supersedeErr <- func() error {
			_, err := repo.Supersede(ctx, domain.SupersedeInput{
				NewMemoryID: newID, OldMemoryID: oldID, Actor: "admin", Reason: "race", IdempotencyKey: key + "-race",
			})
			return err
		}()
	}()
	close(start)
	wg.Wait()
	aerr, serr := <-approveErr, <-supersedeErr
	successes := 0
	if aerr == nil {
		successes++
	}
	if serr == nil {
		successes++
	}
	if successes != 1 {
		t.Fatalf("approve err=%v supersede err=%v, want exactly one success", aerr, serr)
	}
	var replacementCount int
	if err := repo.pool.QueryRow(context.Background(), `SELECT count(*) FROM memory_replacements WHERE new_memory_id=$1::uuid`, newID).Scan(&replacementCount); err != nil {
		t.Fatalf("count replacements: %v", err)
	}
	if serr != nil && replacementCount != 0 {
		t.Fatalf("failed supersede left %d relation rows", replacementCount)
	}
	if serr == nil {
		newMemory, _, _ := repo.Get(ctx, newID)
		oldMemory, _, _ := repo.Get(ctx, oldID)
		if newMemory.Status != domain.StatusActive || oldMemory.Status != domain.StatusSuperseded {
			t.Fatalf("successful supersede states = new:%s old:%s", newMemory.Status, oldMemory.Status)
		}
	}
}

// TestMemorySupersedeConcurrentDelete verifies delete and supersede serialize:
// delete first yields not-found; supersede first leaves the relation but a
// later delete removes only the row.
// TestMemorySupersedeConcurrentDelete 验证 delete 与 supersede 串行：delete 先则
// not-found；supersede 先则关系保留但随后删除只移除行。
func TestMemorySupersedeConcurrentDelete(t *testing.T) {
	repo, oldID, newID, key := seedReplacementFixture(t, "delete-race")
	ctx := context.Background()
	start := make(chan struct{})
	var wg sync.WaitGroup
	deleteErr := make(chan error, 1)
	supersedeErr := make(chan error, 1)
	wg.Add(2)
	go func() {
		defer wg.Done()
		<-start
		_, err := repo.DeleteMemory(ctx, oldID)
		deleteErr <- err
	}()
	go func() {
		defer wg.Done()
		<-start
		_, err := repo.Supersede(ctx, domain.SupersedeInput{
			NewMemoryID: newID, OldMemoryID: oldID, Actor: "admin", Reason: "delete race", IdempotencyKey: key + "-delete",
		})
		supersedeErr <- err
	}()
	close(start)
	wg.Wait()
	if derr := <-deleteErr; derr != nil {
		t.Fatalf("delete error: %v", derr)
	}
	serr := <-supersedeErr
	if serr != nil && !errors.Is(serr, memoryapp.ErrMemoryNotFound) {
		t.Fatalf("supersede after delete error = %v", serr)
	}
	if serr == nil {
		var count int
		if err := repo.pool.QueryRow(context.Background(), `SELECT count(*) FROM memory_replacements WHERE new_memory_id=$1::uuid`, newID).Scan(&count); err != nil || count != 1 {
			t.Fatalf("relation after supersede/delete count=%d err=%v", count, err)
		}
	}
}

// TestMemorySupersedeConcurrentEdit verifies edit and supersede serialize:
// edit first keeps the edited content under the replacement; supersede first
// makes the edit fail with a status conflict and never revives the old row.
// TestMemorySupersedeConcurrentEdit 验证 edit 与 supersede 串行：edit 先则替代保留
// 编辑后的内容；supersede 先则 edit 状态冲突且不会复活旧条目。
func TestMemorySupersedeConcurrentEdit(t *testing.T) {
	repo, oldID, newID, key := seedReplacementFixture(t, "edit-race")
	ctx := context.Background()
	oldMemory, found, err := repo.Get(ctx, oldID)
	if err != nil || !found {
		t.Fatalf("get old memory: found=%v err=%v", found, err)
	}
	oldMemory.Content = "A 爱喝手冲"
	start := make(chan struct{})
	var wg sync.WaitGroup
	editErr := make(chan error, 1)
	supersedeErr := make(chan error, 1)
	wg.Add(2)
	go func() {
		defer wg.Done()
		<-start
		editErr <- repo.UpdateMemoryLocked(ctx, oldMemory)
	}()
	go func() {
		defer wg.Done()
		<-start
		_, err := repo.Supersede(ctx, domain.SupersedeInput{
			NewMemoryID: newID, OldMemoryID: oldID, Actor: "admin", Reason: "edit race", IdempotencyKey: key + "-edit",
		})
		supersedeErr <- err
	}()
	close(start)
	wg.Wait()
	eerr, serr := <-editErr, <-supersedeErr
	if serr != nil {
		t.Fatalf("supersede error: %v", serr)
	}
	if eerr != nil && !errors.Is(eerr, memoryapp.ErrMemoryStatusConflict) {
		t.Fatalf("edit error = %v", eerr)
	}
	finalOld, found, err := repo.Get(ctx, oldID)
	if err != nil || !found || finalOld.Status != domain.StatusSuperseded {
		t.Fatalf("final old memory = %+v found=%v err=%v", finalOld, found, err)
	}
	if eerr == nil && finalOld.Content != "A 爱喝手冲" {
		t.Fatalf("edited content = %q, want edited value", finalOld.Content)
	}
}

// TestMemoryCandidateInsertLockedDeduplicates verifies the manual candidate
// insert path uses the shared mutation lock and dedupe semantics.
// TestMemoryCandidateInsertLockedDeduplicates 验证人工候选插入路径使用共享变更锁
// 并保持去重语义。
func TestMemoryCandidateInsertLockedDeduplicates(t *testing.T) {
	repo, _, _, _ := seedReplacementFixture(t, "candidate-insert")
	ctx := context.Background()
	candidate := domain.Memory{
		BotID: 993001, Visibility: domain.VisibilityGroup, OwnerKey: "700",
		Content: "A 的新候选事实", Status: domain.StatusCandidate,
		SubjectType: domain.SubjectUser, SubjectID: "1001", Source: "manual", Origin: domain.OriginManual,
		DedupeKey: domain.ComputeDedupeKey(993001, domain.VisibilityGroup, "700", domain.SubjectUser, "1001", "A 的新候选事实"),
	}
	firstID, duplicate, err := repo.InsertCandidateLocked(ctx, candidate)
	if err != nil || duplicate {
		t.Fatalf("first insert = %q duplicate=%v err=%v", firstID, duplicate, err)
	}
	secondID, duplicate, err := repo.InsertCandidateLocked(ctx, candidate)
	if err != nil || !duplicate || secondID != firstID {
		t.Fatalf("second insert = %q duplicate=%v err=%v, want existing %q", secondID, duplicate, err, firstID)
	}
}

func TestMemoryEditBeforeApprovalKeepsEditedContent(t *testing.T) {
	repo, _, id, _ := seedReplacementFixture(t, "edit-first")
	ctx := context.Background()
	memory, found, err := repo.Get(ctx, id)
	if err != nil || !found {
		t.Fatalf("candidate: found=%v err=%v", found, err)
	}
	memory.Content = "A 爱喝冷萃"
	if err := repo.UpdateMemoryLocked(ctx, memory); err != nil {
		t.Fatalf("edit: %v", err)
	}
	if err := repo.ApproveMemory(ctx, id, "admin"); err != nil {
		t.Fatalf("approve: %v", err)
	}
	final, found, err := repo.Get(ctx, id)
	if err != nil || !found || final.Status != domain.StatusActive || final.ApprovedBy != "admin" || final.Content != memory.Content {
		t.Fatalf("active memory = %+v found=%v err=%v", final, found, err)
	}
}

func TestMemoryStaleEditAfterApprovalLeavesRecordsUnchanged(t *testing.T) {
	repo, _, id, _ := seedReplacementFixture(t, "approve-first")
	ctx := context.Background()
	stale, found, err := repo.Get(ctx, id)
	if err != nil || !found {
		t.Fatalf("candidate: found=%v err=%v", found, err)
	}
	stale.Content = "A 爱喝冷萃"
	if err := repo.ApproveMemory(ctx, id, "admin"); err != nil {
		t.Fatalf("approve: %v", err)
	}
	before, linksBefore, found, err := repo.Management(ctx, id, stale.BotID)
	if err != nil || !found || before.Status != domain.StatusActive || before.ApprovedBy != "admin" || before.Content == stale.Content {
		t.Fatalf("approved memory = %+v found=%v err=%v", before, found, err)
	}
	if err := repo.UpdateMemoryLocked(ctx, stale); !errors.Is(err, memoryapp.ErrMemoryStatusConflict) {
		t.Fatalf("stale edit = %v, want status conflict", err)
	}
	after, linksAfter, found, err := repo.Management(ctx, id, stale.BotID)
	if err != nil || !found || !reflect.DeepEqual(before, after) || !reflect.DeepEqual(linksBefore, linksAfter) {
		t.Fatalf("stale edit changed records: before=%+v after=%+v links=%v/%v found=%v err=%v", before, after, linksBefore, linksAfter, found, err)
	}
}

// TestMemoryApproveWithEditConcurrentApprove verifies edit+approve and plain
// approve serialize under one transaction: exactly one succeeds, and a failed
// edit+approve never commits its content.
// TestMemoryApproveWithEditConcurrentApprove 验证编辑+审批与普通审批在同一事务
// 锁下串行：只能一方成功，失败的编辑+审批不会落库内容。
func TestMemoryApproveWithEditConcurrentApprove(t *testing.T) {
	repo, _, newID, key := seedReplacementFixture(t, "approve-edit-race")
	ctx := context.Background()
	start := make(chan struct{})
	var wg sync.WaitGroup
	editErr := make(chan error, 1)
	approveErr := make(chan error, 1)
	wg.Add(2)
	go func() {
		defer wg.Done()
		<-start
		editErr <- repo.ApproveMemoryWithEdit(ctx, newID, "admin", "A 爱喝冷萃", "", nil)
	}()
	go func() {
		defer wg.Done()
		<-start
		approveErr <- repo.ApproveMemory(ctx, newID, "admin")
	}()
	close(start)
	wg.Wait()
	eerr, aerr := <-editErr, <-approveErr
	successes := 0
	if eerr == nil {
		successes++
	}
	if aerr == nil {
		successes++
	}
	if successes != 1 {
		t.Fatalf("edit err=%v approve err=%v, want exactly one success (key=%s)", eerr, aerr, key)
	}
	memory, found, err := repo.Get(ctx, newID)
	if err != nil || !found || memory.Status != domain.StatusActive || memory.ApprovedBy != "admin" {
		t.Fatalf("final memory = %+v found=%v err=%v", memory, found, err)
	}
	if eerr == nil && memory.Content != "A 爱喝冷萃" {
		t.Fatalf("successful edit content = %q", memory.Content)
	}
	if eerr != nil && (!errors.Is(eerr, memoryapp.ErrMemoryStatusConflict) || memory.Content == "A 爱喝冷萃") {
		t.Fatalf("failed edit err=%v content=%q", eerr, memory.Content)
	}
}

// TestMemoryApproveWithEditConcurrentSupersede verifies edit+approve and
// supersede serialize; when supersede wins the edit fails without committing
// content or a replacement relation.
// TestMemoryApproveWithEditConcurrentSupersede 验证编辑+审批与替代串行；替代先
// 成功时编辑失败且不落库内容，也不留下替代关系。
func TestMemoryApproveWithEditConcurrentSupersede(t *testing.T) {
	repo, oldID, newID, key := seedReplacementFixture(t, "approve-edit-supersede-race")
	ctx := context.Background()
	start := make(chan struct{})
	var wg sync.WaitGroup
	editErr := make(chan error, 1)
	supersedeErr := make(chan error, 1)
	wg.Add(2)
	go func() {
		defer wg.Done()
		<-start
		editErr <- repo.ApproveMemoryWithEdit(ctx, newID, "admin", "A 爱喝冷萃", "", nil)
	}()
	go func() {
		defer wg.Done()
		<-start
		_, err := repo.Supersede(ctx, domain.SupersedeInput{
			NewMemoryID: newID, OldMemoryID: oldID, Actor: "admin", Reason: "race", IdempotencyKey: key + "-edit-supersede",
		})
		supersedeErr <- err
	}()
	close(start)
	wg.Wait()
	eerr, serr := <-editErr, <-supersedeErr
	successes := 0
	if eerr == nil {
		successes++
	}
	if serr == nil {
		successes++
	}
	if successes != 1 {
		t.Fatalf("edit err=%v supersede err=%v, want exactly one success", eerr, serr)
	}
	memory, found, err := repo.Get(ctx, newID)
	if err != nil || !found || memory.Status != domain.StatusActive {
		t.Fatalf("final new memory = %+v found=%v err=%v", memory, found, err)
	}
	var relationCount int
	if err := repo.pool.QueryRow(ctx, `SELECT count(*) FROM memory_replacements WHERE new_memory_id=$1::uuid`, newID).Scan(&relationCount); err != nil {
		t.Fatalf("count replacements: %v", err)
	}
	if serr != nil {
		if !errors.Is(serr, memoryapp.ErrMemoryStatusConflict) || relationCount != 0 {
			t.Fatalf("failed supersede err=%v relations=%d", serr, relationCount)
		}
	}
	if eerr != nil && (!errors.Is(eerr, memoryapp.ErrMemoryStatusConflict) || memory.Content == "A 爱喝冷萃") {
		t.Fatalf("failed edit err=%v content=%q", eerr, memory.Content)
	}
}
