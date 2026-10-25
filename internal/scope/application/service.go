// Package application implements scope policy resolution and management use cases.
// scope/application 实现作用域策略解析与管理用例。
package application

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/HappyLadySauceM/QQBot/internal/scope/domain"
)

// Repository persists scope policies; implemented by infrastructure.
// Repository 持久化作用域策略；由 infrastructure 实现。
type Repository interface {
	Get(ctx context.Context, botID int64, scopeType domain.ScopeType, scopeID int64) (domain.Policy, bool, error)
	List(ctx context.Context, botID *int64) ([]domain.Policy, error)
	Upsert(ctx context.Context, botID int64, policy domain.Policy) error
	// Delete removes one stored policy and reports whether a row existed.
	// Delete 删除一条已存策略并报告是否存在该行。
	Delete(ctx context.Context, botID int64, scopeType domain.ScopeType, scopeID int64) (bool, error)
}

// cacheTTL bounds how long a resolved policy — including the default-deny
// result of unknown scopes — is served from memory. Expiry keeps out-of-band
// database changes effective without a restart. Overridable in tests.
// cacheTTL 限制解析结果（包括未知作用域的默认拒绝结果）在内存中的存留时间。
// 过期后重新读库，保证绕过管理接口的数据库变更无需重启即生效。测试可覆盖。
var cacheTTL = 30 * time.Second

// cacheEntry pairs a policy with its expiry.
// cacheEntry 将策略与其过期时间配对。
type cacheEntry struct {
	policy    domain.Policy
	expiresAt time.Time
}

// Service resolves effective policies with a write-through cache, so management
// updates take effect immediately for later resolutions.
// Service 通过写穿缓存解析生效策略，管理端更新对后续解析立即生效。
type Service struct {
	repo Repository

	mu    sync.RWMutex
	cache map[string]cacheEntry
}

// NewService validates dependencies and builds the service.
// NewService 校验依赖并构建服务。
func NewService(repo Repository) (*Service, error) {
	if repo == nil {
		return nil, fmt.Errorf("scope: repository required")
	}
	return &Service{repo: repo, cache: make(map[string]cacheEntry)}, nil
}

// Resolve returns the effective policy of one scope. Unknown or disabled scopes
// resolve to the safe default; lower layers can only narrow, never widen, permissions.
// Resolve 返回单个作用域的生效策略。未知或未配置的作用域解析为安全默认；
// 下层只能收紧权限，不能放大上层禁令。
func (s *Service) Resolve(ctx context.Context, botID int64, scopeType domain.ScopeType, scopeID int64) (domain.Policy, error) {
	key := cacheKey(botID, scopeType, scopeID)
	s.mu.RLock()
	cached, ok := s.cache[key]
	s.mu.RUnlock()
	if ok && time.Now().Before(cached.expiresAt) {
		return cached.policy, nil
	}
	stored, found, err := s.repo.Get(ctx, botID, scopeType, scopeID)
	if err != nil {
		return domain.Policy{}, err
	}
	if !found {
		stored = domain.Default(scopeType, scopeID)
	}
	effective := stored.WithDefaults()
	// Stored rows may come from a scan that omits the identity columns; the
	// resolved policy is always keyed by the requested scope.
	// 已存行可能来自不扫描身份列的查询；生效策略始终以请求的作用域为标识。
	effective.ScopeType = scopeType
	effective.ScopeID = scopeID
	s.mu.Lock()
	s.cache[key] = cacheEntry{policy: effective, expiresAt: time.Now().Add(cacheTTL)}
	s.mu.Unlock()
	return effective, nil
}

// List returns all stored policies, optionally filtered by bot.
// List 返回全部已存储策略，可按机器人过滤。
func (s *Service) List(ctx context.Context, botID *int64) ([]domain.Policy, error) {
	return s.repo.List(ctx, botID)
}

// Get returns the effective policy of one scope; it is an alias of Resolve.
// Get 返回单个作用域的生效策略；是 Resolve 的别名。
func (s *Service) Get(ctx context.Context, botID int64, scopeType domain.ScopeType, scopeID int64) (domain.Policy, error) {
	return s.Resolve(ctx, botID, scopeType, scopeID)
}

// Upsert validates and stores one policy, then refreshes the cache entry.
// Upsert 校验并保存一条策略，然后刷新缓存。
func (s *Service) Upsert(ctx context.Context, botID int64, policy domain.Policy) error {
	if err := policy.Validate(); err != nil {
		return err
	}
	policy = policy.WithDefaults()
	if err := s.repo.Upsert(ctx, botID, policy); err != nil {
		return err
	}
	s.mu.Lock()
	s.cache[cacheKey(botID, policy.ScopeType, policy.ScopeID)] = cacheEntry{
		policy: policy, expiresAt: time.Now().Add(cacheTTL),
	}
	s.mu.Unlock()
	return nil
}

// Delete removes one stored policy and evicts its cache entry, so the scope
// falls back to the default-deny policy immediately instead of after the TTL.
// Delete 删除一条已存策略并驱逐缓存，使该作用域立即回到默认拒绝，而不是等缓存过期。
func (s *Service) Delete(ctx context.Context, botID int64, scopeType domain.ScopeType, scopeID int64) (bool, error) {
	if !scopeType.Valid() || scopeID <= 0 || botID <= 0 {
		return false, fmt.Errorf("scope: invalid scope identity")
	}
	deleted, err := s.repo.Delete(ctx, botID, scopeType, scopeID)
	if err != nil {
		return false, err
	}
	s.mu.Lock()
	delete(s.cache, cacheKey(botID, scopeType, scopeID))
	s.mu.Unlock()
	return deleted, nil
}

func cacheKey(botID int64, scopeType domain.ScopeType, scopeID int64) string {
	return fmt.Sprintf("%d:%s:%d", botID, scopeType, scopeID)
}
