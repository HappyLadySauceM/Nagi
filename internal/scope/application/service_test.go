// Tests for scope policy resolution with default-deny and write-through cache.
// 作用域策略解析的测试：默认拒绝与写穿缓存。
package application

import (
	"context"
	"testing"
	"time"

	"github.com/HappyLadySauceM/QQBot/internal/scope/domain"
)

// fakeRepo is an in-memory scope repository.
// fakeRepo 是内存版作用域仓库。
type fakeRepo struct {
	stored map[string]domain.Policy
}

func newFakeRepo() *fakeRepo {
	return &fakeRepo{stored: make(map[string]domain.Policy)}
}

func (f *fakeRepo) key(botID int64, t domain.ScopeType, id int64) string {
	return serviceCacheKeyForTest(botID, t, id)
}

func (f *fakeRepo) Get(_ context.Context, botID int64, t domain.ScopeType, id int64) (domain.Policy, bool, error) {
	p, ok := f.stored[f.key(botID, t, id)]
	return p, ok, nil
}

func (f *fakeRepo) List(_ context.Context, _ *int64) ([]domain.Policy, error) {
	out := make([]domain.Policy, 0, len(f.stored))
	for _, p := range f.stored {
		out = append(out, p)
	}
	return out, nil
}

func (f *fakeRepo) Upsert(_ context.Context, botID int64, p domain.Policy) error {
	f.stored[f.key(botID, p.ScopeType, p.ScopeID)] = p
	return nil
}

func (f *fakeRepo) Delete(_ context.Context, botID int64, t domain.ScopeType, id int64) (bool, error) {
	key := f.key(botID, t, id)
	_, ok := f.stored[key]
	delete(f.stored, key)
	return ok, nil
}

func serviceCacheKeyForTest(botID int64, t domain.ScopeType, id int64) string {
	return cacheKey(botID, t, id)
}

// TestResolveDefaultsToDeny verifies that an unknown scope resolves to the safe
// default with everything disabled.
// TestResolveDefaultsToDeny 验证未知作用域解析为全部关闭的安全默认。
func TestResolveDefaultsToDeny(t *testing.T) {
	svc, err := NewService(newFakeRepo())
	if err != nil {
		t.Fatalf("new service: %v", err)
	}
	policy, err := svc.Resolve(context.Background(), 1, domain.ScopeGroup, 42)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if policy.Enabled {
		t.Fatal("unknown scope must resolve to disabled")
	}
	if policy.ReplyMode != domain.ModeMentionOnly {
		t.Fatalf("unknown scope reply mode = %q", policy.ReplyMode)
	}
}

// TestResolveKeepsScopeIdentity verifies that a stored row without identity
// columns still resolves to the requested scope type and id.
// TestResolveKeepsScopeIdentity 验证缺少身份列的已存行解析后仍带请求的作用域类型与 ID。
func TestResolveKeepsScopeIdentity(t *testing.T) {
	repo := newFakeRepo()
	svc, err := NewService(repo)
	if err != nil {
		t.Fatalf("new service: %v", err)
	}
	repo.stored[serviceCacheKeyForTest(1, domain.ScopeGroup, 42)] = domain.Policy{
		Enabled: true, ReplyMode: domain.ModeMentionOnly,
	}

	policy, err := svc.Resolve(context.Background(), 1, domain.ScopeGroup, 42)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if policy.ScopeType != domain.ScopeGroup || policy.ScopeID != 42 {
		t.Fatalf("identity = %q/%d, want group/42", policy.ScopeType, policy.ScopeID)
	}
}

// TestUpsertTakesEffectImmediately verifies that after Upsert, Resolve returns
// the new policy even when the repository no longer has it (cache hit).
// TestUpsertTakesEffectImmediately 验证 Upsert 后即使仓库已无该行，
// Resolve 仍返回新策略（命中缓存）。
func TestUpsertTakesEffectImmediately(t *testing.T) {
	repo := newFakeRepo()
	svc, err := NewService(repo)
	if err != nil {
		t.Fatalf("new service: %v", err)
	}
	ctx := context.Background()
	// Warm the cache with the disabled default first.
	// 先用关闭的默认策略预热缓存。
	if _, err := svc.Resolve(ctx, 1, domain.ScopeGroup, 42); err != nil {
		t.Fatalf("warm resolve: %v", err)
	}
	err = svc.Upsert(ctx, 1, domain.Policy{
		ScopeType: domain.ScopeGroup, ScopeID: 42,
		Enabled: true, ReplyMode: domain.ModeIntentBased, AllowHistoryRead: true,
	})
	if err != nil {
		t.Fatalf("upsert: %v", err)
	}
	delete(repo.stored, serviceCacheKeyForTest(1, domain.ScopeGroup, 42))
	policy, err := svc.Resolve(ctx, 1, domain.ScopeGroup, 42)
	if err != nil {
		t.Fatalf("resolve after upsert: %v", err)
	}
	if !policy.Enabled || policy.ReplyMode != domain.ModeIntentBased {
		t.Fatalf("stale policy returned: enabled=%v mode=%q", policy.Enabled, policy.ReplyMode)
	}
}

// TestUpsertRejectsInvalidPolicy verifies validation errors from Upsert.
// TestUpsertRejectsInvalidPolicy 验证 Upsert 的校验错误。
func TestUpsertRejectsInvalidPolicy(t *testing.T) {
	svc, err := NewService(newFakeRepo())
	if err != nil {
		t.Fatalf("new service: %v", err)
	}
	err = svc.Upsert(context.Background(), 1, domain.Policy{
		ScopeType: "channel", ScopeID: 42, ReplyMode: domain.ModeMentionOnly,
	})
	if err == nil {
		t.Fatal("expected validation error for invalid scope type")
	}
}

// TestCacheExpiryPicksUpOutOfBandChanges verifies that cached entries —
// including cached default-deny results — expire so database changes made
// outside the service become effective without a restart.
// TestCacheExpiryPicksUpOutOfBandChanges 验证缓存条目（包括默认拒绝结果）
// 会过期，使绕过本服务的数据库变更无需重启即生效。
func TestCacheExpiryPicksUpOutOfBandChanges(t *testing.T) {
	oldTTL := cacheTTL
	cacheTTL = 20 * time.Millisecond
	defer func() { cacheTTL = oldTTL }()

	repo := newFakeRepo()
	svc, err := NewService(repo)
	if err != nil {
		t.Fatalf("new service: %v", err)
	}
	ctx := context.Background()

	// Warm the cache with the default-deny result.
	// 用默认拒绝结果预热缓存。
	if p, err := svc.Resolve(ctx, 1, domain.ScopeGroup, 42); err != nil || p.Enabled {
		t.Fatalf("warm resolve: policy=%+v err=%v", p, err)
	}
	// Out-of-band database change while the cache holds the deny result.
	// 缓存持有拒绝结果期间的库外变更。
	repo.stored[serviceCacheKeyForTest(1, domain.ScopeGroup, 42)] = domain.Policy{
		ScopeType: domain.ScopeGroup, ScopeID: 42, Enabled: true, ReplyMode: domain.ModeIntentBased,
	}
	time.Sleep(2 * cacheTTL)
	policy, err := svc.Resolve(ctx, 1, domain.ScopeGroup, 42)
	if err != nil {
		t.Fatalf("resolve after expiry: %v", err)
	}
	if !policy.Enabled {
		t.Fatal("expired cache must reload the changed policy from the repository")
	}
}

// TestDeleteEvictsCacheAndFallsBackToDeny verifies a deleted policy takes effect
// immediately, without waiting for the 30s cache TTL.
// TestDeleteEvictsCacheAndFallsBackToDeny 验证删除立即生效，无需等待 30s 缓存过期。
func TestDeleteEvictsCacheAndFallsBackToDeny(t *testing.T) {
	repo := newFakeRepo()
	svc, err := NewService(repo)
	if err != nil {
		t.Fatalf("new service: %v", err)
	}
	ctx := context.Background()

	policy := domain.Default(domain.ScopeGroup, 42)
	policy.Enabled = true
	policy.StoreMessages = true
	if err := svc.Upsert(ctx, 1, policy); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	// Resolve once so the enabled policy is cached.
	// 先解析一次，把启用策略写入缓存。
	if cached, err := svc.Resolve(ctx, 1, domain.ScopeGroup, 42); err != nil || !cached.Enabled {
		t.Fatalf("expected cached enabled policy: %+v err=%v", cached, err)
	}

	deleted, err := svc.Delete(ctx, 1, domain.ScopeGroup, 42)
	if err != nil || !deleted {
		t.Fatalf("delete: deleted=%v err=%v", deleted, err)
	}
	if _, found, _ := repo.Get(ctx, 1, domain.ScopeGroup, 42); found {
		t.Fatal("stored row should be gone")
	}

	after, err := svc.Resolve(ctx, 1, domain.ScopeGroup, 42)
	if err != nil {
		t.Fatalf("resolve after delete: %v", err)
	}
	if after.Enabled || after.StoreMessages {
		t.Fatalf("expected default deny after delete: %+v", after)
	}
}
