// Package audit exposes administrator-only local AI audit facts.
// Package audit 提供仅管理员可访问的本地 AI 审计事实。
package audit

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/HappyLadySauceM/Nagi/cmd/app/svc"
	"github.com/HappyLadySauceM/Nagi/cmd/app/types/common"
	v1 "github.com/HappyLadySauceM/Nagi/cmd/app/types/v1"
	auditdomain "github.com/HappyLadySauceM/Nagi/internal/audit/domain"
	intentcontracts "github.com/HappyLadySauceM/Nagi/internal/intent/contracts"
	pluginsapp "github.com/HappyLadySauceM/Nagi/internal/plugins/application"
	scopedomain "github.com/HappyLadySauceM/Nagi/internal/scope/domain"
	"github.com/google/uuid"
)

const maxRunPageSize = 100

// Handler serves durable worker-run audit records.
// Handler 提供持久化的 worker 运行审计记录。
type Handler struct{ svcCtx *svc.ServiceContext }

// NewHandler builds the local audit handler.
// NewHandler 构建本地审计处理器。
func NewHandler(svcCtx *svc.ServiceContext) *Handler { return &Handler{svcCtx: svcCtx} }

// Register wires the read-only audit endpoints.
// Register 注册只读审计端点。
func (h *Handler) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/audit/runs", h.ListRuns)
	mux.HandleFunc("GET /api/v1/audit/runs/{id}", h.GetRun)
	mux.HandleFunc("GET /api/v1/audit/artifacts/{id}", h.GetArtifact)
}

// ListRuns returns the newest runs of an account, optionally narrowed to a
// stable conversation or one task.
// ListRuns 返回某账号最新运行，可按稳定会话或单个任务收窄。
func (h *Handler) ListRuns(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if h.svcCtx.AIAudit == nil {
		common.WriteFailure(w, http.StatusServiceUnavailable, "AI 审计存储不可用")
		return
	}
	botID, err := h.svcCtx.ResolveBotID(r.URL.Query().Get("bot_id"))
	if err != nil {
		common.WriteFailure(w, http.StatusBadRequest, "bot_id 无效或缺失")
		return
	}
	stamp, allowedAccount := h.svcCtx.DecisionAuditStamp(r.Context(), botID)
	if !allowedAccount {
		common.WriteFailure(w, http.StatusNotFound, "AI 审计运行记录不存在")
		return
	}
	query := auditdomain.AIRunQuery{BotID: botID, ConversationID: strings.TrimSpace(r.URL.Query().Get("conversation_id")), Limit: 20}
	if query.ConversationID != "" {
		if _, err := uuid.Parse(query.ConversationID); err != nil {
			common.WriteFailure(w, http.StatusBadRequest, "conversation_id 必须为 UUID")
			return
		}
	}
	if raw := r.URL.Query().Get("job_id"); raw != "" {
		query.JobID, err = strconv.ParseInt(raw, 10, 64)
		if err != nil || query.JobID <= 0 {
			common.WriteFailure(w, http.StatusBadRequest, "job_id 必须为正整数")
			return
		}
	}
	if raw := r.URL.Query().Get("limit"); raw != "" {
		query.Limit, err = strconv.Atoi(raw)
		if err != nil || query.Limit < 1 || query.Limit > maxRunPageSize {
			common.WriteFailure(w, http.StatusBadRequest, "limit 必须在 1 到 100 之间")
			return
		}
	}
	runs, err := h.svcCtx.AIAudit.ListRuns(r.Context(), query)
	if err != nil {
		common.WriteFailure(w, http.StatusInternalServerError, "读取 AI 审计运行记录失败")
		return
	}
	allowed := runs[:0]
	for _, run := range runs {
		if visible, _ := h.svcCtx.DecisionAuditScope(r.Context(), run.BotID, run.ConversationID); visible {
			allowed = append(allowed, run)
		}
	}
	runs = allowed
	if current, ok := h.svcCtx.DecisionAuditStamp(r.Context(), botID); !ok || current != stamp {
		common.WriteFailure(w, http.StatusNotFound, "AI 审计运行记录不存在")
		return
	}
	common.WriteSuccess(w, map[string]any{"items": runs, "limit": query.Limit})
}

// GetRun returns a complete run including ordered operation events and media
// metadata. Media bytes are intentionally not exposed here.
// GetRun 返回含有有序操作和媒体元数据的完整运行，不在此暴露媒体字节。
func (h *Handler) GetRun(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if h.svcCtx.AIAudit == nil {
		common.WriteFailure(w, http.StatusServiceUnavailable, "AI 审计存储不可用")
		return
	}
	id := r.PathValue("id")
	if _, err := uuid.Parse(id); err != nil {
		common.WriteFailure(w, http.StatusBadRequest, "运行 ID 必须为 UUID")
		return
	}
	run, err := h.svcCtx.AIAudit.GetRun(r.Context(), id)
	if errors.Is(err, auditdomain.ErrNotFound) {
		common.WriteFailure(w, http.StatusNotFound, "AI 审计运行记录不存在")
		return
	}
	if err != nil {
		common.WriteFailure(w, http.StatusInternalServerError, "读取 AI 审计运行详情失败")
		return
	}
	stamp, allowed := h.svcCtx.DecisionAuditStamp(r.Context(), run.BotID)
	visible, history := h.svcCtx.DecisionAuditScope(r.Context(), run.BotID, run.ConversationID)
	if !allowed || !visible {
		common.WriteFailure(w, http.StatusNotFound, "AI 审计运行记录不存在")
		return
	}
	if repo, live := h.svcCtx.Decisions.(intentcontracts.LiveDecisionReader); live {
		h.projectRun(r.Context(), repo, &run, history)
	}
	if current, ok := h.svcCtx.DecisionAuditStamp(r.Context(), run.BotID); !ok || current != stamp {
		common.WriteFailure(w, http.StatusNotFound, "AI 审计运行记录不存在")
		return
	}
	common.WriteSuccess(w, run)
}

// GetArtifact returns durable media metadata and adds a short-lived read URL
// when the object is uploaded and still retained.
// GetArtifact 返回持久化媒体元数据，并在对象已上传且未到期时附加短时读取地址。
func (h *Handler) GetArtifact(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if h.svcCtx.AIAudit == nil {
		common.WriteFailure(w, http.StatusServiceUnavailable, "AI 审计存储不可用")
		return
	}
	id := r.PathValue("id")
	if _, err := uuid.Parse(id); err != nil {
		common.WriteFailure(w, http.StatusBadRequest, "媒体工件 ID 必须为 UUID")
		return
	}
	artifact, err := h.svcCtx.AIAudit.GetArtifact(r.Context(), id)
	if errors.Is(err, auditdomain.ErrNotFound) {
		common.WriteFailure(w, http.StatusNotFound, "媒体工件不存在")
		return
	}
	if err != nil {
		common.WriteFailure(w, http.StatusInternalServerError, "读取媒体工件失败")
		return
	}
	var account int64
	var stamp string
	var verifyPlayback func() bool
	if repo, live := h.svcCtx.Decisions.(intentcontracts.LiveDecisionReader); live {
		run, err := h.svcCtx.AIAudit.GetRun(r.Context(), artifact.RunID)
		if err != nil {
			common.WriteFailure(w, http.StatusNotFound, "媒体工件不存在")
			return
		}
		account = run.BotID
		var allowed bool
		stamp, allowed = h.svcCtx.DecisionAuditStamp(r.Context(), account)
		visible, history := h.svcCtx.DecisionAuditScope(r.Context(), account, run.ConversationID)
		verifyPlayback = func() bool { return h.artifactUsable(r.Context(), repo, artifact, run) }
		if !allowed || !visible || !history || artifact.Status != "uploaded" || !verifyPlayback() {
			common.WriteFailure(w, http.StatusNotFound, "媒体工件不存在")
			return
		}
	}
	response := v1.ArtifactResponse{AIArtifact: artifact}
	if h.svcCtx.MediaArchive != nil {
		url, signErr := h.svcCtx.MediaArchive.Presign(r.Context(), artifact)
		if signErr != nil {
			common.WriteFailure(w, http.StatusServiceUnavailable, "媒体临时访问地址不可用")
			return
		}
		response.URL = url
	}
	if account != 0 {
		if !verifyPlayback() {
			common.WriteFailure(w, http.StatusNotFound, "媒体工件不存在")
			return
		}
		if current, allowed := h.svcCtx.DecisionAuditStamp(r.Context(), account); !allowed || current != stamp {
			common.WriteFailure(w, http.StatusNotFound, "媒体工件不存在")
			return
		}
		response.ObjectKey, response.Bucket, response.SHA256 = "", "", ""
	}
	common.WriteSuccess(w, response)
}

func (h *Handler) sourceRetained(ctx context.Context, repo intentcontracts.LiveDecisionReader, run auditdomain.AIRun) bool {
	if h.svcCtx.Conversation == nil {
		return false
	}
	in, err := h.svcCtx.Conversation.JobIngress(ctx, run.BotID, run.JobID)
	if err != nil || in == nil || in.Message.ConversationID != run.ConversationID {
		return false
	}
	allowed, err := repo.DecisionSourceAccess(ctx, run.BotID, run.ConversationID, in.Message.ID)
	return err == nil && allowed
}

func (h *Handler) artifactUsable(ctx context.Context, repo intentcontracts.LiveDecisionReader, artifact auditdomain.AIArtifact, run auditdomain.AIRun) bool {
	if artifact.RunID != run.ID || artifact.EventID == "" || artifact.ExpiresAt == nil || !artifact.ExpiresAt.After(time.Now()) {
		return false
	}
	stage := ""
	switch artifact.Kind {
	case "audio.inbound.derived":
		stage = "media.voice.transcribe"
	case "audio.outbound.synthesized":
		stage = "media.voice.synthesize"
	default:
		return false
	}
	for _, event := range run.Events {
		if event.ID != artifact.EventID || event.RunID != run.ID || event.Stage != stage {
			continue
		}
		if stage == "media.voice.transcribe" {
			return h.sourceRetained(ctx, repo, run)
		}
		if event.Status != "succeeded" || h.svcCtx.Conversation == nil || h.svcCtx.Scope == nil {
			return false
		}
		// The only synthesis caller sends voice to the current conversation.
		// 唯一合成调用方的语音目标限定为当前会话。
		in, err := h.svcCtx.Conversation.JobIngress(ctx, run.BotID, run.JobID)
		if err != nil || in == nil || in.Message.ConversationID != run.ConversationID || in.TargetID <= 0 {
			return false
		}
		kind := scopedomain.ScopeType(in.Kind)
		if !kind.Valid() {
			return false
		}
		allowed, err := repo.DecisionSourceAccess(ctx, run.BotID, run.ConversationID, in.Message.ID)
		if err != nil || !allowed {
			return false
		}
		policy, err := h.svcCtx.Scope.Resolve(ctx, run.BotID, kind, in.TargetID)
		if err != nil || !policy.Enabled || !policy.StoreMessages || !policy.AllowHistoryRead || policy.ReplyMode == scopedomain.ModeObserveOnly || h.svcCtx.Settings == nil || h.svcCtx.Tools == nil {
			return false
		}
		defaults, err := h.svcCtx.Settings.CurrentPermissionDefaults(ctx)
		if err != nil {
			return false
		}
		granted := pluginsapp.ResolvePermissions(h.svcCtx.Tools.Snapshot(), defaults, policy.ToolPermissions)
		return granted["media.voice.send"] && granted["qq.reply.current"] && granted["qq.content.media"]
	}
	return false
}

func (h *Handler) projectRun(ctx context.Context, repo intentcontracts.LiveDecisionReader, run *auditdomain.AIRun, history bool) {
	retained := history && h.sourceRetained(ctx, repo, *run)
	for i := range run.Events {
		event := &run.Events[i]
		var input, output map[string]json.RawMessage
		_ = json.Unmarshal(event.Input, &input)
		_ = json.Unmarshal(event.Output, &output)
		event.Input, event.Output = nil, nil
		switch event.Stage {
		case "agent.generation":
			event.Output = auditMetadata(output, "model", "input_usage", "output_usage")
		case "intent.decision":
			var decision map[string]json.RawMessage
			if json.Unmarshal(output["decision"], &decision) == nil {
				if fields := auditMetadata(decision, "RunID"); len(fields) != 0 {
					event.Output, _ = json.Marshal(map[string]json.RawMessage{"decision": fields})
				}
			}
		case "agent.context":
			if !retained {
				continue
			}
			var ids []int64
			if json.Unmarshal(output["history_message_ids"], &ids) != nil || len(ids) == 0 {
				continue
			}
			allowed, err := repo.DecisionHistorySources(ctx, run.BotID, run.ConversationID, ids)
			if err != nil {
				continue
			}
			filtered := make([]int64, 0, len(ids))
			for _, id := range ids {
				if allowed[id] {
					filtered = append(filtered, id)
				}
			}
			event.Output, _ = json.Marshal(map[string][]int64{"history_message_ids": filtered})
		case "tool.execute":
			event.Input = auditMetadata(input, "tool")
		}
	}
	for i := range run.Artifacts {
		artifact := &run.Artifacts[i]
		if artifact.Status == "uploaded" && (!retained || !h.artifactUsable(ctx, repo, *artifact, *run)) {
			artifact.Status = "redacted"
		}
		artifact.ObjectKey, artifact.Bucket, artifact.SHA256 = "", "", ""
	}
}

func auditMetadata(values map[string]json.RawMessage, keys ...string) []byte {
	keep := make(map[string]json.RawMessage)
	for _, key := range keys {
		value := values[key]
		if len(value) == 0 {
			continue
		}
		switch key {
		case "RunID":
			var id string
			if json.Unmarshal(value, &id) != nil {
				continue
			}
			if _, err := uuid.Parse(id); err != nil {
				continue
			}
		case "input_usage", "output_usage":
			var count int64
			if json.Unmarshal(value, &count) != nil || count < 0 {
				continue
			}
		default:
			var text string
			if json.Unmarshal(value, &text) != nil || len(text) > 128 || strings.ContainsAny(text, " \r\n\t") {
				continue
			}
		}
		keep[key] = value
	}
	if len(keep) == 0 {
		return nil
	}
	raw, _ := json.Marshal(keep)
	return raw
}
