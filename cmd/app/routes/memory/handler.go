// Package memory serves long-term memory management endpoints.
// Package memory 提供长期记忆管理接口。
package memory

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/google/uuid"

	"github.com/HappyLadySauceM/Nagi/cmd/app/middleware"
	"github.com/HappyLadySauceM/Nagi/cmd/app/svc"
	"github.com/HappyLadySauceM/Nagi/cmd/app/types/common"
	v1 "github.com/HappyLadySauceM/Nagi/cmd/app/types/v1"
	conversationdomain "github.com/HappyLadySauceM/Nagi/internal/conversation/domain"
	memoryapp "github.com/HappyLadySauceM/Nagi/internal/memory/application"
	memorycontracts "github.com/HappyLadySauceM/Nagi/internal/memory/contracts"
	memorydomain "github.com/HappyLadySauceM/Nagi/internal/memory/domain"
	scopedomain "github.com/HappyLadySauceM/Nagi/internal/scope/domain"
	settingsapp "github.com/HappyLadySauceM/Nagi/internal/settings/application"
	settingsdomain "github.com/HappyLadySauceM/Nagi/internal/settings/domain"
)

// middlewareActor reads the authenticated actor for approval records.
// middlewareActor 读取已认证身份，用于审批记录。
func middlewareActor(r *http.Request) string {
	return middleware.ActorFrom(r.Context())
}

// Handler serves the memory management endpoints.
// Handler 提供记忆管理接口。
type Handler struct {
	svcCtx *svc.ServiceContext
}

// NewHandler builds the memory handler.
// NewHandler 构建记忆处理器。
func NewHandler(svcCtx *svc.ServiceContext) *Handler {
	return &Handler{svcCtx: svcCtx}
}

// Register wires routes using Go 1.22 method+path patterns.
// Register 以 Go 1.22 的方法+路径模式注册路由。
func (h *Handler) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/memories", h.List)
	mux.HandleFunc("GET /api/v1/memories/{id}/management", h.Management)
	mux.HandleFunc("GET /api/v1/memories/{id}", h.Get)
	mux.HandleFunc("POST /api/v1/memories", h.Create)
	mux.HandleFunc("POST /api/v1/memories/{candidate_id}/supersede", h.Supersede)
	mux.HandleFunc("PATCH /api/v1/memories/{id}", h.Patch)
	mux.HandleFunc("DELETE /api/v1/memories/{id}", h.Delete)
	mux.HandleFunc("POST /api/v1/memories/recall-preview", h.RecallPreview)
	mux.HandleFunc("GET /api/v1/memory-extraction/settings", h.GetExtractionSettings)
	mux.HandleFunc("PUT /api/v1/memory-extraction/settings", h.PutExtractionSettings)
	mux.HandleFunc("GET /api/v1/memory-extraction/jobs", h.ListExtractionJobs)
	mux.HandleFunc("POST /api/v1/memory-extraction/jobs/{id}/retry", h.RetryExtractionJob)
}

// requireDB replies with 503 when the database is not configured.
// requireDB 在未配置数据库时返回 503。
func (h *Handler) requireDB(w http.ResponseWriter) bool {
	if h.svcCtx.Memory == nil {
		common.WriteFailure(w, http.StatusServiceUnavailable, "database not configured")
		return false
	}
	return true
}

// validMemoryID rejects malformed path identifiers before any storage call.
// validMemoryID 在进入存储层前拒绝格式非法的路径 ID。
func validMemoryID(id string) bool {
	_, err := uuid.Parse(id)
	return err == nil
}

// List returns one page of memories filtered by visibility, owner, status or search.
// List 返回按可见性、归属、状态或搜索过滤的一页记忆。
func (h *Handler) List(w http.ResponseWriter, r *http.Request) {
	if !h.requireDB(w) {
		return
	}
	q := r.URL.Query()
	f := memorydomain.ListFilter{
		Visibility: memorydomain.Visibility(q.Get("visibility")),
		OwnerKey:   q.Get("owner_key"),
		Status:     memorydomain.Status(q.Get("status")),
		Search:     q.Get("search"),
	}
	if raw := q.Get("bot_id"); raw != "" {
		if v, err := strconv.ParseInt(raw, 10, 64); err == nil {
			f.BotID = &v
		}
	}
	f.Page, _ = strconv.Atoi(q.Get("page"))
	f.Limit, _ = strconv.Atoi(q.Get("limit"))
	items, total, err := h.svcCtx.Memory.List(r.Context(), f)
	if err != nil {
		common.WriteFailure(w, http.StatusInternalServerError, "list memories failed")
		return
	}
	common.WriteSuccess(w, map[string]any{"items": items, "total": total, "page": f.Page, "limit": f.Limit})
}

// RecallPreview runs the bounded recall ranker without writing memory or
// sending a message. Example mode uses synthetic records only.
// RecallPreview 在不写记忆、不发消息的前提下运行有界召回排序；
// example 模式只使用合成记录。
func (h *Handler) RecallPreview(w http.ResponseWriter, r *http.Request) {
	var body v1.RecallPreviewRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		common.WriteFailure(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if body.Kind == "" {
		body.Kind = body.TargetType
	}
	switch body.PreviewMode {
	case "example", "real":
	default:
		common.WriteFailure(w, http.StatusBadRequest, "preview_mode must be example or real")
		return
	}
	if body.PreviewMode == "real" && (body.BotID <= 0 || body.TargetID <= 0 || (body.Kind != "group" && body.Kind != "private")) {
		common.WriteFailure(w, http.StatusBadRequest, "bot_id, kind and target_id are required")
		return
	}
	if body.PreviewMode == "example" {
		common.WriteSuccess(w, exampleRecallPreview(body))
		return
	}
	if h.svcCtx.Memory == nil || h.svcCtx.Scope == nil {
		common.WriteFailure(w, http.StatusServiceUnavailable, "database not configured")
		return
	}
	policy, err := h.svcCtx.Scope.Resolve(r.Context(), body.BotID, scopedomain.ScopeType(body.Kind), body.TargetID)
	if err != nil {
		common.WriteFailure(w, http.StatusInternalServerError, "resolve scope policy failed")
		return
	}
	active := policy.Enabled && policy.StoreMessages && policy.ReplyMode != scopedomain.ModeObserveOnly
	if !active || !policy.AllowMemoryRead {
		reason := "memory_not_authorized"
		if !active {
			reason = "scope_disabled"
		}
		common.WriteSuccess(w, map[string]any{
			"Items": []memorycontracts.RecallItem{}, "NoKeyword": false, "Limit": normalizedRecallLimit(body.Limit),
			"Reason": reason, "Skipped": true,
		})
		return
	}
	conversationID := strings.TrimSpace(body.ConversationID)
	if conversationID != "" {
		if h.svcCtx.Conversation == nil {
			common.WriteSuccess(w, map[string]any{
				"Items": []memorycontracts.RecallItem{}, "NoKeyword": false, "Limit": normalizedRecallLimit(body.Limit),
				"Reason": "conversation_unavailable", "Skipped": true,
			})
			return
		}
		conversation, found, err := h.svcCtx.Conversation.GetConversation(r.Context(), body.BotID, conversationdomain.Kind(body.Kind), body.TargetID)
		if err != nil {
			common.WriteFailure(w, http.StatusInternalServerError, "read conversation failed")
			return
		}
		if !found {
			common.WriteSuccess(w, map[string]any{
				"Items": []memorycontracts.RecallItem{}, "NoKeyword": false, "Limit": normalizedRecallLimit(body.Limit),
				"Reason": "conversation_not_found", "Skipped": true,
			})
			return
		}
		if conversation.ID != conversationID {
			common.WriteSuccess(w, map[string]any{
				"Items": []memorycontracts.RecallItem{}, "NoKeyword": false, "Limit": normalizedRecallLimit(body.Limit),
				"Reason": "conversation_target_mismatch", "Skipped": true,
			})
			return
		}
		conversationID = conversation.ID
	}
	result, err := h.svcCtx.Memory.Recall(r.Context(), memorycontracts.RecallRequest{
		BotID: body.BotID, Kind: body.Kind, TargetID: body.TargetID,
		ConversationID: conversationID, Query: body.Query, Limit: body.Limit,
		SpeakerType: body.SpeakerType, SpeakerID: body.SpeakerID,
		SubjectType: body.SubjectType, SubjectID: body.SubjectID,
		SubjectSource: body.SubjectSource, SubjectEvidence: body.SubjectEvidence,
		SubjectAmbiguous: body.SubjectAmbiguous,
	})
	if err != nil {
		common.WriteFailure(w, http.StatusBadRequest, "invalid recall request")
		return
	}
	common.WriteSuccess(w, result)
}

func normalizedRecallLimit(limit int) int {
	if limit <= 0 {
		return 3
	}
	if limit > 5 {
		return 5
	}
	return limit
}

func exampleRecallPreview(body v1.RecallPreviewRequest) memorycontracts.RecallResult {
	kind := body.Kind
	if kind != "group" && kind != "private" {
		kind = "group"
	}
	// Example preview is always synthetic; never borrow a caller target id.
	// 示例预览始终使用合成目标，绝不借用调用方传入的真实目标 ID。
	targetID := int64(1000)
	memories := []memorydomain.Memory{
		{ID: "example-memory-1", Visibility: memorydomain.VisibilityBotShared, Content: "示例记忆：这位用户喜欢喝美式咖啡。", SubjectType: memorydomain.SubjectUnknown},
	}
	hits, noKeyword, reason := memorydomain.RankRecall(memories, memorydomain.RecallQuery{
		Kind: kind, TargetID: targetID, Query: body.Query,
		SubjectType: memorydomain.SubjectType(body.SubjectType), SubjectID: body.SubjectID,
		Limit: normalizedRecallLimit(body.Limit),
	})
	result := memorycontracts.RecallResult{Items: make([]memorycontracts.RecallItem, 0, len(hits)), NoKeyword: noKeyword, Reason: reason, Limit: normalizedRecallLimit(body.Limit)}
	for _, hit := range hits {
		result.Items = append(result.Items, memorycontracts.RecallItem{
			MemoryID: hit.Memory.ID, Content: hit.Memory.Content, Visibility: string(hit.Memory.Visibility),
			SubjectType: string(hit.Memory.SubjectType), SubjectID: hit.Memory.SubjectID,
			Score: hit.Score, HitKeywords: hit.HitKeywords, SubjectMatch: hit.SubjectMatch, SelectionReason: hit.SelectionReason,
		})
	}
	return result
}

// Get returns one memory only when it is active and visible to the requested
// bot and conversation scope. This is an administrator management endpoint: it
// deliberately does not apply the conversation-level allow_memory_read grant.
// Source metadata is returned only when stored on the entry; the handler never
// invents or resolves message bodies.
// Get 仅在记忆激活且对请求账号、会话可见时返回该条记忆。
// 这是管理员管理接口，刻意不套用会话级 allow_memory_read 授权。
// 只有条目自身已存来源信息时才返回来源；处理器不编造、也不展开原始消息正文。
func (h *Handler) Get(w http.ResponseWriter, r *http.Request) {
	if !h.requireDB(w) {
		return
	}
	if !validMemoryID(r.PathValue("id")) {
		common.WriteFailure(w, http.StatusBadRequest, "invalid memory id")
		return
	}
	q := r.URL.Query()
	botID, err := strconv.ParseInt(q.Get("bot_id"), 10, 64)
	if err != nil || botID <= 0 {
		common.WriteFailure(w, http.StatusBadRequest, "bot_id is required")
		return
	}
	kind := q.Get("kind")
	scopeID, err := strconv.ParseInt(q.Get("scope_id"), 10, 64)
	if err != nil || scopeID <= 0 || (kind != "group" && kind != "private") {
		common.WriteFailure(w, http.StatusBadRequest, "kind and scope_id are required")
		return
	}
	item, found, err := h.svcCtx.Memory.RecallDetail(r.Context(), memorycontracts.DetailRequest{
		BotID: botID, Kind: kind, TargetID: scopeID,
		ConversationID: q.Get("conversation_id"), MemoryID: r.PathValue("id"),
	})
	if err != nil {
		if errors.Is(err, memoryapp.ErrInvalidID) {
			common.WriteFailure(w, http.StatusBadRequest, "invalid memory id")
			return
		}
		common.WriteFailure(w, http.StatusInternalServerError, "read memory failed")
		return
	}
	if !found {
		common.WriteFailure(w, http.StatusNotFound, "memory not found")
		return
	}
	common.WriteSuccess(w, item)
}

// Supersede explicitly replaces one active memory with a candidate. It never
// infers an old memory from content similarity.
// Supersede 显式用一个候选替代一条 active 记忆，绝不按正文相似度推断旧记忆。
func (h *Handler) Supersede(w http.ResponseWriter, r *http.Request) {
	if !h.requireDB(w) {
		return
	}
	candidateID := r.PathValue("candidate_id")
	if !validMemoryID(candidateID) {
		common.WriteFailure(w, http.StatusBadRequest, "invalid candidate memory id")
		return
	}
	var body v1.SupersedeRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		common.WriteFailure(w, http.StatusUnprocessableEntity, "replacement_invalid")
		return
	}
	result, err := h.svcCtx.Memory.Supersede(r.Context(), memorydomain.SupersedeInput{
		NewMemoryID: candidateID, OldMemoryID: body.OldMemoryID,
		Actor: middlewareActor(r), Reason: body.Reason, IdempotencyKey: body.IdempotencyKey,
	})
	if err != nil {
		writeMemoryMutationError(w, err)
		return
	}
	common.WriteSuccess(w, result)
}

// Management returns one memory in any lifecycle state with its replacement
// relations for the administrator detail page.
// Management 返回任意生命周期状态的记忆及其替代关系，供管理员详情页使用。
func (h *Handler) Management(w http.ResponseWriter, r *http.Request) {
	if !h.requireDB(w) {
		return
	}
	memoryID := r.PathValue("id")
	if !validMemoryID(memoryID) {
		common.WriteFailure(w, http.StatusBadRequest, "invalid memory id")
		return
	}
	botID, err := strconv.ParseInt(r.URL.Query().Get("bot_id"), 10, 64)
	if err != nil || botID <= 0 {
		common.WriteFailure(w, http.StatusBadRequest, "bot_id is required")
		return
	}
	memory, replacements, found, err := h.svcCtx.Memory.Management(r.Context(), memoryID, botID)
	if err != nil {
		if errors.Is(err, memoryapp.ErrInvalidID) {
			common.WriteFailure(w, http.StatusBadRequest, "invalid memory id")
			return
		}
		common.WriteFailure(w, http.StatusInternalServerError, "read memory management failed")
		return
	}
	if !found {
		common.WriteFailure(w, http.StatusNotFound, "memory_not_found")
		return
	}
	common.WriteSuccess(w, map[string]any{"Memory": managementMemoryViewOf(memory), "Replacements": replacements})
}

func managementMemoryViewOf(memory memorydomain.Memory) v1.ManagementMemoryView {
	view := v1.ManagementMemoryView{
		ID: memory.ID, BotID: memory.BotID, Status: memory.Status, Visibility: memory.Visibility,
		OwnerKey: memory.OwnerKey, Content: memory.Content, Source: memory.Source,
		SourceMessageID: memory.SourceMessageID, SourceSenderID: memory.SourceSenderID, Confidence: memory.Confidence,
		SubjectType: memory.SubjectType, SubjectID: memory.SubjectID,
		Origin: memory.Origin, ApprovedBy: memory.ApprovedBy, CreatedAt: memory.CreatedAt, UpdatedAt: memory.UpdatedAt,
	}
	if !memory.ExpiresAt.IsZero() {
		expiresAt := memory.ExpiresAt
		view.ExpiresAt = &expiresAt
	}
	return view
}

func writeMemoryMutationError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, memoryapp.ErrMemoryNotFound):
		common.WriteFailure(w, http.StatusNotFound, "memory_not_found")
	case errors.Is(err, memoryapp.ErrMemoryStatusConflict):
		common.WriteFailure(w, http.StatusConflict, "memory_status_conflict")
	case errors.Is(err, memoryapp.ErrSubjectMismatch):
		common.WriteFailure(w, http.StatusConflict, "subject_mismatch")
	case errors.Is(err, memoryapp.ErrSubjectAmbiguous):
		common.WriteFailure(w, http.StatusConflict, "subject_ambiguous")
	case errors.Is(err, memoryapp.ErrIdempotencyConflict):
		common.WriteFailure(w, http.StatusConflict, "idempotency_conflict")
	case errors.Is(err, memoryapp.ErrMemoryInvalid):
		common.WriteFailure(w, http.StatusBadRequest, "revise memory failed")
	case errors.Is(err, memoryapp.ErrReplacementInvalid), errors.Is(err, memoryapp.ErrInvalidID):
		common.WriteFailure(w, http.StatusUnprocessableEntity, "replacement_invalid")
	default:
		common.WriteFailure(w, http.StatusInternalServerError, "memory mutation failed")
	}
}

// Create stores a new memory, optionally approved by the acting administrator.
// Create 存储新记忆，可由当前操作的管理员直接批准。
func (h *Handler) Create(w http.ResponseWriter, r *http.Request) {
	if !h.requireDB(w) {
		return
	}
	var body v1.CreateRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		common.WriteFailure(w, http.StatusBadRequest, "invalid request body")
		return
	}
	id, duplicate, err := h.svcCtx.Memory.Propose(r.Context(), memoryapp.CandidateInput{
		BotID:          body.BotID,
		Visibility:     memorydomain.Visibility(body.Visibility),
		OwnerKey:       body.OwnerKey,
		Content:        body.Content,
		Source:         body.Source,
		SourceSenderID: body.SourceSenderID,
		Origin:         memorydomain.OriginManual,
		SubjectType:    memorydomain.SubjectType(body.SubjectType),
		SubjectID:      body.SubjectID,
		ReviewReason:   body.ReviewReason,
		Confidence:     body.Confidence,
	})
	if err != nil {
		common.WriteFailure(w, http.StatusBadRequest, "invalid memory")
		return
	}
	if body.Approve && !duplicate {
		if err := h.svcCtx.Memory.Approve(r.Context(), id, middlewareActor(r)); err != nil {
			common.WriteFailure(w, http.StatusInternalServerError, "approve memory failed")
			return
		}
	}
	common.WriteSuccess(w, map[string]any{"id": id, "duplicate": duplicate})
}

// statusAction is the effect of the PATCH status field on one memory.
// statusAction 表示 PATCH 的 status 字段对一条记忆产生的效果。
type statusAction int

const (
	statusNone statusAction = iota
	statusApprove
	statusDelete
)

// parseStatusAction validates the requested transition. Only "" (keep the
// current status), "active" (approve) and "deleted" (physical delete) are supported:
// the management API cannot move a memory back to candidate or superseded, and
// silently accepting those values would report success without any effect.
// parseStatusAction 校验状态变更：只支持 ""（保持不变）、active（审批）与 deleted（物理删除）。
// 管理接口无法把记忆改回 candidate 或 superseded，静默接受会谎报成功。
func parseStatusAction(status string) (statusAction, bool) {
	switch status {
	case "":
		return statusNone, true
	case "active":
		return statusApprove, true
	case "deleted":
		return statusDelete, true
	default:
		return statusNone, false
	}
}

func hasOnlyDeleteStatus(raw json.RawMessage) bool {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	opening, err := decoder.Token()
	if err != nil || opening != json.Delim('{') {
		return false
	}
	key, err := decoder.Token()
	if err != nil || key != "status" {
		return false
	}
	value, err := decoder.Token()
	return err == nil && value == "deleted" && !decoder.More()
}

// Patch revises or approves a memory, or deletes it without other edits.
// Patch 修订或审批一条记忆，删除须单独提交。
func (h *Handler) Patch(w http.ResponseWriter, r *http.Request) {
	if !h.requireDB(w) {
		return
	}
	id := r.PathValue("id")
	if !validMemoryID(id) {
		common.WriteFailure(w, http.StatusBadRequest, "invalid memory id")
		return
	}
	decoder := json.NewDecoder(r.Body)
	var raw json.RawMessage
	var body v1.PatchRequest
	if err := decoder.Decode(&raw); err != nil || json.Unmarshal(raw, &body) != nil {
		common.WriteFailure(w, http.StatusBadRequest, "invalid request body")
		return
	}
	action, ok := parseStatusAction(body.Status)
	if !ok {
		common.WriteFailure(w, http.StatusBadRequest, "unsupported status; use active or deleted")
		return
	}
	if action == statusDelete {
		if _, err := decoder.Token(); err != io.EOF {
			common.WriteFailure(w, http.StatusBadRequest, "invalid request body")
			return
		}
		if !hasOnlyDeleteStatus(raw) {
			common.WriteFailure(w, http.StatusBadRequest, "delete must be requested without other fields")
			return
		}
		if err := h.svcCtx.Memory.Delete(r.Context(), id); err != nil {
			writeMemoryMutationError(w, err)
			return
		}
		common.WriteSuccess(w, map[string]any{"id": id})
		return
	}
	var content string
	if body.Content != nil {
		content = *body.Content
	}
	var visibility memorydomain.Visibility
	if body.Visibility != nil {
		visibility = memorydomain.Visibility(*body.Visibility)
	}
	if action == statusApprove {
		if err := h.svcCtx.Memory.ApproveWithEdit(r.Context(), id, middlewareActor(r), content, visibility, body.OwnerKey); err != nil {
			writeMemoryMutationError(w, err)
			return
		}
		common.WriteSuccess(w, map[string]any{"id": id})
		return
	}
	if err := h.svcCtx.Memory.Revise(r.Context(), id, content, visibility, body.OwnerKey); err != nil {
		writeMemoryMutationError(w, err)
		return
	}
	common.WriteSuccess(w, map[string]any{"id": id})
}

// GetExtractionSettings returns the effective process-wide settings.
// GetExtractionSettings 返回进程级生效抽取配置。
func (h *Handler) GetExtractionSettings(w http.ResponseWriter, r *http.Request) {
	if h.svcCtx.Config == nil {
		common.WriteFailure(w, http.StatusServiceUnavailable, "config unavailable")
		return
	}
	var view settingsapp.View
	if h.svcCtx.Settings != nil {
		view = h.svcCtx.Settings.View()
	}
	h.writeExtractionSettings(w, view)
}

func (h *Handler) PutExtractionSettings(w http.ResponseWriter, r *http.Request) {
	if h.svcCtx.Settings == nil || h.svcCtx.Config == nil || !h.svcCtx.Settings.View().Persisted {
		common.WriteFailure(w, http.StatusServiceUnavailable, "persistent settings unavailable")
		return
	}
	var body struct {
		settingsdomain.Extraction
		Version *int64
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 32<<10))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&body); err != nil || body.Version == nil || *body.Version < 0 {
		common.WriteFailure(w, http.StatusBadRequest, "invalid extraction settings request")
		return
	}
	view, err := h.svcCtx.Settings.UpdateExtraction(r.Context(), body.Extraction, *body.Version, middlewareActor(r))
	var invalid *settingsdomain.ValidationError
	switch {
	case err == nil:
		h.writeExtractionSettings(w, view)
	case errors.Is(err, settingsapp.ErrConflict):
		common.WriteFailure(w, http.StatusConflict, "settings changed; reload before saving")
	case errors.As(err, &invalid):
		common.WriteJSON(w, http.StatusBadRequest, common.Envelope{Code: 400, Message: "抽取设置校验未通过", Data: map[string]any{"errors": invalid.Fields}})
	default:
		common.WriteFailure(w, http.StatusInternalServerError, "save extraction settings failed")
	}
}

func (h *Handler) writeExtractionSettings(w http.ResponseWriter, view settingsapp.View) {
	settings := view.Settings.Extraction
	source := "database"
	if settings == nil {
		fallback := settingsapp.ExtractionFromConfig(h.svcCtx.Config.Memory.Extraction)
		settings = &fallback
		source = "config"
	}
	common.WriteSuccess(w, struct {
		settingsdomain.Extraction
		Scope    string
		Source   string
		Editable bool
		Version  int64
	}{*settings, "global", source, view.Persisted, view.Version})
}

// ListExtractionJobs returns a management page of memory extraction jobs.
// ListExtractionJobs 返回一页管理用记忆提取任务。
func (h *Handler) ListExtractionJobs(w http.ResponseWriter, r *http.Request) {
	if !h.requireDB(w) {
		return
	}
	q := r.URL.Query()
	botID, err := strconv.ParseInt(q.Get("bot_id"), 10, 64)
	if err != nil || botID <= 0 {
		common.WriteFailure(w, http.StatusBadRequest, "bot_id is required")
		return
	}
	status := q.Get("status")
	switch status {
	case "", "pending", "processing", "done", "failed", "cancelled":
	default:
		common.WriteFailure(w, http.StatusBadRequest, "invalid extraction job status")
		return
	}
	f := memorydomain.ExtractionJobFilter{
		BotID:          botID,
		ConversationID: q.Get("conversation_id"),
		Status:         status,
	}
	f.Page, _ = strconv.Atoi(q.Get("page"))
	f.Limit, _ = strconv.Atoi(q.Get("limit"))
	f = f.Normalized()
	items, total, err := h.svcCtx.Memory.ListExtractionJobs(r.Context(), f)
	if err != nil {
		common.WriteFailure(w, http.StatusInternalServerError, "list memory extraction jobs failed")
		return
	}
	common.WriteSuccess(w, map[string]any{"items": items, "total": total, "page": f.Page, "limit": f.Limit})
}

// RetryExtractionJob restarts one failed extraction job while its source and
// authorization generation remain valid.
// RetryExtractionJob 在来源与授权代际仍然有效时重启一个失败提取任务。
func (h *Handler) RetryExtractionJob(w http.ResponseWriter, r *http.Request) {
	if !h.requireDB(w) {
		return
	}
	id := r.PathValue("id")
	if !validMemoryID(id) {
		common.WriteFailure(w, http.StatusBadRequest, "invalid extraction job id")
		return
	}
	err := h.svcCtx.Memory.RetryExtractionJob(r.Context(), id)
	switch {
	case err == nil:
		common.WriteSuccess(w, map[string]any{"retried": true})
	case errors.Is(err, memoryapp.ErrInvalidID):
		common.WriteFailure(w, http.StatusBadRequest, "invalid extraction job id")
	case errors.Is(err, memoryapp.ErrExtractionJobNotFound):
		common.WriteFailure(w, http.StatusNotFound, "extraction job not found")
	case errors.Is(err, memoryapp.ErrExtractionSourceMissing):
		common.WriteFailure(w, http.StatusGone, "extraction source unavailable")
	case errors.Is(err, memoryapp.ErrExtractionJobState), errors.Is(err, memoryapp.ErrExtractionAuthorizationChanged), errors.Is(err, memoryapp.ErrExtractionRetryLimit):
		common.WriteFailure(w, http.StatusConflict, "extraction job not retryable")
	default:
		common.WriteFailure(w, http.StatusInternalServerError, "retry memory extraction job failed")
	}
}

// Delete permanently removes one memory.
// Delete 永久删除一条记忆。
func (h *Handler) Delete(w http.ResponseWriter, r *http.Request) {
	if !h.requireDB(w) {
		return
	}
	id := r.PathValue("id")
	if !validMemoryID(id) {
		common.WriteFailure(w, http.StatusBadRequest, "invalid memory id")
		return
	}
	if err := h.svcCtx.Memory.Delete(r.Context(), id); err != nil {
		if errors.Is(err, memoryapp.ErrMemoryNotFound) {
			common.WriteFailure(w, http.StatusNotFound, "memory not found")
			return
		}
		common.WriteFailure(w, http.StatusInternalServerError, "delete memory failed")
		return
	}
	common.WriteSuccess(w, map[string]any{"deleted": true})
}
