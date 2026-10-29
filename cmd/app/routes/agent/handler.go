// Package agent exposes the delivery record of one agent run.
// Package agent 提供单次 Agent 运行的投递记录。
package agent

import (
	"net/http"
	"strconv"
	"time"

	"github.com/HappyLadySauceM/Nagi/cmd/app/svc"
	"github.com/HappyLadySauceM/Nagi/cmd/app/types/common"
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
}

// outboundInfo is one delivery record.
// outboundInfo 是一条投递记录。
type outboundInfo struct {
	ID                string
	JobID             int64
	ConversationID    string
	Sequence          int
	Text              string
	Status            string
	PlatformMessageID int64
	ErrorCode         string
	CreatedAt         time.Time
	UpdatedAt         time.Time
}

// ListOutbound returns the records of one job in sequence order.
// ListOutbound 按序号返回某个任务的投递记录。
func (h *Handler) ListOutbound(w http.ResponseWriter, r *http.Request) {
	jobID, err := strconv.ParseInt(r.URL.Query().Get("job_id"), 10, 64)
	if err != nil || jobID <= 0 {
		common.WriteFailure(w, http.StatusBadRequest, "job_id 必填且必须为正整数")
		return
	}
	records, err := h.svcCtx.Outbound.ListByJob(r.Context(), jobID)
	if err != nil {
		common.WriteFailure(w, http.StatusInternalServerError, "读取投递记录失败")
		return
	}
	items := make([]outboundInfo, 0, len(records))
	for _, record := range records {
		items = append(items, outboundInfo{
			ID: record.ID, JobID: record.JobID, ConversationID: record.ConversationID,
			Sequence: record.Sequence, Text: record.Text, Status: string(record.Status),
			PlatformMessageID: record.PlatformMessageID, ErrorCode: record.ErrorCode,
			CreatedAt: record.CreatedAt, UpdatedAt: record.UpdatedAt,
		})
	}
	common.WriteSuccess(w, map[string]any{"Items": items})
}
