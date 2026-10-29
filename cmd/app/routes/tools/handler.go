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
)

// unavailableScopeKindMismatch is the stable reason returned when a tool does
// not apply to the requested conversation kind.
// unavailableScopeKindMismatch 是工具不适用于请求会话类型时返回的稳定原因。
const unavailableScopeKindMismatch = "scope_kind_mismatch"

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
	ID          string
	Provider    string
	Description string
	Category    string
	ScopeKinds  []string
	RiskLevel   contracts.RiskLevel
	ReadOnly    bool
	SideEffect  bool
	Permission  string
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
	ID             string
	Tools          []string
	DefaultGranted bool
}

// catalog is the response of GET /api/v1/tools.
// catalog 是 GET /api/v1/tools 的响应体。
type catalog struct {
	Providers        []string
	Tools            []toolInfo
	Permissions      []permissionInfo
	AllowSideEffects bool
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
	snapshot := registry.Snapshot()
	granted := pluginsapp.ResolvePermissions(snapshot, h.svcCtx.Config.Tools.PermissionDefaults, nil)
	allowSideEffects := h.svcCtx.Config.Tools.AllowSideEffects

	out := catalog{
		Providers:        make([]string, 0),
		Tools:            make([]toolInfo, 0, len(snapshot)),
		Permissions:      make([]permissionInfo, 0),
		AllowSideEffects: allowSideEffects,
	}
	for _, manifest := range registry.Manifests() {
		out.Providers = append(out.Providers, manifest.ID)
	}
	sort.Strings(out.Providers)

	byPermission := make(map[string]*permissionInfo)
	for _, registered := range snapshot {
		descriptor := registered.Descriptor
		applicable := true
		unavailableReason := ""
		if scopeKind != "" {
			applicable = descriptor.AppliesTo(scopeKind)
			if !applicable {
				unavailableReason = unavailableScopeKindMismatch
			}
		}
		out.Tools = append(out.Tools, toolInfo{
			ID:                descriptor.ID,
			Provider:          registered.ProviderID,
			Description:       descriptor.Description,
			Category:          descriptor.Category,
			ScopeKinds:        append([]string{}, descriptor.ScopeKinds...),
			RiskLevel:         descriptor.EffectiveRiskLevel(),
			ReadOnly:          descriptor.ReadOnly,
			SideEffect:        descriptor.SideEffect,
			Permission:        descriptor.Permission,
			DefaultGranted:    pluginsapp.Allowed(descriptor, granted, allowSideEffects),
			Applicable:        applicable,
			UnavailableReason: unavailableReason,
		})
		if descriptor.Permission == "" {
			continue
		}
		group, ok := byPermission[descriptor.Permission]
		if !ok {
			group = &permissionInfo{ID: descriptor.Permission, DefaultGranted: granted[descriptor.Permission]}
			byPermission[descriptor.Permission] = group
		}
		group.Tools = append(group.Tools, descriptor.ID)
	}
	for _, group := range byPermission {
		sort.Strings(group.Tools)
		out.Permissions = append(out.Permissions, *group)
	}
	sort.Slice(out.Permissions, func(i, j int) bool { return out.Permissions[i].ID < out.Permissions[j].ID })
	sort.Slice(out.Tools, func(i, j int) bool { return out.Tools[i].ID < out.Tools[j].ID })

	common.WriteSuccess(w, out)
}
