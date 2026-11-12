// Package intent exposes the recorded intent decisions and their reasons.
// Package intent 提供已记录的意图决策及其原因。
package intent

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/HappyLadySauceM/Nagi/cmd/app/svc"
	"github.com/HappyLadySauceM/Nagi/cmd/app/types/common"
	intentapp "github.com/HappyLadySauceM/Nagi/internal/intent/application"
	intentcontracts "github.com/HappyLadySauceM/Nagi/internal/intent/contracts"
	intentdomain "github.com/HappyLadySauceM/Nagi/internal/intent/domain"
	intentinfra "github.com/HappyLadySauceM/Nagi/internal/intent/infrastructure"
)

// maxDecisionPageSize bounds one page of decisions.
// maxDecisionPageSize 限定单页返回的决策数量。
const maxDecisionPageSize = 100

// Handler serves the decision log endpoints.
// Handler 提供决策记录接口。
type Handler struct {
	svcCtx *svc.ServiceContext
}

// NewHandler builds the intent handler.
// NewHandler 构建意图处理器。
func NewHandler(svcCtx *svc.ServiceContext) *Handler {
	return &Handler{svcCtx: svcCtx}
}

// Register wires routes using Go 1.22 method+path patterns.
// Register 以 Go 1.22 的方法+路径模式注册路由。
func (h *Handler) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/intent/overview", h.Overview)
	mux.HandleFunc("GET /api/v1/intent/flow", h.Flow)
	mux.HandleFunc("GET /api/v1/intent/decisions", h.List)
	mux.HandleFunc("GET /api/v1/intent/decisions/{id}", h.Get)
	mux.HandleFunc("POST /api/v1/intent/decisions/{id}/recalculate", h.Recalculate)
	mux.HandleFunc("POST /api/v1/intent/decisions/{id}/replay-model", h.ReplayModel)
	mux.HandleFunc("POST /api/v1/intent/decisions/{id}/replay-model/estimate", h.EstimateReplayModel)
	mux.HandleFunc("POST /api/v1/intent/decisions/{id}/replay-evaluations", h.CreateReplayEvaluation)
	mux.HandleFunc("GET /api/v1/intent/replay-evaluations", h.ListReplayEvaluations)
	mux.HandleFunc("GET /api/v1/intent/replay-evaluations/{id}", h.GetReplayEvaluation)
	mux.HandleFunc("GET /api/v1/intent/replay-evaluations/{id}/comparison", h.CompareReplayEvaluation)
	h.RegisterRules(mux)
	h.RegisterPrompts(mux)
}

// decisionInfo is the list representation of one decision.
// decisionInfo 是决策的列表表示。
type decisionInfo struct {
	ID             int64
	JobID          int64
	BotID          int64
	ConversationID string
	Decision       string
	ReasonCode     string
	Provider       string
	Model          string
	RulesetVersion string
	RunID          string
	FlowVersion    string
	SnapshotStatus string
	MissingFields  []string
	LatencyMS      int
	CreatedAt      time.Time
}

// decisionDetail adds the explainable steps to the list representation.
// decisionDetail 在列表表示之上附带可解释步骤。
type decisionDetail struct {
	decisionInfo
	Scores map[string]float64
	Steps  []stepInfo
	Run    intentcontracts.Run
}

// stepInfo is one explainable stage of a decision.
// stepInfo 是决策中的一个可解释阶段。
type stepInfo struct {
	Index      int
	Phase      string
	ReasonCode string
	Detail     map[string]any
	CreatedAt  time.Time
}

// List returns the newest decisions of one account.
// List 返回某账号最新的决策记录。
func (h *Handler) List(w http.ResponseWriter, r *http.Request) {
	botID, err := strconv.ParseInt(r.URL.Query().Get("bot_id"), 10, 64)
	if err != nil || botID <= 0 {
		common.WriteFailure(w, http.StatusBadRequest, "bot_id 必填且必须为正整数")
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
	records, err := h.svcCtx.Decisions.List(r.Context(), intentcontracts.DecisionQuery{
		BotID:          botID,
		ConversationID: r.URL.Query().Get("conversation_id"),
		Limit:          limit,
		BeforeID:       beforeID,
		ReasonCode:     r.URL.Query().Get("reason_code"),
	})
	if err != nil {
		common.WriteFailure(w, http.StatusInternalServerError, "读取决策记录失败")
		return
	}
	items := make([]decisionInfo, 0, len(records))
	for _, record := range records {
		items = append(items, toInfo(record))
	}
	common.WriteSuccess(w, map[string]any{"Items": items, "Limit": limit})
}

// Get returns one decision with its explainable steps.
// Get 返回一条决策及其可解释步骤。
func (h *Handler) Get(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		common.WriteFailure(w, http.StatusBadRequest, "决策 id 无效")
		return
	}
	record, err := h.svcCtx.Decisions.Get(r.Context(), id)
	if err != nil {
		if errors.Is(err, intentinfra.ErrDecisionNotFound) {
			common.WriteFailure(w, http.StatusNotFound, "决策记录不存在")
			return
		}
		common.WriteFailure(w, http.StatusInternalServerError, "读取决策详情失败")
		return
	}
	delivery := h.deliveryFor(r.Context(), record.BotID, record.JobID)
	detail := decisionDetail{decisionInfo: toInfo(record), Scores: record.Scores, Run: intentapp.BuildRunFromRecord(record, delivery)}
	for _, step := range record.Steps {
		detail.Steps = append(detail.Steps, stepInfo{
			Index: step.Index, Phase: step.Phase, ReasonCode: step.ReasonCode, Detail: step.Detail, CreatedAt: step.CreatedAt,
		})
	}
	common.WriteSuccess(w, detail)
}

func toInfo(record intentcontracts.DecisionRecord) decisionInfo {
	return decisionInfo{
		ID:             record.ID,
		JobID:          record.JobID,
		BotID:          record.BotID,
		ConversationID: record.ConversationID,
		Decision:       record.Decision,
		ReasonCode:     record.ReasonCode,
		Provider:       record.Provider,
		Model:          record.Model,
		RulesetVersion: record.RulesetVersion,
		RunID:          record.RunID,
		FlowVersion:    record.FlowVersion,
		SnapshotStatus: record.SnapshotStatus,
		MissingFields:  record.MissingFields,
		LatencyMS:      record.LatencyMS,
		CreatedAt:      record.CreatedAt,
	}
}

// Flow returns the fixed read-only execution flow definition.
// Flow 返回固定的只读执行流程定义。
func (h *Handler) Flow(w http.ResponseWriter, r *http.Request) {
	common.WriteSuccess(w, intentdomain.FixedFlow())
}

// deliveryFor reads the independent outbound outcome of one decision job.
// deliveryFor 读取一条决策任务独立的出站结果。
func (h *Handler) deliveryFor(ctx context.Context, botID, jobID int64) intentcontracts.Delivery {
	out := intentcontracts.Delivery{Items: []intentcontracts.DeliveryItem{}}
	if h.svcCtx.Outbound == nil {
		return out
	}
	owns, err := h.svcCtx.Outbound.OwnsJob(ctx, botID, jobID)
	if err != nil || !owns {
		return out
	}
	outcome, reason, denied, err := h.svcCtx.Outbound.RunOutcome(ctx, jobID)
	if err == nil {
		out.Outcome, out.Reason, out.Denied = outcome, reason, denied
	}
	rows, err := h.svcCtx.Outbound.ListByJob(ctx, jobID)
	if err != nil {
		return out
	}
	for _, row := range rows {
		out.Items = append(out.Items, intentcontracts.DeliveryItem{
			ID: row.ID, Sequence: row.Sequence, Status: string(row.Status), Text: row.Text,
			PlatformMessageID: row.PlatformMessageID, ErrorCode: row.ErrorCode,
		})
	}
	return out
}

// Recalculate re-runs policy and rules from a stored snapshot without model calls.
// Recalculate 使用已保存快照重跑策略与规则，不调用模型。
func (h *Handler) Recalculate(w http.ResponseWriter, r *http.Request) {
	h.replay(w, r, false)
}

// ReplayModel calls the current semantic model with a stored input snapshot.
// ReplayModel 使用已保存输入快照调用当前语义模型。
func (h *Handler) ReplayModel(w http.ResponseWriter, r *http.Request) {
	h.replay(w, r, true)
}

func (h *Handler) replay(w http.ResponseWriter, r *http.Request, model bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		common.WriteFailure(w, http.StatusBadRequest, "决策 id 无效")
		return
	}
	if h.svcCtx.IntentReplayer == nil {
		common.WriteFailure(w, http.StatusServiceUnavailable, "决策回放不可用")
		return
	}
	if model {
		var body struct {
			ConfirmationToken string
			Name              string
			Note              string
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil && !errors.Is(err, io.EOF) {
			common.WriteFailure(w, http.StatusBadRequest, "请求体不合法")
			return
		}
		evaluation, err := h.svcCtx.IntentReplayer.ReplayModelConfirmed(r.Context(), id, body.ConfirmationToken, body.Name, body.Note)
		if err != nil {
			h.replayEvaluationError(w, err)
			return
		}
		common.WriteSuccess(w, map[string]any{
			"Run": evaluation.After, "Cost": evaluation.Cost, "CostEstimate": evaluation.Cost.Text,
			"EvaluationID": evaluation.ID, "Evaluation": evaluation,
		})
		return
	}
	before, after, changed, err := h.svcCtx.IntentReplayer.Recalculate(r.Context(), id)
	if err != nil {
		h.replayError(w, err)
		return
	}
	common.WriteSuccess(w, map[string]any{"Before": before, "After": after, "Changed": changed})
}

func (h *Handler) replayError(w http.ResponseWriter, err error) {
	if errors.Is(err, intentapp.ErrSnapshotMissing) {
		common.WriteFailure(w, http.StatusConflict, "snapshot_missing")
		return
	}
	common.WriteFailure(w, http.StatusInternalServerError, "决策回放失败")
}

func (h *Handler) Overview(w http.ResponseWriter, r *http.Request) {
	botID, err := strconv.ParseInt(r.URL.Query().Get("bot_id"), 10, 64)
	if err != nil || botID <= 0 {
		common.WriteFailure(w, 400, "invalid bot_id")
		return
	}
	hours := 24
	if raw := r.URL.Query().Get("hours"); raw != "" {
		hours, err = strconv.Atoi(raw)
		if err != nil || hours < 1 || hours > 720 {
			common.WriteFailure(w, 400, "invalid time range")
			return
		}
	}
	reader, ok := h.svcCtx.Decisions.(intentcontracts.DecisionStatsReader)
	if !ok {
		common.WriteFailure(w, 503, "decision statistics unavailable")
		return
	}
	stats, err := reader.Stats(r.Context(), botID, time.Now().Add(-time.Duration(hours)*time.Hour))
	if err != nil {
		common.WriteFailure(w, 500, "read decision statistics failed")
		return
	}
	common.WriteSuccess(w, stats)
}
