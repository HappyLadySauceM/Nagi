// Package audit exposes administrator-only local AI audit facts.
// Package audit 提供仅管理员可访问的本地 AI 审计事实。
package audit

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/HappyLadySauceM/Nagi/cmd/app/svc"
	"github.com/HappyLadySauceM/Nagi/cmd/app/types/common"
	auditdomain "github.com/HappyLadySauceM/Nagi/internal/audit/domain"
	"github.com/google/uuid"
)

const maxRunPageSize = 100

// Handler serves durable worker-run audit records.
// Handler 提供持久化的 worker 运行审计记录。
type Handler struct{ svcCtx *svc.ServiceContext }

// NewHandler builds the local audit handler.
// NewHandler 构建本地审计处理器。
func NewHandler(svcCtx *svc.ServiceContext) *Handler { return &Handler{svcCtx: svcCtx} }

// Register wires the read-only audit endpoints.
// Register 注册只读审计端点。
func (h *Handler) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/audit/runs", h.ListRuns)
	mux.HandleFunc("GET /api/v1/audit/runs/{id}", h.GetRun)
	mux.HandleFunc("GET /api/v1/audit/artifacts/{id}", h.GetArtifact)
}

// ListRuns returns the newest runs of an account, optionally narrowed to a
// stable conversation or one task.
// ListRuns 返回某账号最新运行，可按稳定会话或单个任务收窄。
func (h *Handler) ListRuns(w http.ResponseWriter, r *http.Request) {
	if h.svcCtx.AIAudit == nil {
		common.WriteFailure(w, http.StatusServiceUnavailable, "AI 审计存储不可用")
		return
	}
	botID, err := h.svcCtx.ResolveBotID(r.URL.Query().Get("bot_id"))
	if err != nil {
		common.WriteFailure(w, http.StatusBadRequest, "bot_id 无效或缺失")
		return
	}
	query := auditdomain.AIRunQuery{BotID: botID, ConversationID: strings.TrimSpace(r.URL.Query().Get("conversation_id")), Limit: 20}
	if query.ConversationID != "" {
		if _, err := uuid.Parse(query.ConversationID); err != nil {
			common.WriteFailure(w, http.StatusBadRequest, "conversation_id 必须为 UUID")
			return
		}
	}
	if raw := r.URL.Query().Get("job_id"); raw != "" {
		query.JobID, err = strconv.ParseInt(raw, 10, 64)
		if err != nil || query.JobID <= 0 {
			common.WriteFailure(w, http.StatusBadRequest, "job_id 必须为正整数")
			return
		}
	}
	if raw := r.URL.Query().Get("limit"); raw != "" {
		query.Limit, err = strconv.Atoi(raw)
		if err != nil || query.Limit < 1 || query.Limit > maxRunPageSize {
			common.WriteFailure(w, http.StatusBadRequest, "limit 必须在 1 到 100 之间")
			return
		}
	}
	runs, err := h.svcCtx.AIAudit.ListRuns(r.Context(), query)
	if err != nil {
		common.WriteFailure(w, http.StatusInternalServerError, "读取 AI 审计运行记录失败")
		return
	}
	common.WriteSuccess(w, map[string]any{"items": runs, "limit": query.Limit})
}

// GetRun returns a complete run including ordered operation events and media
// metadata. Media bytes are intentionally not exposed here.
// GetRun 返回含有有序操作和媒体元数据的完整运行，不在此暴露媒体字节。
func (h *Handler) GetRun(w http.ResponseWriter, r *http.Request) {
	if h.svcCtx.AIAudit == nil {
		common.WriteFailure(w, http.StatusServiceUnavailable, "AI 审计存储不可用")
		return
	}
	id := r.PathValue("id")
	if _, err := uuid.Parse(id); err != nil {
		common.WriteFailure(w, http.StatusBadRequest, "运行 ID 必须为 UUID")
		return
	}
	run, err := h.svcCtx.AIAudit.GetRun(r.Context(), id)
	if errors.Is(err, auditdomain.ErrNotFound) {
		common.WriteFailure(w, http.StatusNotFound, "AI 审计运行记录不存在")
		return
	}
	if err != nil {
		common.WriteFailure(w, http.StatusInternalServerError, "读取 AI 审计运行详情失败")
		return
	}
	common.WriteSuccess(w, run)
}

// GetArtifact returns durable media metadata. A temporary read URL is added in
// the media-storage batch once the object is uploaded and still retained.
// GetArtifact 返回持久化媒体元数据。媒体存储批次会在对象已上传且未到期时补充临时读取地址。
func (h *Handler) GetArtifact(w http.ResponseWriter, r *http.Request) {
	if h.svcCtx.AIAudit == nil {
		common.WriteFailure(w, http.StatusServiceUnavailable, "AI 审计存储不可用")
		return
	}
	id := r.PathValue("id")
	if _, err := uuid.Parse(id); err != nil {
		common.WriteFailure(w, http.StatusBadRequest, "媒体工件 ID 必须为 UUID")
		return
	}
	artifact, err := h.svcCtx.AIAudit.GetArtifact(r.Context(), id)
	if errors.Is(err, auditdomain.ErrNotFound) {
		common.WriteFailure(w, http.StatusNotFound, "媒体工件不存在")
		return
	}
	if err != nil {
		common.WriteFailure(w, http.StatusInternalServerError, "读取媒体工件失败")
		return
	}
	common.WriteSuccess(w, artifact)
}
