package intent

import (
	"context"
	"errors"
	"io"
	"mime"
	"net/http"
	"strconv"
	"time"

	"github.com/HappyLadySauceM/Nagi/cmd/app/types/common"
	v1 "github.com/HappyLadySauceM/Nagi/cmd/app/types/v1"
	intentcontracts "github.com/HappyLadySauceM/Nagi/internal/intent/contracts"
	mediacontracts "github.com/HappyLadySauceM/Nagi/internal/media/contracts"
	"github.com/HappyLadySauceM/Nagi/internal/observability"
	"github.com/google/uuid"
	"go.opentelemetry.io/otel/trace"
)

type inboundItem struct {
	Attachments []mediacontracts.InboundAttachment `json:",omitempty"`
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
	item := items[0]
	if reader, ok := h.svcCtx.Decisions.(intentcontracts.MediaReader); ok {
		value, err := reader.InboundMedia(r.Context(), botID, q.MessageID, item.DecisionIDs)
		if err != nil {
			common.WriteFailure(w, http.StatusInternalServerError, "read inbound media evidence failed")
			return
		}
		item.Media = &value
	}
	if archive := h.svcCtx.InboundArchive; archive != nil {
		attachments, err := archive.List(r.Context(), botID, q.MessageID)
		if err != nil && !errors.Is(err, mediacontracts.ErrAttachmentNotFound) {
			common.WriteFailure(w, http.StatusInternalServerError, "read attachments failed")
			return
		}
		item.Attachments = attachments
	}
	common.WriteSuccess(w, item)
}

func (h *Handler) DownloadAttachment(w http.ResponseWriter, r *http.Request) {
	botID, botErr := strconv.ParseInt(r.URL.Query().Get("bot_id"), 10, 64)
	messageID, msgErr := strconv.ParseInt(r.PathValue("message_id"), 10, 64)
	attachmentID, idErr := uuid.Parse(r.PathValue("attachment_id"))
	if botErr != nil || msgErr != nil || idErr != nil || botID <= 0 || messageID <= 0 {
		common.WriteFailure(w, http.StatusBadRequest, "invalid attachment address")
		return
	}
	archive := h.svcCtx.InboundArchive
	if archive == nil {
		common.WriteFailure(w, http.StatusNotFound, "attachment not found")
		return
	}
	deadline := time.Now().Add(5 * time.Minute)
	if err := http.NewResponseController(w).SetWriteDeadline(deadline); err != nil && !errors.Is(err, http.ErrNotSupported) {
		common.WriteFailure(w, http.StatusInternalServerError, "attachment response unavailable")
		return
	}
	ctx, cancel := context.WithDeadline(r.Context(), deadline)
	defer cancel()
	body, meta, err := archive.Open(ctx, botID, messageID, attachmentID.String())
	if errors.Is(err, mediacontracts.ErrAttachmentNotFound) {
		common.WriteFailure(w, http.StatusNotFound, "attachment not found")
		return
	}
	if err != nil {
		common.WriteFailure(w, http.StatusBadGateway, "attachment unavailable")
		return
	}
	defer body.Close()
	filename := meta.Filename
	if filename == "" {
		filename = attachmentID.String()
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": filename}))
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Length", strconv.FormatInt(meta.SizeBytes, 10))
	if _, err := io.Copy(w, body); err != nil {
		h.svcCtx.Logger.Warn("inbound attachment response interrupted")
	}
}

func (h *Handler) BackfillAttachments(w http.ResponseWriter, r *http.Request) {
	botID, botErr := strconv.ParseInt(r.URL.Query().Get("bot_id"), 10, 64)
	messageID, msgErr := strconv.ParseInt(r.PathValue("message_id"), 10, 64)
	if botErr != nil || msgErr != nil || botID <= 0 || messageID <= 0 {
		common.WriteFailure(w, http.StatusBadRequest, "invalid inbound address")
		return
	}
	archive := h.svcCtx.InboundArchive
	if archive == nil || h.svcCtx.Conversation == nil {
		common.WriteFailure(w, http.StatusServiceUnavailable, "attachment archive unavailable")
		return
	}
	_, err := archive.List(r.Context(), botID, messageID)
	if errors.Is(err, mediacontracts.ErrAttachmentNotFound) {
		common.WriteFailure(w, http.StatusNotFound, "inbound message not found")
		return
	}
	if err != nil || h.svcCtx.Conversation.BackfillInboundAttachments(r.Context(), botID, messageID) != nil {
		common.WriteFailure(w, http.StatusInternalServerError, "attachment backfill failed")
		return
	}
	common.WriteSuccess(w, map[string]int64{"MessageID": messageID})
}
