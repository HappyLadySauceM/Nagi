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
	w.Header().Set("Cache-Control", "no-store")
	query := r.URL.Query()
	botID, err := strconv.ParseInt(query.Get("bot_id"), 10, 64)
	if err != nil || botID <= 0 {
		common.WriteFailure(w, http.StatusBadRequest, "invalid bot_id")
		return
	}
	stamp, allowed := h.auditAccountStamp(r.Context(), botID)
	if !allowed {
		common.WriteFailure(w, http.StatusNotFound, "inbound ledger unavailable")
		return
	}
	q := intentcontracts.InboundQuery{AuditVisibleOnly: stamp != "", BotID: botID, Limit: 20, ConversationID: query.Get("conversation_id")}
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
	if !h.auditStampCurrent(r.Context(), botID, stamp) {
		common.WriteFailure(w, http.StatusConflict, "inbound ledger changed")
		return
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
	if err := h.attachInboundMedia(r.Context(), &item.MessageIngress, botID, item.ConversationID, item.DecisionIDs); err != nil {
		common.WriteFailure(w, http.StatusInternalServerError, "read inbound media failed")
		return
	}
	if archive := h.svcCtx.InboundArchive; archive != nil && h.auditSourceAllowed(r.Context(), botID, item.ConversationID, q.MessageID) {
		attachments, err := archive.List(r.Context(), botID, q.MessageID)
		if err != nil && !errors.Is(err, mediacontracts.ErrAttachmentNotFound) {
			common.WriteFailure(w, http.StatusInternalServerError, "read attachments failed")
			return
		}
		item.Attachments = attachments
	}
	if !h.auditStampCurrent(r.Context(), botID, stamp) {
		common.WriteFailure(w, http.StatusNotFound, "inbound message not found")
		return
	}
	common.WriteSuccess(w, item)
}

// attachInboundMedia uses the shared media projection for retained source evidence.
// attachInboundMedia 使用共同的媒体投影读取仍留存且获准的来源证据。
func (h *Handler) attachInboundMedia(ctx context.Context, ingress *v1.MessageIngress, bot int64, conversation string, decisions []int64) error {
	if !h.auditSourceAllowed(ctx, bot, conversation, ingress.MessageID) {
		return nil
	}
	if repo, ok := h.svcCtx.Decisions.(intentcontracts.MediaReader); ok {
		media, err := repo.InboundMedia(ctx, bot, ingress.MessageID, decisions)
		if err != nil {
			return err
		}
		ingress.Media = &media
	}
	return nil
}

func (h *Handler) DownloadAttachment(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	botID, botErr := strconv.ParseInt(r.URL.Query().Get("bot_id"), 10, 64)
	messageID, msgErr := strconv.ParseInt(r.PathValue("message_id"), 10, 64)
	attachmentID, idErr := uuid.Parse(r.PathValue("attachment_id"))
	if botErr != nil || msgErr != nil || idErr != nil || botID <= 0 || messageID <= 0 {
		common.WriteFailure(w, http.StatusBadRequest, "invalid attachment address")
		return
	}
	archive := h.svcCtx.InboundArchive
	if archive == nil || !h.auditSourceAllowed(r.Context(), botID, "", messageID) {
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
	if !h.auditSourceAllowed(ctx, botID, "", messageID) {
		common.WriteFailure(w, http.StatusNotFound, "attachment not found")
		return
	}
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
	w.Header().Set("Cache-Control", "no-store")
	botID, botErr := strconv.ParseInt(r.URL.Query().Get("bot_id"), 10, 64)
	messageID, msgErr := strconv.ParseInt(r.PathValue("message_id"), 10, 64)
	if botErr != nil || msgErr != nil || botID <= 0 || messageID <= 0 {
		common.WriteFailure(w, http.StatusBadRequest, "invalid inbound address")
		return
	}
	if !h.auditSourceAllowed(r.Context(), botID, "", messageID) {
		common.WriteFailure(w, http.StatusNotFound, "inbound message not found")
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
