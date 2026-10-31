// Package tools serves the read-only tool catalog.
// Package tools 提供只读的工具清单接口。
package tools

import (
	"net/http"
	"sort"
	"strings"

	"github.com/HappyLadySauceM/Nagi/cmd/app/svc"
	"github.com/HappyLadySauceM/Nagi/cmd/app/types/common"
	pluginsapp "github.com/HappyLadySauceM/Nagi/internal/plugins/application"
	"github.com/HappyLadySauceM/Nagi/internal/plugins/contracts"
	settingsapp "github.com/HappyLadySauceM/Nagi/internal/settings/application"
	settingsdomain "github.com/HappyLadySauceM/Nagi/internal/settings/domain"
)

// unavailableScopeKindMismatch is the stable reason returned when a tool does
// not apply to the requested conversation kind.
// unavailableScopeKindMismatch 是工具不适用于请求会话类型时返回的稳定原因。
const unavailableScopeKindMismatch = "scope_kind_mismatch"

// riskRank orders risk levels so a permission group can keep the strictest one.
// riskRank 为风险级别排序，便于权限分组建模时保留最严格项。
func riskRank(level contracts.RiskLevel) int {
	switch level {
	case contracts.RiskAdmin:
		return 3
	case contracts.RiskWrite:
		return 2
	case contracts.RiskRead:
		return 1
	}
	return 0
}

// Handler serves the tool catalog endpoint.
// Handler 提供工具清单接口。
type Handler struct {
	svcCtx *svc.ServiceContext
}

// NewHandler builds the tools handler.
// NewHandler 构建工具清单处理器。
func NewHandler(svcCtx *svc.ServiceContext) *Handler {
	return &Handler{svcCtx: svcCtx}
}

// Register wires routes using Go 1.22 method+path patterns.
// Register 以 Go 1.22 的方法+路径模式注册路由。
func (h *Handler) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/tools", h.List)
}

// toolInfo describes one registered tool, including its default grant state
// and, when a scope kind was requested, whether it applies to that kind.
// toolInfo 描述单个已注册工具，包含其默认授予状态；
// 请求带会话类型时，还包含该工具是否适用于该类型。
type toolInfo struct {
	BlockedReasons []string
	ID             string
	Provider       string
	Description    string
	Category       string
	ScopeKinds     []string
	RiskLevel      contracts.RiskLevel
	ReadOnly       bool
	SideEffect     bool
	Permission     string
	// DefaultGranted mirrors the runtime resolution for this tool.
	// DefaultGranted 对应该工具的运行时授予结果。
	DefaultGranted bool
	// Applicable is true when the tool applies to the requested scope kind, and
	// always true for requests without scope_kind.
	// Applicable 表示工具是否适用于请求的会话类型；未指定会话类型时恒为 true。
	Applicable bool
	// UnavailableReason is empty when applicable, otherwise a stable
	// machine-readable code such as scope_kind_mismatch.
	// UnavailableReason 在适用时为空串，否则为 scope_kind_mismatch 等稳定机器码。
	UnavailableReason string
}

// permissionInfo groups the tools that require one permission.
// permissionInfo 归组需要同一权限的工具。
type permissionInfo struct {
	Description    string
	RiskLevel      contracts.RiskLevel
	ID             string
	Tools          []string
	DefaultGranted bool
}

// catalog is the response of GET /api/v1/tools.
// catalog 是 GET /api/v1/tools 的响应体。
type catalog struct {
	Providers   []string
	Tools       []toolInfo
	Permissions []permissionInfo
}

// scopeKindFilter validates the optional scope_kind query parameter; an empty
// value means "no filtering".
// scopeKindFilter 校验可选的 scope_kind 查询参数；空值表示不过滤。
func scopeKindFilter(raw string) (string, bool) {
	kind := strings.TrimSpace(raw)
	switch kind {
	case "":
		return "", true
	case contracts.ScopeKindPrivate, contracts.ScopeKindGroup:
		return kind, true
	default:
		return "", false
	}
}

// liveSettings returns the live snapshot, or the config-file baseline when the
// process runs without a database (tests, database-less wiring).
// liveSettings 返回实时快照；进程未配置数据库时（测试或无数据库装配）
// 回落到配置文件基线。
func (h *Handler) liveSettings() settingsdomain.Settings {
	if h.svcCtx.SettingsProvider != nil {
		return h.svcCtx.SettingsProvider.Settings()
	}
	if h.svcCtx.Config == nil {
		return settingsdomain.Settings{}
	}
	return settingsapp.FromConfig(h.svcCtx.Config)
}

// List returns the tools that were actually registered at startup. Default
// grants mirror runtime resolution, so the UI never needs to duplicate rules.
// List 返回启动时真正注册成功的工具。默认授予与运行时解析一致，
// 界面因此无需重复实现规则。
func (h *Handler) List(w http.ResponseWriter, r *http.Request) {
	scopeKind, ok := scopeKindFilter(r.URL.Query().Get("scope_kind"))
	if !ok {
		common.WriteFailure(w, http.StatusBadRequest, "unsupported scope_kind; use private or group")
		return
	}
	registry := h.svcCtx.Tools
	if registry == nil {
		// Without AI generation no tool is reachable; an empty catalog would
		// look like "everything is revoked".
		// 未启用 AI 生成时没有任何工具可用；返回空清单会看起来像"全部已撤销"。
		common.WriteFailure(w, http.StatusServiceUnavailable, "tools are not enabled")
		return
	}
	// Every registered provider is available; the catalogue only mirrors the
	// permission baseline the runtime uses.
	// 每个已注册提供方都可用；清单只镜像运行时使用的权限基线。
	visible := registry.Snapshot()
	live := h.liveSettings()
	granted := pluginsapp.ResolvePermissions(visible, live.Tools.PermissionDefaults, nil)

	out := catalog{
		Providers:   make([]string, 0),
		Tools:       make([]toolInfo, 0, len(visible)),
		Permissions: make([]permissionInfo, 0),
	}
	seenProviders := make(map[string]bool, len(visible))
	for _, registered := range visible {
		if seenProviders[registered.ProviderID] {
			continue
		}
		seenProviders[registered.ProviderID] = true
		out.Providers = append(out.Providers, registered.ProviderID)
	}
	sort.Strings(out.Providers)

	byPermission := make(map[string]*permissionInfo)
	for _, registered := range visible {
		descriptor := registered.Descriptor
		applicable := true
		unavailableReason := ""
		if scopeKind != "" {
			applicable = descriptor.AppliesTo(scopeKind)
			if !applicable {
				unavailableReason = unavailableScopeKindMismatch
			}
		}
		// Only pipeline state can block a capability now; whether a write tool
		// may run is decided by the conversation's own grant.
		// 现在只有流水线状态会阻断能力；写工具能否执行由该会话自身的授权决定。
		blocked := make([]string, 0)
		if !live.AI.Enabled {
			blocked = append(blocked, "未启用生成模型")
		}
		if !live.Intent.Enabled {
			blocked = append(blocked, "未启用意图流水线")
		}
		out.Tools = append(out.Tools, toolInfo{
			BlockedReasons:    blocked,
			ID:                descriptor.ID,
			Provider:          registered.ProviderID,
			Description:       descriptor.Description,
			Category:          descriptor.Category,
			ScopeKinds:        append([]string{}, descriptor.ScopeKinds...),
			RiskLevel:         descriptor.EffectiveRiskLevel(),
			ReadOnly:          descriptor.ReadOnly,
			SideEffect:        descriptor.SideEffect,
			Permission:        descriptor.Permission,
			DefaultGranted:    pluginsapp.Allowed(descriptor, granted),
			Applicable:        applicable,
			UnavailableReason: unavailableReason,
		})
		if descriptor.Permission == "" {
			continue
		}
		group, ok := byPermission[descriptor.Permission]
		if !ok {
			group = &permissionInfo{ID: descriptor.Permission, DefaultGranted: granted[descriptor.Permission], Description: descriptor.Description, RiskLevel: descriptor.EffectiveRiskLevel()}
			byPermission[descriptor.Permission] = group
		} else if riskRank(descriptor.EffectiveRiskLevel()) > riskRank(group.RiskLevel) {
			// Keep the strictest risk so the group never depends on snapshot order.
			// 取最严格的风险级别，组内展示不随快照顺序变化。
			group.RiskLevel = descriptor.EffectiveRiskLevel()
		}
		group.Tools = append(group.Tools, descriptor.ID)
	}
	extraDescriptions := map[string]string{"qq.cross_conversation": "跨会话操作：仅来源会话授权即可操作当前机器人的其他目标", "qq.content.links": "发送链接：允许消息包含链接或远程媒体地址", "qq.content.at_all": "发送 @全体：允许通知群内所有成员", "qq.content.media": "发送媒体：允许图片、语音及其他非文本消息段"}
	for _, registered := range visible {
		for _, permission := range registered.Descriptor.AdditionalPermissions {
			group := byPermission[permission]
			if group == nil {
				// Conditional grants are write-level content gates; cross-conversation
				// access stays administrative.
				// 条件授权中的内容门属于写入级别；跨会话访问仍按管理级别处理。
				risk := contracts.RiskAdmin
				if strings.HasPrefix(permission, "qq.content.") {
					risk = contracts.RiskWrite
				}
				group = &permissionInfo{ID: permission, DefaultGranted: granted[permission], Description: extraDescriptions[permission], RiskLevel: risk}
				byPermission[permission] = group
			}
			group.Tools = append(group.Tools, registered.Descriptor.ID)
		}
	}
	for _, group := range byPermission {
		sort.Strings(group.Tools)
		out.Permissions = append(out.Permissions, *group)
	}
	sort.Slice(out.Permissions, func(i, j int) bool { return out.Permissions[i].ID < out.Permissions[j].ID })
	sort.Slice(out.Tools, func(i, j int) bool { return out.Tools[i].ID < out.Tools[j].ID })

	common.WriteSuccess(w, out)
}
