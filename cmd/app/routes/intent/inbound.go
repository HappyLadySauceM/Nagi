package intent

import (
	"net/http"
	"strconv"

	"github.com/HappyLadySauceM/Nagi/cmd/app/types/common"
	v1 "github.com/HappyLadySauceM/Nagi/cmd/app/types/v1"
	intentcontracts "github.com/HappyLadySauceM/Nagi/internal/intent/contracts"
	"github.com/HappyLadySauceM/Nagi/internal/observability"
	"github.com/google/uuid"
	"go.opentelemetry.io/otel/trace"
)

type inboundItem struct {
	v1.MessageIngress
	ConversationID string
	JobID          int64
	JobError       string
	DecisionID     int64
	DecisionIDs    []int64 `json:",omitempty"`
	Decision       string
	ReasonCode     string
	Model          string
}

// Inbound serves the account-scoped ledger by message ID.
func (h *Handler) Inbound(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	botID, err := strconv.ParseInt(query.Get("bot_id"), 10, 64)
	if err != nil || botID <= 0 {
		common.WriteFailure(w, http.StatusBadRequest, "invalid bot_id")
		return
	}
	q := intentcontracts.InboundQuery{BotID: botID, Limit: 20, ConversationID: query.Get("conversation_id")}
	if q.ConversationID != "" {
		id, err := uuid.Parse(q.ConversationID)
		if err != nil {
			common.WriteFailure(w, http.StatusBadRequest, "invalid conversation_id")
			return
		}
		q.ConversationID = id.String()
	}
	if raw := query.Get("limit"); raw != "" {
		q.Limit, err = strconv.Atoi(raw)
		if err != nil || q.Limit < 1 || q.Limit > maxDecisionPageSize {
			common.WriteFailure(w, http.StatusBadRequest, "invalid limit")
			return
		}
	}
	if raw := query.Get("before_id"); raw != "" {
		q.BeforeID, err = strconv.ParseInt(raw, 10, 64)
		if err != nil || q.BeforeID < 0 {
			common.WriteFailure(w, http.StatusBadRequest, "invalid before_id")
			return
		}
	}
	if raw := r.PathValue("message_id"); raw != "" {
		q.MessageID, err = strconv.ParseInt(raw, 10, 64)
		if err != nil || q.MessageID < 1 {
			common.WriteFailure(w, http.StatusBadRequest, "invalid message_id")
			return
		}
		q.Limit, q.BeforeID, q.ConversationID = 1, 0, ""
	}
	repo, ok := h.svcCtx.Decisions.(intentcontracts.InboundReader)
	if !ok {
		common.WriteFailure(w, http.StatusServiceUnavailable, "inbound ledger unavailable")
		return
	}
	records, err := repo.ListInbound(r.Context(), q)
	if err != nil {
		common.WriteFailure(w, http.StatusInternalServerError, "read inbound ledger failed")
		return
	}
	items := make([]inboundItem, 0, len(records))
	for _, record := range records {
		var traceID string
		parent := observability.TraceContext{TraceParent: record.TraceParent}
		if parent.Valid() {
			traceID = trace.SpanContextFromContext(parent.Restore(r.Context())).TraceID().String()
		}
		items = append(items, inboundItem{
			MessageIngress: v1.MessageIngress{
				MessageID: record.MessageID, PlatformMessageID: record.PlatformMessageID,
				SenderID: record.SenderID, Text: boundContextText(record.Text),
				SegmentTypes: record.SegmentTypes, ReceivedAt: record.ReceivedAt,
				TraceID: traceID, JobStatus: record.JobStatus,
			},
			ConversationID: record.ConversationID, JobID: record.JobID, JobError: record.JobError,
			DecisionID: record.DecisionID, DecisionIDs: record.DecisionIDs, Decision: record.Decision, ReasonCode: record.ReasonCode, Model: record.Model,
		})
	}
	if q.MessageID == 0 {
		var blocked uint64
		if h.svcCtx.Conversation != nil {
			blocked = h.svcCtx.Conversation.BlockedIngress(q.BotID)
		}
		common.WriteSuccess(w, map[string]any{"Items": items, "Limit": q.Limit, "BlockedSinceStart": blocked})
		return
	}
	if len(items) == 0 {
		common.WriteFailure(w, http.StatusNotFound, "inbound message not found")
		return
	}
	common.WriteSuccess(w, items[0])
}
