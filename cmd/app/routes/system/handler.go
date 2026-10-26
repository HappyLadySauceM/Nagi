// Package system provides health checks, the runtime status endpoint and the
// account list that stays available without a database.
// Package system 提供健康检查、运行状态接口，以及不依赖数据库的账号列表。
package system

import (
	"net/http"
	"strconv"
	"time"

	"github.com/HappyLadySauceM/Nagi/cmd/app/svc"
	"github.com/HappyLadySauceM/Nagi/cmd/app/types/common"
)

// Handler serves /livez, /readyz, /api/v1/status and /api/v1/accounts.
// Handler 服务于 /livez、/readyz、/api/v1/status 与 /api/v1/accounts。
type Handler struct {
	svcCtx *svc.ServiceContext
}

// NewHandler builds the system handler.
// NewHandler 构建系统接口处理器。
func NewHandler(svcCtx *svc.ServiceContext) *Handler {
	return &Handler{svcCtx: svcCtx}
}

// Register wires routes using Go 1.22 method+path patterns.
// Register 以 Go 1.22 的方法+路径模式注册路由。
func (h *Handler) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /livez", h.Livez)
	mux.HandleFunc("GET /readyz", h.Readyz)
	mux.HandleFunc("GET /api/v1/status", h.Status)
	mux.HandleFunc("GET /api/v1/accounts", h.Accounts)
	mux.HandleFunc("GET /api/v1/accounts/{bot_id}", h.Account)
}

// Livez reports process liveness without touching external dependencies.
// Livez 报告进程存活，不检查外部依赖。
func (h *Handler) Livez(w http.ResponseWriter, r *http.Request) {
	common.WriteSuccess(w, map[string]string{"status": "alive"})
}

// Readyz reports whether at least one OneBot connection is ready.
// Readyz 报告是否至少有一条 OneBot 连接就绪；全部未就绪返回 503。
func (h *Handler) Readyz(w http.ResponseWriter, r *http.Request) {
	if !h.svcCtx.Accounts.ConnectedAny() {
		common.WriteFailure(w, http.StatusServiceUnavailable, "no onebot connection is ready")
		return
	}
	common.WriteSuccess(w, map[string]string{"status": "ready"})
}

// Status returns the runtime status snapshot.
// Status 返回运行状态快照。
func (h *Handler) Status(w http.ResponseWriter, r *http.Request) {
	common.WriteSuccess(w, h.svcCtx.StatusSnapshot())
}

// accountView is one entry of the account list.
// accountView 是账号列表中的一项。
type accountView struct {
	BotID           int64  `json:"bot_id,omitempty"`
	Nickname        string `json:"nickname"`
	Endpoint        string `json:"endpoint"`
	ExpectedQQ      int64  `json:"expected_qq,omitempty"`
	Connected       bool   `json:"connected"`
	SelfIDKnown     bool   `json:"self_id_known"`
	Rejected        string `json:"rejected,omitempty"`
	HeartbeatSeen   bool   `json:"heartbeat_seen"`
	LastHeartbeatAt string `json:"last_heartbeat,omitempty"`
	SyncedAt        string `json:"synced_at,omitempty"`
}

// Accounts reports every configured account and its connection state. It only
// reads the runtime and, when available, stored nicknames, so it stays usable
// without a database.
// Accounts 报告全部已配置账号及其连接状态。它只读取运行时与（可用时）已存储昵称，
// 因此未配置数据库时仍可使用。
func (h *Handler) Accounts(w http.ResponseWriter, r *http.Request) {
	nicknames, syncedAt := h.storedAccounts(r)
	list := h.svcCtx.Accounts.Snapshot()
	items := make([]accountView, 0, len(list))
	for _, item := range list {
		view := accountView{
			BotID:         item.BotID,
			Nickname:      nicknames[item.BotID],
			Endpoint:      item.Endpoint,
			ExpectedQQ:    item.ExpectedQQ,
			Connected:     item.Connected,
			SelfIDKnown:   item.SelfIDKnown,
			Rejected:      item.Rejected,
			HeartbeatSeen: item.HeartbeatSeen,
		}
		if at, ok := syncedAt[item.BotID]; ok {
			view.SyncedAt = at.Format(time.RFC3339)
		}
		if item.HeartbeatSeen {
			view.LastHeartbeatAt = item.LastHeartbeatAt.Format(time.RFC3339)
		}
		items = append(items, view)
	}
	common.WriteSuccess(w, map[string]any{
		"connected":     h.svcCtx.Accounts.ConnectedAny(),
		"db_configured": h.svcCtx.Directory != nil,
		"accounts":      items,
	})
}

// Account returns one configured account by bot id.
// Account 按 bot id 返回单个已配置账号。
func (h *Handler) Account(w http.ResponseWriter, r *http.Request) {
	botID, err := strconv.ParseInt(r.PathValue("bot_id"), 10, 64)
	if err != nil || botID <= 0 {
		common.WriteFailure(w, http.StatusBadRequest, "invalid bot id")
		return
	}
	nicknames, syncedAt := h.storedAccounts(r)
	for _, item := range h.svcCtx.Accounts.Snapshot() {
		if item.BotID != botID {
			continue
		}
		view := accountView{
			BotID:         item.BotID,
			Nickname:      nicknames[item.BotID],
			Endpoint:      item.Endpoint,
			ExpectedQQ:    item.ExpectedQQ,
			Connected:     item.Connected,
			SelfIDKnown:   item.SelfIDKnown,
			Rejected:      item.Rejected,
			HeartbeatSeen: item.HeartbeatSeen,
		}
		if at, ok := syncedAt[item.BotID]; ok {
			view.SyncedAt = at.Format(time.RFC3339)
		}
		if item.HeartbeatSeen {
			view.LastHeartbeatAt = item.LastHeartbeatAt.Format(time.RFC3339)
		}
		common.WriteSuccess(w, view)
		return
	}
	common.WriteFailure(w, http.StatusNotFound, "bot account not configured")
}

// storedAccounts loads nicknames and last sync times; missing database is fine.
// storedAccounts 读取昵称与最近同步时间；数据库缺失时返回空。
func (h *Handler) storedAccounts(r *http.Request) (map[int64]string, map[int64]time.Time) {
	nicknames := map[int64]string{}
	syncedAt := map[int64]time.Time{}
	if h.svcCtx.Directory == nil {
		return nicknames, syncedAt
	}
	rows, err := h.svcCtx.Directory.BotAccounts(r.Context())
	if err != nil {
		h.svcCtx.Logger.Warn("load bot accounts failed", "error", err)
		return nicknames, syncedAt
	}
	for _, row := range rows {
		nicknames[row.BotID] = row.Nickname
		if !row.LastSeenAt.IsZero() && row.LastSeenAt.Unix() > 0 {
			syncedAt[row.BotID] = row.LastSeenAt
		}
	}
	return nicknames, syncedAt
}
