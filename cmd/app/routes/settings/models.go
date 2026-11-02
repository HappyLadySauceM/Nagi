package settings

import (
	"encoding/json"
	"errors"
	"github.com/HappyLadySauceM/Nagi/cmd/app/middleware"
	"github.com/HappyLadySauceM/Nagi/cmd/app/types/common"
	app "github.com/HappyLadySauceM/Nagi/internal/settings/application"
	"net/http"
)

func (h *Handler) registerModels(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/models", func(w http.ResponseWriter, r *http.Request) {
		if h.requireService(w) {
			common.WriteSuccess(w, h.svcCtx.Settings.Models())
		}
	})
	mux.HandleFunc("POST /api/v1/models", h.saveModel)
	mux.HandleFunc("PUT /api/v1/models/{id}", h.saveModel)
	mux.HandleFunc("DELETE /api/v1/models/{id}", func(w http.ResponseWriter, r *http.Request) {
		if !h.requireService(w) {
			return
		}
		var body struct{ Version int64 }
		if !decodeModelBody(w, r, &body) {
			return
		}
		v, err := h.svcCtx.Settings.DeleteModel(r.Context(), r.PathValue("id"), body.Version, middleware.ActorFrom(r.Context()))
		h.modelResult(w, v, err)
	})
	mux.HandleFunc("PATCH /api/v1/models", func(w http.ResponseWriter, r *http.Request) {
		if !h.requireService(w) {
			return
		}
		var body struct {
			Defaults    map[string]string
			Cookie      *string
			ClearCookie bool
			Version     int64
		}
		if !decodeModelBody(w, r, &body) {
			return
		}
		v, err := h.svcCtx.Settings.ConfigureModels(r.Context(), body.Defaults, body.Cookie, body.ClearCookie, body.Version, middleware.ActorFrom(r.Context()))
		h.modelResult(w, v, err)
	})
	mux.HandleFunc("PUT /api/v1/models/selection", func(w http.ResponseWriter, r *http.Request) {
		if !h.requireService(w) {
			return
		}
		var body struct {
			BotID    int64
			Kind     string
			TargetID int64
			ModelID  string
			Version  int64
		}
		if !decodeModelBody(w, r, &body) {
			return
		}
		err := h.svcCtx.Settings.SelectModel(r.Context(), body.BotID, body.Kind, body.TargetID, body.ModelID, body.Version, middleware.ActorFrom(r.Context()))
		h.modelResult(w, h.svcCtx.Settings.Models(), err)
	})
	mux.HandleFunc("PATCH /api/v1/settings", h.patchSettings)
}
func decodeModelBody(w http.ResponseWriter, r *http.Request, v any) bool {
	d := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxRequestBytes))
	d.DisallowUnknownFields()
	if err := d.Decode(v); err != nil {
		common.WriteFailure(w, 400, "invalid request body")
		return false
	}
	return true
}
func (h *Handler) saveModel(w http.ResponseWriter, r *http.Request) {
	if !h.requireService(w) {
		return
	}
	var body app.ModelEdit
	if !decodeModelBody(w, r, &body) {
		return
	}
	v, err := h.svcCtx.Settings.SaveModel(r.Context(), r.PathValue("id"), body, middleware.ActorFrom(r.Context()))
	h.modelResult(w, v, err)
}
func (h *Handler) modelResult(w http.ResponseWriter, v any, err error) {
	if err == nil {
		common.WriteSuccess(w, v)
		return
	}
	if errors.Is(err, app.ErrConflict) {
		common.WriteFailure(w, 409, "configuration changed; reload before saving")
		return
	}
	common.WriteFailure(w, 400, err.Error())
}
func (h *Handler) patchSettings(w http.ResponseWriter, r *http.Request) {
	if !h.requireService(w) {
		return
	}
	var body struct {
		Version  int64
		Sections map[string]json.RawMessage
	}
	if !decodeModelBody(w, r, &body) {
		return
	}
	next := h.svcCtx.Settings.View().Settings
	for section, raw := range body.Sections {
		var target any
		switch section {
		case "AI":
			target = &next.AI
		case "Intent":
			target = &next.Intent
		case "Agent":
			target = &next.Agent
		case "Messages":
			target = &next.Messages
		case "Tools":
			target = &next.Tools
		default:
			common.WriteFailure(w, 400, "invalid settings section")
			return
		}
		d := json.Unmarshal(raw, target)
		if d != nil {
			common.WriteFailure(w, 400, "invalid settings section")
			return
		}
	}
	h.apply(w, r, next, body.Version)
}
