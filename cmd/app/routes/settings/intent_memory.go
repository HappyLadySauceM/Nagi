package settings

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"github.com/HappyLadySauceM/Nagi/cmd/app/middleware"
	"github.com/HappyLadySauceM/Nagi/cmd/app/types/common"
	settingsapp "github.com/HappyLadySauceM/Nagi/internal/settings/application"
)

func (h *Handler) intentMemoryGrant(w http.ResponseWriter, r *http.Request) {
	if !h.requireService(w) {
		return
	}
	actor := middleware.ActorFrom(r.Context())
	if actor != "admin" && actor != "local" {
		common.WriteFailure(w, http.StatusForbidden, "需要账号管理权限")
		return
	}
	botID, botErr := strconv.ParseInt(r.PathValue("bot"), 10, 64)
	groupID, groupErr := strconv.ParseInt(r.PathValue("group"), 10, 64)
	if botErr != nil || groupErr != nil || botID <= 0 || groupID <= 0 {
		common.WriteFailure(w, http.StatusBadRequest, "账号或群编号无效")
		return
	}
	if h.svcCtx.Accounts == nil {
		common.WriteFailure(w, http.StatusNotFound, "账号不存在")
		return
	}
	if _, ok := h.svcCtx.Accounts.Lookup(botID); !ok {
		common.WriteFailure(w, http.StatusNotFound, "账号不存在")
		return
	}
	if r.Method == http.MethodGet {
		allowed, destination, version, err := h.svcCtx.Settings.IntentMemoryGrant(r.Context(), botID, groupID)
		if err != nil {
			h.writeError(w, err)
			return
		}
		common.WriteSuccess(w, map[string]any{"Allow": allowed, "Destination": destination, "Version": version})
		return
	}
	var input struct {
		Allow       *bool
		Destination string
		Version     int64
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil || input.Allow == nil {
		common.WriteFailure(w, http.StatusBadRequest, "需要明确指定披露许可")
		return
	}
	version, err := h.svcCtx.Settings.SetIntentMemoryGrant(r.Context(), botID, groupID, *input.Allow, input.Destination, input.Version, actor)
	if errors.Is(err, settingsapp.ErrIntentMemoryDestination) {
		common.WriteFailure(w, http.StatusConflict, "意图模型目的地不可用或已变更，请刷新后重试")
		return
	}
	if err != nil {
		h.writeError(w, err)
		return
	}
	common.WriteSuccess(w, map[string]any{"Version": version})
}
