package infrastructure

import (
	"context"
	"testing"
	"time"

	"github.com/HappyLadySauceM/Nagi/internal/scope/domain"
	"github.com/HappyLadySauceM/Nagi/pkg/db/dbtest"
)

// TestMemoryAuthorizationEventsFollowRelevantPolicyChanges verifies that
// unrelated updates preserve the generation while enabled/store/allow changes
// and independent policy deletion create fresh generations.
// TestMemoryAuthorizationEventsFollowRelevantPolicyChanges 验证无关更新保持代际，
// enabled/store/allow 变化与独立策略删除产生新代际。
func TestMemoryAuthorizationEventsFollowRelevantPolicyChanges(t *testing.T) {
	pool := dbtest.Open(t)
	repo, err := NewRepo(pool)
	if err != nil {
		t.Fatalf("new repo: %v", err)
	}
	ctx := context.Background()
	const botID = 991001
	const groupID = 991002
	if _, err := pool.Exec(ctx, `INSERT INTO bot_accounts (bot_id, nickname) VALUES ($1, 'memory-auth-events') ON CONFLICT DO NOTHING`, botID); err != nil {
		t.Fatalf("seed bot: %v", err)
	}
	defer func() {
		_, _ = pool.Exec(ctx, `DELETE FROM scope_policies WHERE bot_id=$1`, botID)
		_, _ = pool.Exec(ctx, `DELETE FROM scope_memory_authorization_events WHERE bot_id=$1`, botID)
	}()

	policy := domain.Default(domain.ScopeGroup, groupID)
	policy.Enabled = true
	policy.StoreMessages = true
	policy.AllowMemoryWrite = true
	if err := repo.Upsert(ctx, botID, policy); err != nil {
		t.Fatalf("create policy: %v", err)
	}
	first, authorized, err := repo.ResolveMemoryAuthorization(ctx, botID, domain.ScopeGroup, groupID)
	if err != nil || !authorized || first == 0 {
		t.Fatalf("initial authorization: event=%d authorized=%v err=%v", first, authorized, err)
	}

	// Reply mode is unrelated to memory authorization and must not move the
	// generation.
	policy.ReplyMode = domain.ModeAlwaysReply
	if err := repo.Upsert(ctx, botID, policy); err != nil {
		t.Fatalf("unrelated update: %v", err)
	}
	second, authorized, err := repo.ResolveMemoryAuthorization(ctx, botID, domain.ScopeGroup, groupID)
	if err != nil || !authorized || second != first {
		t.Fatalf("unrelated update changed event: first=%d second=%d authorized=%v err=%v", first, second, authorized, err)
	}

	policy.AllowMemoryWrite = false
	if err := repo.Upsert(ctx, botID, policy); err != nil {
		t.Fatalf("disable write: %v", err)
	}
	third, authorized, err := repo.ResolveMemoryAuthorization(ctx, botID, domain.ScopeGroup, groupID)
	if err != nil || authorized || third == first {
		t.Fatalf("disabled authorization: event=%d authorized=%v err=%v first=%d", third, authorized, err, first)
	}

	// A short close/reopen must create another generation even if a scanner did
	// not observe the disabled period.
	policy.AllowMemoryWrite = true
	if err := repo.Upsert(ctx, botID, policy); err != nil {
		t.Fatalf("re-enable write: %v", err)
	}
	fourth, authorized, err := repo.ResolveMemoryAuthorization(ctx, botID, domain.ScopeGroup, groupID)
	if err != nil || !authorized || fourth == third || fourth == first {
		t.Fatalf("re-enabled authorization: event=%d authorized=%v err=%v first=%d third=%d", fourth, authorized, err, first, third)
	}

	// enabled and store_messages are also authorization switches, not just
	// allow_memory_write; each close/reopen must isolate a generation.
	policy.Enabled = false
	if err := repo.Upsert(ctx, botID, policy); err != nil {
		t.Fatalf("disable enabled: %v", err)
	}
	disabledEnabled, authorized, err := repo.ResolveMemoryAuthorization(ctx, botID, domain.ScopeGroup, groupID)
	if err != nil || authorized || disabledEnabled == fourth {
		t.Fatalf("disabled enabled: event=%d authorized=%v err=%v fourth=%d", disabledEnabled, authorized, err, fourth)
	}
	policy.Enabled = true
	if err := repo.Upsert(ctx, botID, policy); err != nil {
		t.Fatalf("re-enable enabled: %v", err)
	}
	enabledAgain, authorized, err := repo.ResolveMemoryAuthorization(ctx, botID, domain.ScopeGroup, groupID)
	if err != nil || !authorized || enabledAgain == disabledEnabled {
		t.Fatalf("re-enabled enabled: event=%d authorized=%v err=%v disabled=%d", enabledAgain, authorized, err, disabledEnabled)
	}
	policy.StoreMessages = false
	if err := repo.Upsert(ctx, botID, policy); err != nil {
		t.Fatalf("disable store: %v", err)
	}
	disabledStore, authorized, err := repo.ResolveMemoryAuthorization(ctx, botID, domain.ScopeGroup, groupID)
	if err != nil || authorized || disabledStore == enabledAgain {
		t.Fatalf("disabled store: event=%d authorized=%v err=%v enabled=%d", disabledStore, authorized, err, enabledAgain)
	}
	policy.StoreMessages = true
	if err := repo.Upsert(ctx, botID, policy); err != nil {
		t.Fatalf("re-enable store: %v", err)
	}
	storeAgain, authorized, err := repo.ResolveMemoryAuthorization(ctx, botID, domain.ScopeGroup, groupID)
	if err != nil || !authorized || storeAgain == disabledStore {
		t.Fatalf("re-enabled store: event=%d authorized=%v err=%v disabled=%d", storeAgain, authorized, err, disabledStore)
	}
}

// TestMemoryAuthorizationTemplateAndIndependentTransitions verifies template
// inheritance and independent deletion do not reuse an old template event.
// TestMemoryAuthorizationTemplateAndIndependentTransitions 验证模板继承与独立策略删除
// 不会复用旧模板事件。
func TestMemoryAuthorizationTemplateAndIndependentTransitions(t *testing.T) {
	pool := dbtest.Open(t)
	repo, err := NewRepo(pool)
	if err != nil {
		t.Fatalf("new repo: %v", err)
	}
	ctx := context.Background()
	const botID = 991003
	const groupID = 991004
	if _, err := pool.Exec(ctx, `INSERT INTO bot_accounts (bot_id, nickname) VALUES ($1, 'memory-auth-template') ON CONFLICT DO NOTHING`, botID); err != nil {
		t.Fatalf("seed bot: %v", err)
	}
	defer func() {
		_, _ = pool.Exec(ctx, `DELETE FROM scope_policies WHERE bot_id=$1`, botID)
		_, _ = pool.Exec(ctx, `DELETE FROM scope_memory_authorization_events WHERE bot_id=$1`, botID)
	}()

	template := domain.Default(domain.ScopeGroup, 0)
	template.Enabled = true
	template.StoreMessages = true
	template.AllowMemoryWrite = true
	if err := repo.Upsert(ctx, botID, template); err != nil {
		t.Fatalf("template upsert: %v", err)
	}
	templateEvent, authorized, err := repo.ResolveMemoryAuthorization(ctx, botID, domain.ScopeGroup, groupID)
	if err != nil || !authorized || templateEvent == 0 {
		t.Fatalf("template authorization: event=%d authorized=%v err=%v", templateEvent, authorized, err)
	}

	independent := domain.Default(domain.ScopeGroup, groupID)
	independent.Enabled = true
	independent.StoreMessages = true
	independent.AllowMemoryWrite = false
	if err := repo.Upsert(ctx, botID, independent); err != nil {
		t.Fatalf("independent upsert: %v", err)
	}
	independentEvent, authorized, err := repo.ResolveMemoryAuthorization(ctx, botID, domain.ScopeGroup, groupID)
	if err != nil || authorized || independentEvent == templateEvent {
		t.Fatalf("independent authorization: event=%d authorized=%v err=%v template=%d", independentEvent, authorized, err, templateEvent)
	}

	deleted, err := repo.Delete(ctx, botID, domain.ScopeGroup, groupID)
	if err != nil || !deleted {
		t.Fatalf("delete independent: deleted=%v err=%v", deleted, err)
	}
	removedEvent, authorized, err := repo.ResolveMemoryAuthorization(ctx, botID, domain.ScopeGroup, groupID)
	if err != nil || !authorized || removedEvent == independentEvent || removedEvent == templateEvent {
		t.Fatalf("removed inheritance: event=%d authorized=%v err=%v independent=%d template=%d", removedEvent, authorized, err, independentEvent, templateEvent)
	}
}

// TestStampMemoryAuthorizationUsesTransactionLock verifies that a policy write
// waits for the message transaction that is stamping authorization.
// TestStampMemoryAuthorizationUsesTransactionLock 验证策略写入会等待正在盖章的
// 消息事务。
func TestStampMemoryAuthorizationUsesTransactionLock(t *testing.T) {
	pool := dbtest.Open(t)
	repo, err := NewRepo(pool)
	if err != nil {
		t.Fatalf("new repo: %v", err)
	}
	ctx := context.Background()
	const botID = 991005
	const groupID = 991006
	if _, err := pool.Exec(ctx, `INSERT INTO bot_accounts (bot_id, nickname) VALUES ($1, 'memory-auth-lock') ON CONFLICT DO NOTHING`, botID); err != nil {
		t.Fatalf("seed bot: %v", err)
	}
	defer func() {
		_, _ = pool.Exec(ctx, `DELETE FROM scope_policies WHERE bot_id=$1`, botID)
		_, _ = pool.Exec(ctx, `DELETE FROM scope_memory_authorization_events WHERE bot_id=$1`, botID)
	}()
	policy := domain.Default(domain.ScopeGroup, groupID)
	policy.Enabled = true
	policy.StoreMessages = true
	policy.AllowMemoryWrite = true
	if err := repo.Upsert(ctx, botID, policy); err != nil {
		t.Fatalf("seed policy: %v", err)
	}

	tx, err := pool.Client.Tx(ctx)
	if err != nil {
		t.Fatalf("begin stamp tx: %v", err)
	}
	if _, _, err := repo.StampMemoryAuthorization(ctx, tx, botID, string(domain.ScopeGroup), groupID); err != nil {
		tx.Rollback()
		t.Fatalf("stamp: %v", err)
	}

	done := make(chan error, 1)
	go func() {
		updated := policy
		updated.AllowMemoryWrite = false
		done <- repo.Upsert(ctx, botID, updated)
	}()

	select {
	case err := <-done:
		tx.Rollback()
		t.Fatalf("policy write completed while message stamp transaction held the lock: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit stamp tx: %v", err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("policy write after stamp commit: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("policy write did not proceed after stamp commit")
	}
}
