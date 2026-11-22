package memory

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/HappyLadySauceM/Nagi/cmd/app/types/common"
	"github.com/HappyLadySauceM/Nagi/internal/memory/contracts"
	"github.com/HappyLadySauceM/Nagi/pkg/qq"
)

func (h *Handler) consentAccount(w http.ResponseWriter, r *http.Request) (int64, bool) {
	if !h.requireDB(w) {
		return 0, false
	}
	if middlewareActor(r) != "admin" {
		common.WriteFailure(w, http.StatusForbidden, "account administrator required")
		return 0, false
	}
	if r.URL.Query().Get("bot_id") == "" {
		common.WriteFailure(w, http.StatusBadRequest, "bot_id required")
		return 0, false
	}
	bot, err := h.svcCtx.ResolveBotID(r.URL.Query().Get("bot_id"))
	if err != nil {
		writeConsentError(w, contracts.ErrConsentInvalid)
		return 0, false
	}
	if h.svcCtx.Accounts == nil {
		writeConsentError(w, contracts.ErrConsentMissing)
		return 0, false
	}
	if _, ok := h.svcCtx.Accounts.Lookup(bot); !ok {
		writeConsentError(w, contracts.ErrConsentMissing)
		return 0, false
	}
	return bot, true
}

func decodeConsentRequest(w http.ResponseWriter, r *http.Request, body any) bool {
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16384))
	decoder.DisallowUnknownFields()
	if decoder.Decode(body) != nil {
		return false
	}
	return errors.Is(decoder.Decode(new(json.RawMessage)), io.EOF)
}

func (h *Handler) CreateConsent(w http.ResponseWriter, r *http.Request) {
	bot, ok := h.consentAccount(w, r)
	if !ok {
		return
	}
	if h.svcCtx.Config == nil || !h.svcCtx.Config.Memory.CrossGroupConsentWrite {
		writeConsentError(w, contracts.ErrConsentDisabled)
		return
	}
	var body struct {
		MemoryID       string  `json:"memory_id"`
		FactVersion    int64   `json:"expected_fact_version"`
		TargetGroups   []int64 `json:"target_group_ids"`
		IdempotencyKey string  `json:"idempotency_key"`
	}
	if !decodeConsentRequest(w, r, &body) {
		writeConsentError(w, contracts.ErrConsentInvalid)
		return
	}
	limit := qq.MaxTextRunes
	if h.svcCtx.SettingsProvider != nil {
		limits := h.svcCtx.SettingsProvider.Settings().Agent.Sending
		for _, n := range []int{limits.MaxCharsPerMessage, limits.MaxTotalCharsPerRun} {
			if n > 0 {
				limit = min(limit, n)
			}
		}
	}
	proposal, err := h.svcCtx.Memory.CreateConsent(r.Context(), contracts.CreateConsent{BotID: bot, MemoryID: body.MemoryID, FactVersion: body.FactVersion, TargetGroups: body.TargetGroups, IdempotencyKey: body.IdempotencyKey, MaxPromptRunes: limit})
	if err != nil {
		writeConsentError(w, err)
		return
	}
	common.WriteSuccess(w, proposal)
}

func (h *Handler) Consent(w http.ResponseWriter, r *http.Request) {
	bot, ok := h.consentAccount(w, r)
	if !ok {
		return
	}
	proposal, err := h.svcCtx.Memory.Consent(r.Context(), bot, r.PathValue("id"))
	if err != nil {
		writeConsentError(w, err)
		return
	}
	common.WriteSuccess(w, proposal)
}

func (h *Handler) RevokeConsent(w http.ResponseWriter, r *http.Request) {
	bot, ok := h.consentAccount(w, r)
	if !ok {
		return
	}
	var body struct {
		ExpectedStateVersion int64 `json:"expected_state_version"`
		TargetGroupID        int64 `json:"target_group_id"`
	}
	if !decodeConsentRequest(w, r, &body) {
		writeConsentError(w, contracts.ErrConsentInvalid)
		return
	}
	proposal, err := h.svcCtx.Memory.RevokeConsent(r.Context(), bot, r.PathValue("id"), body.ExpectedStateVersion, body.TargetGroupID)
	if err != nil {
		writeConsentError(w, err)
		return
	}
	common.WriteSuccess(w, proposal)
}

func writeConsentError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, contracts.ErrConsentInvalid):
		common.WriteFailure(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, contracts.ErrConsentMissing):
		common.WriteFailure(w, http.StatusNotFound, err.Error())
	case errors.Is(err, contracts.ErrConsentDisabled):
		common.WriteFailure(w, http.StatusForbidden, err.Error())
	case errors.Is(err, contracts.ErrConsentConflict):
		common.WriteFailure(w, http.StatusConflict, err.Error())
	default:
		common.WriteFailure(w, http.StatusInternalServerError, "memory consent operation failed")
	}
}
