package intent

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/HappyLadySauceM/Nagi/cmd/app/types/common"
	"github.com/HappyLadySauceM/Nagi/internal/intent/contracts"
	"github.com/HappyLadySauceM/Nagi/internal/intent/domain"
	"github.com/google/uuid"
)

const liveLease = 10 * time.Second
const liveHeartbeat = 5 * time.Second

type liveCursor struct {
	Bot      int64
	Position int64
	Scope    string
	Issued   int64
	History  contracts.LiveHistoryKey `json:",omitempty"`
}

func cursorCipher(secret string) (cipher.AEAD, error) {
	key := sha256.Sum256([]byte("nagi-intent-live-cursor-v1\x00" + secret))
	block, err := aes.NewCipher(key[:])
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

func encodeCursor(box cipher.AEAD, bot, position int64, scope string, history contracts.LiveHistoryKey) (string, error) {
	nonce := make([]byte, box.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	raw, err := json.Marshal(liveCursor{Bot: bot, Position: position, Scope: scope, Issued: time.Now().Unix(), History: history})
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(box.Seal(nonce, nonce, raw, nil)), nil
}

func decodeCursor(box cipher.AEAD, encoded string, bot int64, history bool) (liveCursor, bool) {
	var out liveCursor
	if len(encoded) > 512 {
		return out, false
	}
	raw, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil || len(raw) <= box.NonceSize() {
		return out, false
	}
	nonce := raw[:box.NonceSize()]
	data, err := box.Open(nil, nonce, raw[box.NonceSize():], nil)
	now := time.Now().Unix()
	if err != nil || json.Unmarshal(data, &out) != nil || out.Bot != bot || out.Scope == "" || out.Position < 0 || out.Issued > now || now-out.Issued > 86400 || history != (out.History.RowKey != "") {
		return out, false
	}
	return out, true
}

func (h *Handler) accountAuditAllowed(ctx context.Context, bot int64) bool {
	return h.svcCtx.DecisionAuditAccount(ctx, bot)
}

func (h *Handler) liveAccess(w http.ResponseWriter, r *http.Request) (contracts.LiveDecisionReader, int64, cipher.AEAD, bool) {
	w.Header().Set("Cache-Control", "no-store")
	bot, err := strconv.ParseInt(r.URL.Query().Get("bot_id"), 10, 64)
	if err != nil || bot <= 0 {
		common.WriteFailure(w, http.StatusBadRequest, "invalid bot_id")
		return nil, 0, nil, false
	}
	repo, ok := h.svcCtx.Decisions.(contracts.LiveDecisionReader)
	if !ok || !h.accountAuditAllowed(r.Context(), bot) {
		common.WriteFailure(w, http.StatusNotFound, "decision history unavailable")
		return nil, 0, nil, false
	}
	box, err := cursorCipher(h.svcCtx.Config.Server.AdminToken)
	if err != nil {
		common.WriteFailure(w, http.StatusServiceUnavailable, "decision history unavailable")
		return nil, 0, nil, false
	}
	return repo, bot, box, true
}

func validRunSelection(key string) bool {
	kind, id, ok := strings.Cut(key, ":")
	if !ok || len(key) > 40 || id == "" {
		return false
	}
	switch kind {
	case "r":
		parsed, err := uuid.Parse(id)
		return err == nil && parsed.String() == id
	case "d":
		parsed, err := strconv.ParseInt(id, 10, 64)
		return err == nil && parsed > 0 && strconv.FormatInt(parsed, 10) == id
	default:
		return false
	}
}

func (h *Handler) liveSnapshot(w http.ResponseWriter, r *http.Request) {
	repo, bot, box, ok := h.liveAccess(w, r)
	if !ok {
		return
	}
	var page liveCursor
	if raw := r.URL.Query().Get("before"); raw != "" {
		var valid bool
		page, valid = decodeCursor(box, raw, bot, true)
		if !valid {
			common.WriteFailure(w, http.StatusConflict, "history cursor expired")
			return
		}
	}
	var snapshot contracts.LiveSnapshot
	var err error
	selection := r.URL.Query()["selected"]
	if page.History.RowKey == "" && len(selection) > 1 {
		common.WriteFailure(w, http.StatusBadRequest, "invalid run selection")
		return
	}
	if selected := r.URL.Query().Get("selected"); selected != "" && page.History.RowKey == "" {
		reader, supported := repo.(interface {
			DecisionRunSnapshotSelected(context.Context, int64, contracts.LiveHistoryKey, string) (contracts.LiveSnapshot, error)
		})
		if !supported || !validRunSelection(selected) {
			common.WriteFailure(w, http.StatusBadRequest, "invalid run selection")
			return
		}
		snapshot, err = reader.DecisionRunSnapshotSelected(r.Context(), bot, page.History, selected)
	} else {
		snapshot, err = repo.DecisionRunSnapshot(r.Context(), bot, page.History)
	}
	scope, scopeErr := repo.DecisionScope(r.Context(), bot)
	if err != nil || scopeErr != nil || scope != snapshot.Scope || (page.Scope != "" && page.Scope != scope) || !h.accountAuditAllowed(r.Context(), bot) {
		common.WriteFailure(w, http.StatusConflict, "decision history changed")
		return
	}
	cursor, err := encodeCursor(box, bot, snapshot.Position, snapshot.Scope, contracts.LiveHistoryKey{})
	var next string
	if err == nil && snapshot.Next.RowKey != "" {
		next, err = encodeCursor(box, bot, 0, snapshot.Scope, snapshot.Next)
	}
	if err != nil {
		common.WriteFailure(w, http.StatusServiceUnavailable, "cursor unavailable")
		return
	}
	common.WriteSuccess(w, struct {
		Rows         []contracts.DecisionLiveRow
		Cursor, Next string
		LeaseMS      int64
	}{snapshot.Rows, cursor, next, liveLease.Milliseconds()})
}

func (h *Handler) liveStream(w http.ResponseWriter, r *http.Request) {
	repo, bot, box, ok := h.liveAccess(w, r)
	if !ok {
		return
	}
	select {
	case h.liveSlots <- struct{}{}:
		defer func() { <-h.liveSlots }()
	default:
		common.WriteFailure(w, http.StatusServiceUnavailable, "too many live streams")
		return
	}
	raw := r.Header.Get("Last-Event-ID")
	if raw == "" {
		raw = r.URL.Query().Get("cursor")
	}
	cursor, valid := decodeCursor(box, raw, bot, false)
	rc := http.NewResponseController(w)
	if err := rc.SetWriteDeadline(time.Now().Add(2 * time.Second)); err != nil {
		common.WriteFailure(w, http.StatusServiceUnavailable, "stream response unavailable")
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("X-Accel-Buffering", "no")
	emit := func(event, id string, data any) error {
		payload, err := json.Marshal(data)
		if err != nil {
			return err
		}
		if err := rc.SetWriteDeadline(time.Now().Add(2 * time.Second)); err != nil {
			return err
		}
		if id != "" {
			if _, err := fmt.Fprintf(w, "id: %s\n", id); err != nil {
				return err
			}
		}
		if _, err := fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, payload); err != nil {
			return err
		}
		if err := rc.Flush(); err != nil {
			return err
		}
		return rc.SetWriteDeadline(time.Time{})
	}
	if !valid {
		_ = emit("reset", "", struct{}{})
		return
	}
	var wake <-chan struct{}
	if h.svcCtx.DecisionWakeup != nil {
		var unsubscribe func()
		wake, unsubscribe = h.svcCtx.DecisionWakeup.Subscribe(bot)
		defer unsubscribe()
	}
	// A bounded fallback also covers lost notifications and replica startup.
	// 有界兜底也覆盖通知丢失和副本启动窗口。
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	var renewed time.Time
	for {
		if !h.accountAuditAllowed(r.Context(), bot) {
			_ = emit("reset", "", struct{}{})
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		batch, err := repo.DecisionRunBatch(ctx, bot, cursor.Position, cursor.Scope)
		cancel()
		if err != nil || batch.Reset || !h.accountAuditAllowed(r.Context(), bot) {
			_ = emit("reset", "", struct{}{})
			return
		}
		if len(batch.Events) > 0 {
			ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
			scope, err := repo.DecisionScope(ctx, bot)
			cancel()
			if err != nil || scope != cursor.Scope {
				_ = emit("reset", "", struct{}{})
				return
			}
		}
		if len(batch.Events) > 0 || time.Since(renewed) >= liveHeartbeat {
			id, err := encodeCursor(box, bot, batch.Position, batch.Scope, contracts.LiveHistoryKey{})
			if err != nil || emit("batch", id, struct {
				Events  []contracts.DecisionRunEvent
				LeaseMS int64
			}{batch.Events, liveLease.Milliseconds()}) != nil {
				return
			}
			renewed = time.Now()
		}
		cursor.Position = batch.Position
		select {
		case <-r.Context().Done():
			return
		case <-wake:
		case <-ticker.C:
		}
	}
}

func (h *Handler) auditRecordAccess(ctx context.Context, bot int64, conversation string) (bool, bool) {
	return h.svcCtx.DecisionAuditScope(ctx, bot, conversation)
}

func (h *Handler) auditAccountStamp(ctx context.Context, bot int64) (string, bool) {
	return h.svcCtx.DecisionAuditStamp(ctx, bot)
}

func (h *Handler) auditStampCurrent(ctx context.Context, bot int64, expected string) bool {
	stamp, allowed := h.auditAccountStamp(ctx, bot)
	return allowed && stamp == expected
}

func (h *Handler) auditSourceAllowed(ctx context.Context, bot int64, conversation string, message int64) bool {
	repo, ok := h.svcCtx.Decisions.(contracts.LiveDecisionReader)
	if !ok {
		return true
	}
	if !h.accountAuditAllowed(ctx, bot) {
		return false
	}
	allowed, err := repo.DecisionSourceAccess(ctx, bot, conversation, message)
	return err == nil && allowed
}

func (h *Handler) allowedHistorySources(ctx context.Context, record contracts.DecisionRecord, messages []map[string]any) []map[string]any {
	repo, ok := h.svcCtx.Decisions.(contracts.LiveDecisionReader)
	if !ok || len(messages) == 0 {
		return messages
	}
	if !h.accountAuditAllowed(ctx, record.BotID) {
		return nil
	}
	ids := make([]int64, 0, len(messages))
	for _, message := range messages {
		if id, ok := message["MessageID"].(int64); ok && id != 0 {
			ids = append(ids, id)
		}
	}
	allowed, err := repo.DecisionHistorySources(ctx, record.BotID, record.ConversationID, ids)
	if err != nil {
		return nil
	}
	out := make([]map[string]any, 0, len(messages))
	for _, message := range messages {
		if id, ok := message["MessageID"].(int64); ok && allowed[id] {
			out = append(out, message)
		}
	}
	return out
}

func (h *Handler) auditReplayAllowed(ctx context.Context, decision int64) bool {
	if _, ok := h.svcCtx.Decisions.(contracts.LiveDecisionReader); !ok {
		return true
	}
	record, err := h.svcCtx.Decisions.Get(ctx, decision)
	if err != nil {
		return false
	}
	visible, history := h.auditRecordAccess(ctx, record.BotID, record.ConversationID)
	if !visible || !history {
		return false
	}
	source, ok := h.svcCtx.Decisions.(snapshotSource)
	if !ok {
		return false
	}
	snapshot, err := source.Snapshot(ctx, decision)
	if err != nil {
		// Let the replay service preserve its existing missing-snapshot response.
		// 由回放服务保留已有的缺快照响应。
		return errors.Is(err, domain.ErrSnapshotNotFound)
	}
	messages := snapshotContextMessages(snapshot.Input)
	if snapshot.Input.Topics != nil {
		if ref := snapshot.Input.Topics.Reference; ref != nil {
			messages = append(messages, contextMessageView(*ref, false))
		}
		for _, candidate := range snapshot.Input.Topics.Candidates {
			for _, message := range candidate.History {
				messages = append(messages, contextMessageView(message, false))
			}
		}
	}
	return len(h.allowedHistorySources(ctx, record, messages)) == len(messages)
}
