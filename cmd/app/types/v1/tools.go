package v1

import "github.com/HappyLadySauceM/Nagi/internal/plugins/contracts"

// ToolInfo describes one registered tool.
// ToolInfo 描述单个已注册工具。
type ToolInfo struct {
	BlockedReasons    []string
	ID                string
	Provider          string
	Description       string
	Category          string
	ScopeKinds        []string
	RiskLevel         contracts.RiskLevel
	RequiresGroupRole string
	ReadOnly          bool
	SideEffect        bool
	Permission        string
	DefaultGranted    bool
	Applicable        bool
	UnavailableReason string
}

// PermissionInfo groups the tools that require one permission.
// PermissionInfo 归组需要同一权限的工具。
type PermissionInfo struct {
	Requires          []string
	Description       string
	RiskLevel         contracts.RiskLevel
	RequiresGroupRole string
	ID                string
	Tools             []string
	DefaultGranted    bool
	Kind              string
	Applicable        bool
	UnavailableReason string
}

// Catalog is the response of GET /api/v1/tools.
// Catalog 是 GET /api/v1/tools 的响应体。
type Catalog struct {
	Providers   []string
	Tools       []ToolInfo
	Permissions []PermissionInfo
}
