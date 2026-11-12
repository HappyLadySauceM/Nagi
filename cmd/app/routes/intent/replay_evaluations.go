// Admin API for saved replay evaluations and paid model confirmation.
// 已保存回放评估与付费模型确认的管理 API。
package intent

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"

	"github.com/HappyLadySauceM/Nagi/cmd/app/types/common"
	intentapp "github.com/HappyLadySauceM/Nagi/internal/intent/application"
	intentcontracts "github.com/HappyLadySauceM/Nagi/internal/intent/contracts"
	intentinfra "github.com/HappyLadySauceM/Nagi/internal/intent/infrastructure"
)

// EstimateReplayModel returns the server-side cost and token estimate before a
// paid model replay. The response carries a short-lived confirmation token.
// EstimateReplayModel 在付费模型回放前返回服务端费用与 token 估算；
// 响应携带短期确认令牌。
func (h *Handler) EstimateReplayModel(w http.ResponseWriter, r *http.Request) {
	id, err := decisionIDFromPath(r)
	if err != nil {
		common.WriteFailure(w, http.StatusBadRequest, "决策 id 无效")
		return
	}
	if h.svcCtx.IntentReplayer == nil {
		common.WriteFailure(w, http.StatusServiceUnavailable, "决策回放不可用")
		return
	}
	estimate, err := h.svcCtx.IntentReplayer.EstimateModelReplay(r.Context(), id)
	if err != nil {
		h.replayEvaluationError(w, err)
		return
	}
	common.WriteSuccess(w, estimate)
}

// CreateReplayEvaluation runs a local recalculation or a confirmed model
// replay and persists the result as an independent evaluation record.
// CreateReplayEvaluation 执行本地重算或已确认的模型回放，并保存为独立评估记录。
func (h *Handler) CreateReplayEvaluation(w http.ResponseWriter, r *http.Request) {
	id, err := decisionIDFromPath(r)
	if err != nil {
		common.WriteFailure(w, http.StatusBadRequest, "决策 id 无效")
		return
	}
	if h.svcCtx.IntentReplayer == nil {
		common.WriteFailure(w, http.StatusServiceUnavailable, "决策回放不可用")
		return
	}
	var body struct {
		Mode              string
		ConfirmationToken string
		Name              string
		Note              string
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil && !errors.Is(err, io.EOF) {
		common.WriteFailure(w, http.StatusBadRequest, "请求体不合法")
		return
	}
	var evaluation intentcontracts.ReplayEvaluation
	switch body.Mode {
	case intentcontracts.ReplayModeRecalculate:
		evaluation, err = h.svcCtx.IntentReplayer.SaveRecalculateEvaluation(r.Context(), id, body.Name, body.Note)
	case intentcontracts.ReplayModeModel:
		evaluation, err = h.svcCtx.IntentReplayer.ReplayModelConfirmed(r.Context(), id, body.ConfirmationToken, body.Name, body.Note)
	case "":
		common.WriteFailure(w, http.StatusBadRequest, "mode 必须为 recalculate 或 model")
		return
	default:
		common.WriteFailure(w, http.StatusBadRequest, "mode 必须为 recalculate 或 model")
		return
	}
	if err != nil {
		h.replayEvaluationError(w, err)
		return
	}
	common.WriteSuccess(w, evaluation)
}

// ListReplayEvaluations returns saved evaluation summaries for one bot or decision.
// ListReplayEvaluations 返回某个账号或决策的已保存评估摘要。
func (h *Handler) ListReplayEvaluations(w http.ResponseWriter, r *http.Request) {
	if h.svcCtx.IntentReplayer == nil {
		common.WriteFailure(w, http.StatusServiceUnavailable, "决策回放不可用")
		return
	}
	botID, err := parseOptionalPositiveInt64(r.URL.Query().Get("bot_id"))
	if err != nil {
		common.WriteFailure(w, http.StatusBadRequest, "bot_id 必须为正整数")
		return
	}
	decisionID, err := parseOptionalPositiveInt64(r.URL.Query().Get("decision_id"))
	if err != nil {
		common.WriteFailure(w, http.StatusBadRequest, "decision_id 必须为正整数")
		return
	}
	if botID == 0 && decisionID == 0 {
		common.WriteFailure(w, http.StatusBadRequest, "bot_id 或 decision_id 至少提供一个")
		return
	}
	limit := 20
	if raw := r.URL.Query().Get("limit"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed <= 0 || parsed > maxDecisionPageSize {
			common.WriteFailure(w, http.StatusBadRequest, "limit 必须在 1 到 100 之间")
			return
		}
		limit = parsed
	}
	var beforeID int64
	if raw := r.URL.Query().Get("before_id"); raw != "" {
		parsed, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || parsed < 0 {
			common.WriteFailure(w, http.StatusBadRequest, "before_id 必须为非负整数")
			return
		}
		beforeID = parsed
	}
	items, err := h.svcCtx.IntentReplayer.ListEvaluations(r.Context(), intentcontracts.ReplayEvaluationQuery{
		BotID: botID, DecisionID: decisionID, Limit: limit, BeforeID: beforeID,
	})
	if err != nil {
		h.replayEvaluationError(w, err)
		return
	}
	common.WriteSuccess(w, map[string]any{"Items": items, "Limit": limit})
}

// GetReplayEvaluation returns one saved evaluation with both runs.
// GetReplayEvaluation 返回一条已保存评估及两次运行。
func (h *Handler) GetReplayEvaluation(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		common.WriteFailure(w, http.StatusBadRequest, "评估 id 无效")
		return
	}
	if h.svcCtx.IntentReplayer == nil {
		common.WriteFailure(w, http.StatusServiceUnavailable, "决策回放不可用")
		return
	}
	evaluation, err := h.svcCtx.IntentReplayer.GetEvaluation(r.Context(), id)
	if err != nil {
		h.replayEvaluationError(w, err)
		return
	}
	common.WriteSuccess(w, evaluation)
}

// CompareReplayEvaluation compares a saved evaluation with its original run or
// with another saved evaluation.
// CompareReplayEvaluation 将已保存评估与原始运行或另一条已保存评估对比。
func (h *Handler) CompareReplayEvaluation(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		common.WriteFailure(w, http.StatusBadRequest, "评估 id 无效")
		return
	}
	againstID, err := parseOptionalPositiveInt64(r.URL.Query().Get("against_evaluation_id"))
	if err != nil {
		common.WriteFailure(w, http.StatusBadRequest, "against_evaluation_id 必须为正整数")
		return
	}
	if h.svcCtx.IntentReplayer == nil {
		common.WriteFailure(w, http.StatusServiceUnavailable, "决策回放不可用")
		return
	}
	comparison, err := h.svcCtx.IntentReplayer.CompareEvaluation(r.Context(), id, againstID)
	if err != nil {
		h.replayEvaluationError(w, err)
		return
	}
	common.WriteSuccess(w, comparison)
}

func (h *Handler) replayEvaluationError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, intentapp.ErrSnapshotMissing):
		common.WriteFailure(w, http.StatusConflict, "snapshot_missing")
	case errors.Is(err, intentapp.ErrReplayConfirmationRequired):
		common.WriteFailure(w, http.StatusPreconditionRequired, "confirmation_required")
	case errors.Is(err, intentapp.ErrReplayConfirmationExpired):
		common.WriteFailure(w, http.StatusConflict, "confirmation_expired")
	case errors.Is(err, intentapp.ErrReplayConfirmationStale):
		common.WriteFailure(w, http.StatusConflict, "confirmation_stale")
	case errors.Is(err, intentinfra.ErrReplayEvaluationNotFound):
		common.WriteFailure(w, http.StatusNotFound, "回放评估不存在")
	case errors.Is(err, intentapp.ErrReplayEvaluationStoreDown):
		common.WriteFailure(w, http.StatusServiceUnavailable, "回放评估存储不可用")
	default:
		common.WriteFailure(w, http.StatusInternalServerError, "回放评估失败")
	}
}

func decisionIDFromPath(r *http.Request) (int64, error) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		return 0, errors.New("invalid decision id")
	}
	return id, nil
}

func parseOptionalPositiveInt64(raw string) (int64, error) {
	if raw == "" {
		return 0, nil
	}
	value, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || value <= 0 {
		return 0, errors.New("invalid positive integer")
	}
	return value, nil
}
