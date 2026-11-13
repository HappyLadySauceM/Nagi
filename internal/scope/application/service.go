// Package application implements scope policy resolution and management use cases.
// scope/application 实现作用域策略解析与管理用例。
package application

import (
	"context"
	"fmt"

	models "github.com/HappyLadySauceM/Nagi/internal/models/contracts"
	pluginscontracts "github.com/HappyLadySauceM/Nagi/internal/plugins/contracts"
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
	repo                 Repository
	models               models.Resolver
	permissions          PermissionResolver
	groupRoles           GroupRoleResolver
	groupRoleRequirement GroupRoleRequirement
}

// GroupRoleResolver reads the bot's role in one group. It is bound in the
// composition root so scope does not depend on directory infrastructure.
// GroupRoleResolver 读取机器人在一个群内的角色；由组合根绑定，
// 使 scope 不依赖 directory 基础设施。
type GroupRoleResolver func(ctx context.Context, botID, groupID int64) (string, error)

// GroupRoleRequirement returns the minimum group role required by one
// permission, or empty when the permission has no group-role prerequisite.
// GroupRoleRequirement 返回某权限要求的最低群角色；无前置要求时返回空。
type GroupRoleRequirement func(permission string) string

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

// SetModels binds model availability checks used when a scope is activated or
// an explicit voice capability is granted.
// SetModels 绑定作用域启用与显式语音授权使用的模型可用性检查。
func (s *Service) SetModels(resolver models.Resolver) { s.models = resolver }

// SetPermissionResolver binds the shared runtime permission resolution rule.
// SetPermissionResolver 绑定运行时共用的权限解析规则。
func (s *Service) SetPermissionResolver(resolver PermissionResolver) { s.permissions = resolver }

// SetGroupRoleResolver binds the bot group-role lookup used by save-time checks.
// SetGroupRoleResolver 绑定保存时校验使用的机器人群角色查询。
func (s *Service) SetGroupRoleResolver(resolver GroupRoleResolver) { s.groupRoles = resolver }

// SetGroupRoleRequirement binds the permission-to-role metadata used by
// save-time checks.
// SetGroupRoleRequirement 绑定保存时校验使用的权限到群角色元数据。
func (s *Service) SetGroupRoleRequirement(requirement GroupRoleRequirement) {
	s.groupRoleRequirement = requirement
}

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
	effective.ToolPermissions = pluginscontracts.NormalizePermissionOverrides(clonePermissions(effective.ToolPermissions))
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
		policies[index].ToolPermissions = pluginscontracts.NormalizePermissionOverrides(clonePermissions(policies[index].ToolPermissions))
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
	policy.ToolPermissions = pluginscontracts.NormalizePermissionOverrides(policy.ToolPermissions)
	needsPrevious := s.models != nil || (s.groupRoles != nil && s.groupRoleRequirement != nil)
	var previous domain.Policy
	if needsPrevious {
		var err error
		previous, err = s.Resolve(ctx, botID, policy.ScopeType, policy.ScopeID)
		if err != nil {
			return err
		}
	}
	if s.models != nil {
		policy.ToolPermissions = clonePermissions(policy.ToolPermissions)
		// Enabling a scope still requires a tested generation model because the
		// reply runtime cannot start without one. Saving a disabled scope or
		// editing an enabled one stays a policy decision.
		// 启用作用域仍要求生成模型已测试可用，因为回复运行时离开它无法启动；
		// 保存关闭策略或编辑已启用策略仍只属于策略决策。
		if policy.Enabled && !previous.Enabled {
			if _, resolveErr := s.models.ResolveModel(ctx, "generation", botID, string(policy.ScopeType), policy.ScopeID); resolveErr != nil {
				return fmt.Errorf("请先配置并测试生成模型")
			}
		}
		// Only an explicit voice capability grant requires its model. The
		// default transcription grant (fallback true) must not block creation or
		// editing, and an existing explicit grant must not block unrelated
		// updates or disabling the scope.
		// 只有显式语音能力授权才要求对应模型可用。默认转写授权（兜底 true）
		// 不得阻塞创建或编辑；已存在的显式授权也不得阻塞无关更新或关闭作用域。
		for permission, usage := range map[string]string{"media.voice.transcribe": "asr", "media.voice.send": "tts"} {
			explicit := policy.ToolPermissions[permission]
			newlyGranted := explicit && !previous.ToolPermissions[permission]
			activating := explicit && !previous.Enabled && policy.Enabled
			if newlyGranted || activating {
				if _, resolveErr := s.models.ResolveModel(ctx, usage, 0, "", 0); resolveErr != nil {
					return fmt.Errorf("请先配置并测试 %s 模型", usage)
				}
			}
		}
	}
	if err := policy.Validate(); err != nil {
		return err
	}
	if needsPrevious {
		if err := s.requireGroupRole(ctx, botID, policy, previous); err != nil {
			return err
		}
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
		p.ToolPermissions = pluginscontracts.NormalizePermissionOverrides(clonePermissions(p.ToolPermissions))
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

// requireGroupRole blocks granting group-admin capabilities when the bot is not
// an owner/admin in that group. A missing role row or member role fails closed.
// requireGroupRole 在机器人不是该群群主/管理员时阻止授予群管能力；
// 缺少角色行或角色为 member 时按拒绝处理。
func (s *Service) requireGroupRole(ctx context.Context, botID int64, policy, previous domain.Policy) error {
	if s.groupRoles == nil || s.groupRoleRequirement == nil {
		return nil
	}
	if policy.ScopeType != domain.ScopeGroup || policy.ScopeID <= 0 {
		return nil
	}
	activating := !previous.Enabled && policy.Enabled
	required := ""
	for permission, granted := range policy.ToolPermissions {
		if !granted {
			continue
		}
		// Only a newly granted capability, or one activated by enabling the
		// scope, needs a role check. Disabling or editing unrelated fields must
		// stay possible after the bot loses its admin role.
		// 只有新增授权或随启用作用域生效的能力才校验角色；
		// 撤权、关闭或修改无关字段不因机器人被降权而阻塞。
		if !activating && previous.ToolPermissions[permission] {
			continue
		}
		if candidate := s.groupRoleRequirement(permission); pluginscontracts.GroupRoleRank(candidate) > pluginscontracts.GroupRoleRank(required) {
			required = candidate
		}
	}
	if required == "" {
		return nil
	}
	role, err := s.groupRoles(ctx, botID, policy.ScopeID)
	if err != nil {
		return fmt.Errorf("%w: %v", domain.ErrBotGroupRoleUnavailable, err)
	}
	if !pluginscontracts.GroupRoleSatisfies(role, required) {
		return domain.ErrBotNotGroupAdmin
	}
	return nil
}
