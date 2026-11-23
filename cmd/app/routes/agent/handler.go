// Package agent exposes the delivery record of one agent run.
// Package agent 提供单次 Agent 运行的投递记录。
package agent

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/HappyLadySauceM/Nagi/cmd/app/svc"
	"github.com/HappyLadySauceM/Nagi/cmd/app/types/common"
	v1 "github.com/HappyLadySauceM/Nagi/cmd/app/types/v1"
	intentcontracts "github.com/HappyLadySauceM/Nagi/internal/intent/contracts"
)

// Handler serves the outbound ledger endpoint.
// Handler 提供投递台账接口。
type Handler struct {
	svcCtx *svc.ServiceContext
}

// NewHandler builds the agent handler.
// NewHandler 构建 Agent 处理器。
func NewHandler(svcCtx *svc.ServiceContext) *Handler { return &Handler{svcCtx: svcCtx} }

// Register wires routes.
// Register 注册路由。
func (h *Handler) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/agent/outbound", h.ListOutbound)
	h.RegisterPolicy(mux)
}

// ListOutbound returns the records of one job in sequence order.
// ListOutbound 按序号返回某个任务的投递记录。
func (h *Handler) ListOutbound(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	jobID, err := strconv.ParseInt(r.URL.Query().Get("job_id"), 10, 64)
	if err != nil || jobID <= 0 {
		common.WriteFailure(w, http.StatusBadRequest, "job_id 必填且必须为正整数")
		return
	}
	botID, e := strconv.ParseInt(r.URL.Query().Get("bot_id"), 10, 64)
	if e != nil || botID <= 0 {
		common.WriteFailure(w, 400, "invalid bot id")
		return
	}
	stamp, allowed := h.svcCtx.DecisionAuditStamp(r.Context(), botID)
	if !allowed {
		common.WriteFailure(w, 404, "job not found")
		return
	}
	if repo, live := h.svcCtx.Decisions.(intentcontracts.LiveDecisionReader); live {
		if !h.svcCtx.DecisionAuditAccount(r.Context(), botID) {
			common.WriteFailure(w, 404, "job not found")
			return
		}
		if allowed, err := repo.DecisionJobAccess(r.Context(), botID, jobID); err != nil || !allowed {
			common.WriteFailure(w, 404, "job not found")
			return
		}
	}
	records, summary, err := h.svcCtx.DecisionDelivery(r.Context(), botID, jobID)
	if errors.Is(err, svc.ErrDecisionDeliveryJob) {
		common.WriteFailure(w, http.StatusNotFound, "job not found")
		return
	}
	if err != nil {
		common.WriteFailure(w, http.StatusInternalServerError, "读取投递记录失败")
		return
	}
	items := make([]v1.OutboundInfo, 0, len(records))
	for _, record := range records {
		items = append(items, v1.OutboundInfo{
			RunID: record.RunID, ToolCallID: record.ToolCallID, ID: record.ID, JobID: record.JobID, ConversationID: record.ConversationID,
			Sequence: record.Sequence, Text: record.Text, Status: string(record.Status),
			PlatformMessageID: record.PlatformMessageID, ErrorCode: record.ErrorCode,
			CreatedAt: record.CreatedAt, UpdatedAt: record.UpdatedAt,
		})
	}
	if current, ok := h.svcCtx.DecisionAuditStamp(r.Context(), botID); !ok || current != stamp {
		common.WriteFailure(w, 404, "job not found")
		return
	}
	common.WriteSuccess(w, map[string]any{"Items": items, "Summary": summary})
}
