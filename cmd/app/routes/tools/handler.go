// Package tools serves the read-only tool catalog.
// Package tools 提供只读的工具清单接口。
package tools

import (
	"net/http"
	"sort"

	"github.com/HappyLadySauceM/Nagi/cmd/app/svc"
	"github.com/HappyLadySauceM/Nagi/cmd/app/types/common"
	pluginsapp "github.com/HappyLadySauceM/Nagi/internal/plugins/application"
)

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

// toolInfo describes one registered tool, including its default grant state.
// toolInfo 描述单个已注册工具，包含其默认授予状态。
type toolInfo struct {
	ID             string
	Provider       string
	Description    string
	ReadOnly       bool
	SideEffect     bool
	Permission     string
	DefaultGranted bool
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

// List returns the tools that were actually registered at startup. Default
// grants mirror runtime resolution, so the UI never needs to duplicate rules.
// List 返回启动时真正注册成功的工具。默认授予与运行时解析一致，
// 界面因此无需重复实现规则。
func (h *Handler) List(w http.ResponseWriter, r *http.Request) {
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
		out.Tools = append(out.Tools, toolInfo{
			ID:             descriptor.ID,
			Provider:       registered.ProviderID,
			Description:    descriptor.Description,
			ReadOnly:       descriptor.ReadOnly,
			SideEffect:     descriptor.SideEffect,
			Permission:     descriptor.Permission,
			DefaultGranted: pluginsapp.Allowed(descriptor, granted, allowSideEffects),
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
