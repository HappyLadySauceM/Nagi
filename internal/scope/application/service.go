// Package application implements scope policy resolution and management use cases.
// scope/application 实现作用域策略解析与管理用例。
package application

import (
	"context"
	"fmt"
	models "github.com/HappyLadySauceM/Nagi/internal/models/contracts"
	scopecontracts "github.com/HappyLadySauceM/Nagi/internal/scope/contracts"

	"github.com/HappyLadySauceM/Nagi/internal/scope/domain"
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

// Service resolves current policies from storage so revocations apply to subsequent calls.
// Service 从存储解析当前策略，使撤销对后续调用立即生效。
type Service struct {
	repo        Repository
	models      models.Resolver
	permissions PermissionResolver
}

// PermissionState separates persisted scope overrides from the live baseline
// and calculated result returned to callers.
// PermissionState 区分已持久化的作用域覆盖、实时默认基线和最终生效结果。
type PermissionState struct {
	Defaults  map[string]bool
	Effective map[string]bool
}

// PermissionResolver is bound in the composition root because the scope
// context must not depend on the plugin registry or settings implementation.
// PermissionResolver 在组合根绑定，避免 scope 上下文依赖插件注册表或配置实现。
type PermissionResolver func(overrides map[string]bool) PermissionState

// SetModels binds model availability checks at composition time.
// SetModels 在装配时绑定模型可用性校验。
func (s *Service) SetModels(resolver models.Resolver) { s.models = resolver }

// SetPermissionResolver binds the shared runtime permission resolution rule.
// SetPermissionResolver 绑定运行时共用的权限解析规则。
func (s *Service) SetPermissionResolver(resolver PermissionResolver) { s.permissions = resolver }

// NewService validates dependencies and builds the service.
// NewService 校验依赖并构建服务。
func NewService(repo Repository) (*Service, error) {
	if repo == nil {
		return nil, fmt.Errorf("scope: repository required")
	}
	return &Service{repo: repo}, nil
}

// Resolve returns the effective policy of one scope. Unknown or disabled scopes
// resolve to the safe default; lower layers can only narrow, never widen, permissions.
// Resolve 返回单个作用域的生效策略。未知或未配置的作用域解析为安全默认；
// 下层只能收紧权限，不能放大上层禁令。
func (s *Service) Resolve(ctx context.Context, botID int64, scopeType domain.ScopeType, scopeID int64) (domain.Policy, error) {
	stored, found, err := s.repo.Get(ctx, botID, scopeType, scopeID)
	if err != nil {
		return domain.Policy{}, err
	}
	source := "independent"
	if scopeID == 0 {
		source = "default"
	}
	if !found && scopeID != 0 {
		stored, found, err = s.repo.Get(ctx, botID, scopeType, 0)
		if err != nil {
			return domain.Policy{}, err
		}
		source = "default"
	}
	if !found {
		stored = domain.Default(scopeType, scopeID)
	}
	stored.Source = source
	if source == "default" {
		stored.TemplateVersion = stored.Version
	}
	effective := stored.WithDefaults()
	effective.ToolPermissions = clonePermissions(effective.ToolPermissions)
	if s.permissions != nil {
		state := s.permissions(effective.ToolPermissions)
		effective.PermissionDefaults = clonePermissions(state.Defaults)
		effective.EffectiveToolPermissions = clonePermissions(state.Effective)
	}
	// Stored rows may come from a scan that omits the identity columns; the
	// resolved policy is always keyed by the requested scope.
	// 已存行可能来自不扫描身份列的查询；生效策略始终以请求的作用域为标识。
	effective.ScopeType = scopeType
	effective.ScopeID = scopeID
	return effective, nil
}

// List returns all stored policies, optionally filtered by bot.
// List 返回全部已存储策略，可按机器人过滤。
func (s *Service) List(ctx context.Context, botID *int64) ([]domain.Policy, error) {
	policies, err := s.repo.List(ctx, botID)
	if err != nil {
		return nil, err
	}
	for index := range policies {
		policies[index].ToolPermissions = clonePermissions(policies[index].ToolPermissions)
		if s.permissions != nil {
			state := s.permissions(policies[index].ToolPermissions)
			policies[index].PermissionDefaults = clonePermissions(state.Defaults)
			policies[index].EffectiveToolPermissions = clonePermissions(state.Effective)
		}
	}
	return policies, nil
}

// Get returns the effective policy of one scope; it is an alias of Resolve.
// Get 返回单个作用域的生效策略；是 Resolve 的别名。
func (s *Service) Get(ctx context.Context, botID int64, scopeType domain.ScopeType, scopeID int64) (domain.Policy, error) {
	return s.Resolve(ctx, botID, scopeType, scopeID)
}

// MemoryAuthorizationRepository is an optional read port used by extraction
// and the management retry path.
// MemoryAuthorizationRepository 是提取与管理重试路径使用的可选读取端口。
type MemoryAuthorizationRepository interface {
	ResolveMemoryAuthorization(ctx context.Context, botID int64, scopeType domain.ScopeType, scopeID int64) (int64, bool, error)
}

// ResolveMemoryAuthorization returns the current memory authorization generation.
// ResolveMemoryAuthorization 返回当前记忆授权代际。
func (s *Service) ResolveMemoryAuthorization(ctx context.Context, botID int64, scopeType domain.ScopeType, scopeID int64) (scopecontracts.MemoryAuthorization, error) {
	repo, ok := s.repo.(MemoryAuthorizationRepository)
	if !ok {
		return scopecontracts.MemoryAuthorization{}, fmt.Errorf("scope: memory authorization repository unavailable")
	}
	eventID, authorized, err := repo.ResolveMemoryAuthorization(ctx, botID, scopeType, scopeID)
	if err != nil {
		return scopecontracts.MemoryAuthorization{}, err
	}
	return scopecontracts.MemoryAuthorization{EventID: eventID, Authorized: authorized}, nil
}

// Upsert validates and persists one complete policy.
// Upsert 校验并持久化一份完整策略。
func (s *Service) Upsert(ctx context.Context, botID int64, policy domain.Policy) error {
	if s.models != nil {
		previous, err := s.Resolve(ctx, botID, policy.ScopeType, policy.ScopeID)
		if err != nil {
			return err
		}
		policy.ToolPermissions = clonePermissions(policy.ToolPermissions)
		proposed := policy
		if s.permissions != nil {
			state := s.permissions(proposed.ToolPermissions)
			proposed.PermissionDefaults = clonePermissions(state.Defaults)
			proposed.EffectiveToolPermissions = clonePermissions(state.Effective)
		}
		if policy.Enabled && !previous.Enabled {
			if _, err = s.models.ResolveModel(ctx, "generation", botID, string(policy.ScopeType), policy.ScopeID); err != nil {
				return fmt.Errorf("请先配置并测试生成模型")
			}
		}
		for permission, usage := range map[string]string{"media.voice.transcribe": "asr", "media.voice.send": "tts"} {
			// Enabling a scope activates already-default-granted capabilities too.
			// Checking only the permission delta would let a default grant bypass
			// model readiness on the first activation of a conversation.
			// 启用作用域会激活默认已授予的能力；仅比较权限差异会让首次
			// 启用会话时绕过模型就绪校验。
			if proposed.PermissionGranted(permission) && (!previous.PermissionGranted(permission) || (!previous.Enabled && proposed.Enabled)) {
				if _, err = s.models.ResolveModel(ctx, usage, 0, "", 0); err != nil {
					return fmt.Errorf("请先配置并测试 %s 模型", usage)
				}
			}
		}
	}
	if err := policy.Validate(); err != nil {
		return err
	}
	policy = policy.WithDefaults()
	policy.Source = "independent"
	if policy.ScopeID == 0 {
		policy.Source = "default"
	}
	if err := s.repo.Upsert(ctx, botID, policy); err != nil {
		return err
	}
	return nil
}

// Delete removes an independent policy so subsequent reads use the default template.
// Delete 删除独立策略，使后续读取使用默认模板。
func (s *Service) Delete(ctx context.Context, botID int64, scopeType domain.ScopeType, scopeID int64) (bool, error) {
	if !scopeType.Valid() || scopeID <= 0 || botID <= 0 {
		return false, fmt.Errorf("scope: invalid scope identity")
	}
	deleted, err := s.repo.Delete(ctx, botID, scopeType, scopeID)
	if err != nil {
		return false, err
	}
	return deleted, nil
}

func cacheKey(botID int64, scopeType domain.ScopeType, scopeID int64) string {
	return fmt.Sprintf("%d:%s:%d", botID, scopeType, scopeID)
}

// ResolveMany resolves a directory page from one account-scoped policy snapshot.
// ResolveMany 从一份账号策略快照解析整页目录。
func (s *Service) ResolveMany(ctx context.Context, botID int64, kind domain.ScopeType, ids []int64) (map[int64]domain.Policy, error) {
	rows, err := s.repo.List(ctx, &botID)
	if err != nil {
		return nil, err
	}
	stored := make(map[int64]domain.Policy)
	for _, p := range rows {
		if p.ScopeType == kind {
			stored[p.ScopeID] = p
		}
	}
	template, ok := stored[0]
	if !ok {
		template = domain.Default(kind, 0)
	}
	result := make(map[int64]domain.Policy, len(ids))
	for _, id := range ids {
		p, independent := stored[id]
		if independent {
			p.Source = "independent"
		} else {
			p = template
			p.Source = "default"
			p.TemplateVersion = template.Version
		}
		p.ScopeType = kind
		p.ScopeID = id
		p = p.WithDefaults()
		p.ToolPermissions = clonePermissions(p.ToolPermissions)
		if s.permissions != nil {
			state := s.permissions(p.ToolPermissions)
			p.PermissionDefaults = clonePermissions(state.Defaults)
			p.EffectiveToolPermissions = clonePermissions(state.Effective)
		}
		result[id] = p
	}
	return result, nil
}

func clonePermissions(source map[string]bool) map[string]bool {
	if len(source) == 0 {
		return map[string]bool{}
	}
	copy := make(map[string]bool, len(source))
	for key, value := range source {
		copy[key] = value
	}
	return copy
}
