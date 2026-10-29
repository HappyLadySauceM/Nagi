// Package intent exposes the recorded intent decisions and their reasons.
// Package intent 提供已记录的意图决策及其原因。
package intent

import (
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/HappyLadySauceM/Nagi/cmd/app/svc"
	"github.com/HappyLadySauceM/Nagi/cmd/app/types/common"
	intentcontracts "github.com/HappyLadySauceM/Nagi/internal/intent/contracts"
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
	mux.HandleFunc("GET /api/v1/intent/decisions", h.List)
	mux.HandleFunc("GET /api/v1/intent/decisions/{id}", h.Get)
	h.RegisterRules(mux)
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
	LatencyMS      int
	CreatedAt      time.Time
}

// decisionDetail adds the explainable steps to the list representation.
// decisionDetail 在列表表示之上附带可解释步骤。
type decisionDetail struct {
	decisionInfo
	Scores map[string]float64
	Steps  []stepInfo
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
	detail := decisionDetail{decisionInfo: toInfo(record), Scores: record.Scores}
	for _, step := range record.Steps {
		detail.Steps = append(detail.Steps, stepInfo{
			Index:      step.Index,
			Phase:      step.Phase,
			ReasonCode: step.ReasonCode,
			Detail:     step.Detail,
			CreatedAt:  step.CreatedAt,
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
		LatencyMS:      record.LatencyMS,
		CreatedAt:      record.CreatedAt,
	}
}
