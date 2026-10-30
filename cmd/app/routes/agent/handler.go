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
	h.RegisterPolicy(mux)
}

// outboundInfo is one delivery record.
// outboundInfo 是一条投递记录。
type outboundInfo struct {
	RunID             string
	ToolCallID        string
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
	{
		raw := r.URL.Query().Get("bot_id")
		botID, e := strconv.ParseInt(raw, 10, 64)
		if e != nil || botID <= 0 {
			common.WriteFailure(w, 400, "invalid bot id")
			return
		}
		owns, e := h.svcCtx.Outbound.OwnsJob(r.Context(), botID, jobID)
		if e != nil {
			common.WriteFailure(w, 500, "job unavailable")
			return
		}
		if !owns {
			common.WriteFailure(w, 404, "job not found")
			return
		}
	}
	records, err := h.svcCtx.Outbound.ListByJob(r.Context(), jobID)
	if err != nil {
		common.WriteFailure(w, http.StatusInternalServerError, "读取投递记录失败")
		return
	}
	items := make([]outboundInfo, 0, len(records))
	for _, record := range records {
		items = append(items, outboundInfo{
			RunID: record.RunID, ToolCallID: record.ToolCallID, ID: record.ID, JobID: record.JobID, ConversationID: record.ConversationID,
			Sequence: record.Sequence, Text: record.Text, Status: string(record.Status),
			PlatformMessageID: record.PlatformMessageID, ErrorCode: record.ErrorCode,
			CreatedAt: record.CreatedAt, UpdatedAt: record.UpdatedAt,
		})
	}
	outcome, reason, denied, err := h.svcCtx.Outbound.RunOutcome(r.Context(), jobID)
	if err != nil {
		common.WriteFailure(w, 500, "run outcome unavailable")
		return
	}
	jobOutcome := outcome
	sent, failed, unknown, attempted := 0, 0, 0, 0
	for _, record := range records {
		switch string(record.Status) {
		case "sent":
			sent++
			attempted++
		case "failed":
			failed++
			attempted++
		case "unknown", "sending":
			unknown++
			attempted++
		}
	}
	if unknown > 0 {
		outcome = "unknown"
	} else if failed > 0 {
		outcome = "failed"
	} else if sent > 0 && denied == 0 {
		outcome = "completed"
	}
	if sent > 0 && (failed > 0 || unknown > 0 || denied > 0 || jobOutcome == "failed" || jobOutcome == "blocked") {
		outcome = "partial"
	}
	if jobOutcome == "processing" && failed == 0 && unknown == 0 && denied == 0 {
		outcome = "processing"
	}
	if outcome == "" {
		outcome = "no_output"
	}
	common.WriteSuccess(w, map[string]any{"Items": items, "Summary": map[string]any{"Outcome": outcome, "Reason": reason, "Attempted": attempted, "Sent": sent, "Failed": failed, "Unknown": unknown, "Denied": denied}})
}
