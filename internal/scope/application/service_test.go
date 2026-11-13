// Tests for scope policy resolution with default-deny and write-through cache.
// 作用域策略解析的测试：默认拒绝与写穿缓存。
package application

import (
	"context"
	"errors"
	"fmt"
	"testing"

	models "github.com/HappyLadySauceM/Nagi/internal/models/contracts"
	"github.com/HappyLadySauceM/Nagi/internal/scope/domain"
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

func (f *fakeRepo) ResolveMemoryAuthorization(_ context.Context, botID int64, t domain.ScopeType, id int64) (int64, bool, error) {
	p := f.stored[f.key(botID, t, id)]
	return 0, p.Enabled && p.StoreMessages && p.AllowMemoryWrite, nil
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

// TestUpsertTakesEffectImmediately verifies that a saved policy replaces the default.
// TestUpsertTakesEffectImmediately 验证已保存策略立即替代默认值。
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
func TestDeleteEvictsCacheAndFallsBackToDefault(t *testing.T) {
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
	if after.Enabled {
		t.Fatalf("expected participation off after delete: %+v", after)
	}
	if !after.StoreMessages {
		t.Fatalf("storage is a baseline and must stay on: %+v", after)
	}
}

func TestDefaultTemplatesAreIsolatedAndIndependentPoliciesWin(t *testing.T) {
	repo := newFakeRepo()
	svc, err := NewService(repo)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	template := domain.Default(domain.ScopePrivate, 0)
	template.Enabled = true
	template.AllowMemoryRead = true
	template.Version = 3
	if err := svc.Upsert(ctx, 1, template); err != nil {
		t.Fatal(err)
	}
	inherited, err := svc.Resolve(ctx, 1, domain.ScopePrivate, 100)
	if err != nil || !inherited.Enabled || !inherited.AllowMemoryRead || inherited.Source != "default" || inherited.TemplateVersion != 3 || inherited.ScopeID != 100 {
		t.Fatalf("inheritance: %+v, %v", inherited, err)
	}
	for _, target := range []struct {
		bot  int64
		kind domain.ScopeType
	}{{2, domain.ScopePrivate}, {1, domain.ScopeGroup}} {
		p, err := svc.Resolve(ctx, target.bot, target.kind, 100)
		if err != nil || p.Enabled {
			t.Fatalf("template leaked: %+v, %v", p, err)
		}
	}
	closed := domain.Default(domain.ScopePrivate, 100)
	closed.AllowMemoryRead = false
	if err := svc.Upsert(ctx, 1, closed); err != nil {
		t.Fatal(err)
	}
	p, err := svc.Resolve(ctx, 1, domain.ScopePrivate, 100)
	if err != nil || p.Enabled || p.AllowMemoryRead || p.Source != "independent" {
		t.Fatalf("explicit close: %+v, %v", p, err)
	}
	if _, err := svc.Delete(ctx, 1, domain.ScopePrivate, 100); err != nil {
		t.Fatal(err)
	}
	p, err = svc.Resolve(ctx, 1, domain.ScopePrivate, 100)
	if err != nil || !p.Enabled || !p.AllowMemoryRead || p.Source != "default" {
		t.Fatalf("restore: %+v, %v", p, err)
	}
	template.Enabled = false
	if err := svc.Upsert(ctx, 1, template); err != nil {
		t.Fatal(err)
	}
	p, err = svc.Resolve(ctx, 1, domain.ScopePrivate, 100)
	if err != nil || p.Enabled {
		t.Fatalf("template update stale: %+v, %v", p, err)
	}
}

// TestUpsertEnabledScopeRequiresGenerationModel verifies the existing
// generation-model readiness gate still applies when a scope is enabled, while
// the default-granted ASR permission does not require an ASR model.
// TestUpsertEnabledScopeRequiresGenerationModel 验证启用作用域仍要求生成模型可用，
// 而默认授予的 ASR 权限不要求 ASR 模型。
func TestUpsertEnabledScopeRequiresGenerationModel(t *testing.T) {
	repo := newFakeRepo()
	svc, err := NewService(repo)
	if err != nil {
		t.Fatal(err)
	}
	allowed := map[string]bool{}
	svc.SetModels(gateModels{allowed: allowed})
	svc.SetPermissionResolver(func(overrides map[string]bool) PermissionState {
		defaults := map[string]bool{"media.voice.transcribe": true, "media.voice.send": false}
		effective := clonePermissions(defaults)
		for key, value := range overrides {
			effective[key] = value
		}
		return PermissionState{Defaults: defaults, Effective: effective}
	})
	policy := domain.Default(domain.ScopePrivate, 123)
	policy.Enabled = true
	if err := svc.Upsert(context.Background(), 1, policy); err == nil {
		t.Fatal("enabled scope without generation model must fail")
	}
	allowed["generation"] = true
	if err := svc.Upsert(context.Background(), 1, policy); err != nil {
		t.Fatalf("default-granted ASR must not block enable: %v", err)
	}
	got, err := svc.Resolve(context.Background(), 1, domain.ScopePrivate, 123)
	if err != nil || !got.Enabled {
		t.Fatalf("stored policy = %+v, err=%v", got, err)
	}
	if got.ToolPermissions["media.voice.transcribe"] {
		t.Fatalf("new scope must not persist a default ASR override: %+v", got.ToolPermissions)
	}
	if !got.PermissionGranted("media.voice.transcribe") {
		t.Fatalf("default ASR grant missing: %+v", got)
	}
}

// TestUpsertDisabledThenEnabled verifies both create paths work without an
// intermediate database write.
// TestUpsertDisabledThenEnabled 验证创建后直接启用与先关闭再启用两条路径都可用。
func TestUpsertDisabledThenEnabled(t *testing.T) {
	repo := newFakeRepo()
	svc, err := NewService(repo)
	if err != nil {
		t.Fatal(err)
	}
	allowed := map[string]bool{}
	svc.SetModels(gateModels{allowed: allowed})
	policy := domain.Default(domain.ScopePrivate, 124)
	if err := svc.Upsert(context.Background(), 1, policy); err != nil {
		t.Fatalf("disabled upsert: %v", err)
	}
	allowed["generation"] = true
	policy.Enabled = true
	if err := svc.Upsert(context.Background(), 1, policy); err != nil {
		t.Fatalf("disabled->enabled upsert: %v", err)
	}
	got, err := svc.Resolve(context.Background(), 1, domain.ScopePrivate, 124)
	if err != nil || !got.Enabled {
		t.Fatalf("stored policy = %+v, err=%v", got, err)
	}
}

// TestUpsertPersistsPermissionOverrides verifies explicit capability grants
// remain data and are not replaced by model-readiness checks.
// TestUpsertPersistsPermissionOverrides 验证显式能力授权仍作为数据保存，不会被模型就绪检查替换。
func TestUpsertPersistsPermissionOverrides(t *testing.T) {
	repo := newFakeRepo()
	svc, err := NewService(repo)
	if err != nil {
		t.Fatal(err)
	}
	svc.SetModels(gateModels{allowed: map[string]bool{"generation": true}})
	svc.SetPermissionResolver(func(overrides map[string]bool) PermissionState {
		defaults := map[string]bool{"media.voice.transcribe": true, "media.voice.send": false}
		effective := clonePermissions(defaults)
		for key, value := range overrides {
			effective[key] = value
		}
		return PermissionState{Defaults: defaults, Effective: effective}
	})
	policy := domain.Default(domain.ScopePrivate, 456)
	policy.Enabled = true
	if err := svc.Upsert(context.Background(), 1, policy); err != nil {
		t.Fatalf("default-granted ASR must not block enable: %v", err)
	}
	got, err := svc.Resolve(context.Background(), 1, domain.ScopePrivate, 456)
	if err != nil {
		t.Fatal(err)
	}
	if got.ToolPermissions["media.voice.transcribe"] {
		t.Fatalf("stored overrides unexpectedly contain ASR: %+v", got.ToolPermissions)
	}
	if !got.PermissionDefaults["media.voice.transcribe"] || !got.PermissionGranted("media.voice.transcribe") {
		t.Fatalf("effective ASR grant missing: %+v", got)
	}
	policy.ToolPermissions = map[string]bool{"media.voice.transcribe": false}
	if err := svc.Upsert(context.Background(), 1, policy); err != nil {
		t.Fatalf("explicit deny: %v", err)
	}
	got, err = svc.Resolve(context.Background(), 1, domain.ScopePrivate, 456)
	if err != nil || got.PermissionGranted("media.voice.transcribe") {
		t.Fatalf("explicit deny not effective: %+v, %v", got, err)
	}
}

type gateModels struct{ allowed map[string]bool }

func (m gateModels) ResolveModel(_ context.Context, usage string, _ int64, _ string, _ int64) (models.Resolved, error) {
	if !m.allowed[usage] {
		return models.Resolved{}, fmt.Errorf("not verified")
	}
	return models.Resolved{}, nil
}
func (gateModels) BilibiliCredential(context.Context) (string, error) { return "", nil }

// TestUpsertExplicitVoiceGrantRequiresModel verifies explicit ASR/TTS grants
// still require their model while default-granted voice does not block enable.
// TestUpsertExplicitVoiceGrantRequiresModel 验证显式 ASR/TTS 授权仍要求对应模型，
// 而默认授予的语音能力不阻塞启用。
func TestUpsertExplicitVoiceGrantRequiresModel(t *testing.T) {
	repo := newFakeRepo()
	svc, err := NewService(repo)
	if err != nil {
		t.Fatal(err)
	}
	allowed := map[string]bool{"generation": true}
	svc.SetModels(gateModels{allowed})
	policy := domain.Default(domain.ScopePrivate, 789)
	policy.Enabled = true
	if err := svc.Upsert(context.Background(), 1, policy); err != nil {
		t.Fatalf("default-granted voice must not block enable: %v", err)
	}
	policy.ToolPermissions = map[string]bool{"media.voice.transcribe": true}
	if err := svc.Upsert(context.Background(), 1, policy); err == nil {
		t.Fatal("explicit ASR grant without model must fail")
	}
	allowed["asr"] = true
	if err := svc.Upsert(context.Background(), 1, policy); err != nil {
		t.Fatalf("explicit ASR with model: %v", err)
	}
	policy.ToolPermissions = map[string]bool{"media.voice.send": true}
	if err := svc.Upsert(context.Background(), 1, policy); err == nil {
		t.Fatal("explicit TTS grant without model must fail")
	}
	allowed["tts"] = true
	if err := svc.Upsert(context.Background(), 1, policy); err != nil {
		t.Fatalf("explicit TTS with model: %v", err)
	}
}

// TestUpsertExistingExplicitVoiceGrantDoesNotBlockUpdates verifies an existing
// explicit grant stays editable and disableable when its model later becomes
// unavailable; only a new grant or a re-enable requires the model again.
// TestUpsertExistingExplicitVoiceGrantDoesNotBlockUpdates 验证已有显式授权在模型失效后
// 仍可编辑和关闭；只有新增授权或重新启用才再次要求模型。
func TestUpsertExistingExplicitVoiceGrantDoesNotBlockUpdates(t *testing.T) {
	repo := newFakeRepo()
	svc, err := NewService(repo)
	if err != nil {
		t.Fatal(err)
	}
	allowed := map[string]bool{"generation": true, "asr": true}
	svc.SetModels(gateModels{allowed})
	policy := domain.Default(domain.ScopePrivate, 790)
	policy.Enabled = true
	policy.ToolPermissions = map[string]bool{"media.voice.transcribe": true}
	if err := svc.Upsert(context.Background(), 1, policy); err != nil {
		t.Fatalf("initial explicit ASR grant: %v", err)
	}
	delete(allowed, "asr")
	policy.MessageIntervalMS = 800
	if err := svc.Upsert(context.Background(), 1, policy); err != nil {
		t.Fatalf("unrelated update must not require ASR model: %v", err)
	}
	policy.Enabled = false
	if err := svc.Upsert(context.Background(), 1, policy); err != nil {
		t.Fatalf("disable must not require ASR model: %v", err)
	}
	policy.Enabled = true
	if err := svc.Upsert(context.Background(), 1, policy); err == nil {
		t.Fatal("re-enable with explicit ASR grant and no model must fail")
	}
	policy.Enabled = false
	policy.ToolPermissions = map[string]bool{"media.voice.transcribe": false}
	if err := svc.Upsert(context.Background(), 1, policy); err != nil {
		t.Fatalf("revoke explicit ASR while model unavailable: %v", err)
	}
}

// TestUpsertRequiresBotGroupRoleCanonicalizesLegacyAlias verifies the save guard
// reads the unified permission and rejects member/unknown roles while allowing
// admin and owner; the stored override is rewritten to the canonical key.
// TestUpsertRequiresBotGroupRoleCanonicalizesLegacyAlias 验证保存守卫按统一权限
// 判断，member/未知角色被拒，admin/owner 通过，旧键落库时改写为新键。
func TestUpsertRequiresBotGroupRoleCanonicalizesLegacyAlias(t *testing.T) {
	repo := newFakeRepo()
	svc, err := NewService(repo)
	if err != nil {
		t.Fatal(err)
	}
	svc.SetGroupRoleRequirement(func(permission string) string {
		if permission == "qq.group.admin.kick" {
			return "admin"
		}
		return ""
	})
	role := "member"
	svc.SetGroupRoleResolver(func(context.Context, int64, int64) (string, error) { return role, nil })
	policy := domain.Default(domain.ScopeGroup, 700)
	policy.ToolPermissions = map[string]bool{"qq.api.set_group_kick": true}

	if err := svc.Upsert(context.Background(), 1, policy); !errors.Is(err, domain.ErrBotNotGroupAdmin) {
		t.Fatalf("member role error = %v, want bot_not_group_admin", err)
	}
	role = "admin"
	if err := svc.Upsert(context.Background(), 1, policy); err != nil {
		t.Fatalf("admin role must pass: %v", err)
	}
	stored, found, err := repo.Get(context.Background(), 1, domain.ScopeGroup, 700)
	if err != nil || !found {
		t.Fatalf("stored = %+v found=%v err=%v", stored, found, err)
	}
	if !stored.ToolPermissions["qq.group.admin.kick"] || stored.ToolPermissions["qq.api.set_group_kick"] {
		t.Fatalf("legacy override not canonicalized: %+v", stored.ToolPermissions)
	}
	emptyRolePolicy := domain.Default(domain.ScopeGroup, 704)
	emptyRolePolicy.ToolPermissions = map[string]bool{"qq.group.admin.kick": true}
	role = ""
	if err := svc.Upsert(context.Background(), 1, emptyRolePolicy); !errors.Is(err, domain.ErrBotNotGroupAdmin) {
		t.Fatalf("empty role error = %v, want bot_not_group_admin", err)
	}
	role = "owner"
	if err := svc.Upsert(context.Background(), 1, emptyRolePolicy); err != nil {
		t.Fatalf("owner role must pass: %v", err)
	}
}

// TestResolveCanonicalizesLegacyPermissionOverrides verifies stored rows from
// before the dedupe still expose the unified permission to callers.
// TestResolveCanonicalizesLegacyPermissionOverrides 验证去重前的旧行仍向调用方
// 暴露统一权限。
func TestResolveCanonicalizesLegacyPermissionOverrides(t *testing.T) {
	repo := newFakeRepo()
	svc, err := NewService(repo)
	if err != nil {
		t.Fatal(err)
	}
	repo.stored[serviceCacheKeyForTest(1, domain.ScopeGroup, 701)] = domain.Policy{
		ScopeType: domain.ScopeGroup, ScopeID: 701, Enabled: true, ReplyMode: domain.ModeMentionOnly,
		ToolPermissions: map[string]bool{"qq.api.set_group_ban": true},
	}
	got, err := svc.Resolve(context.Background(), 1, domain.ScopeGroup, 701)
	if err != nil {
		t.Fatal(err)
	}
	if !got.ToolPermissions["qq.group.admin.mute"] || got.ToolPermissions["qq.api.set_group_ban"] {
		t.Fatalf("stored legacy override not canonicalized: %+v", got.ToolPermissions)
	}
}

// TestUpsertExistingAdminGrantDoesNotBlockUpdates verifies the save guard only
// requires a role for a new grant or an enabling transition; an existing grant
// must stay editable and disableable after the bot is demoted.
// TestUpsertExistingAdminGrantDoesNotBlockUpdates 验证保存守卫只对新增授权或启用转换
// 要求角色；机器人被降权后，已有授权仍可编辑和关闭。
func TestUpsertExistingAdminGrantDoesNotBlockUpdates(t *testing.T) {
	repo := newFakeRepo()
	svc, err := NewService(repo)
	if err != nil {
		t.Fatal(err)
	}
	svc.SetGroupRoleRequirement(func(permission string) string {
		if permission == "qq.group.admin.kick" {
			return "admin"
		}
		return ""
	})
	role := "admin"
	svc.SetGroupRoleResolver(func(context.Context, int64, int64) (string, error) { return role, nil })
	policy := domain.Default(domain.ScopeGroup, 702)
	policy.Enabled = true
	policy.ToolPermissions = map[string]bool{"qq.group.admin.kick": true}
	if err := svc.Upsert(context.Background(), 1, policy); err != nil {
		t.Fatalf("initial admin grant: %v", err)
	}
	role = "member"
	policy.ReplyMode = domain.ModeAlwaysReply
	if err := svc.Upsert(context.Background(), 1, policy); err != nil {
		t.Fatalf("unrelated update must not require role: %v", err)
	}
	policy.Enabled = false
	if err := svc.Upsert(context.Background(), 1, policy); err != nil {
		t.Fatalf("disable must not require role: %v", err)
	}
	policy.Enabled = true
	if err := svc.Upsert(context.Background(), 1, policy); !errors.Is(err, domain.ErrBotNotGroupAdmin) {
		t.Fatalf("re-enable error = %v, want bot_not_group_admin", err)
	}
	policy.Enabled = false
	policy.ToolPermissions = map[string]bool{"qq.group.admin.kick": false}
	if err := svc.Upsert(context.Background(), 1, policy); err != nil {
		t.Fatalf("revoke must not require role: %v", err)
	}
}

// TestUpsertGroupRoleUnavailableIsDistinct verifies a role lookup failure is
// not collapsed into the confirmed non-admin sentinel.
// TestUpsertGroupRoleUnavailableIsDistinct 验证角色查询失败不会被折叠成已确认的非管理员错误。
func TestUpsertGroupRoleUnavailableIsDistinct(t *testing.T) {
	repo := newFakeRepo()
	svc, err := NewService(repo)
	if err != nil {
		t.Fatal(err)
	}
	svc.SetGroupRoleRequirement(func(permission string) string {
		if permission == "qq.group.admin.kick" {
			return "admin"
		}
		return ""
	})
	svc.SetGroupRoleResolver(func(context.Context, int64, int64) (string, error) {
		return "", errors.New("offline")
	})
	policy := domain.Default(domain.ScopeGroup, 703)
	policy.ToolPermissions = map[string]bool{"qq.group.admin.kick": true}
	if err := svc.Upsert(context.Background(), 1, policy); !errors.Is(err, domain.ErrBotGroupRoleUnavailable) {
		t.Fatalf("error = %v, want bot_group_role_unavailable", err)
	}
}
