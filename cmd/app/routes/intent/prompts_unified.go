package intent

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/HappyLadySauceM/Nagi/cmd/app/svc"
	"github.com/HappyLadySauceM/Nagi/cmd/app/types/common"
	v1 "github.com/HappyLadySauceM/Nagi/cmd/app/types/v1"
	intentapp "github.com/HappyLadySauceM/Nagi/internal/intent/application"
	intentdomain "github.com/HappyLadySauceM/Nagi/internal/intent/domain"
	intentinfra "github.com/HappyLadySauceM/Nagi/internal/intent/infrastructure"
)

func writePromptSuccess(w http.ResponseWriter, status int, data any) {
	common.WriteJSON(w, status, common.Envelope{Code: status, Message: "success", Data: data})
}

func (h *Handler) RegisterUnifiedPrompts(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/prompts", h.ListUnifiedPrompts)
	// Go 1.22 ServeMux cannot register the contract's version path and the
	// prompt-id/diff path separately: {prompt_id}/diff overlaps versions/{id}.
	// A bounded catch-all dispatcher keeps the public paths exact.
	mux.HandleFunc("GET /api/v1/prompts/{path...}", h.DispatchUnifiedGet)
	mux.HandleFunc("POST /api/v1/prompts/{path...}", h.DispatchUnifiedPost)
	mux.HandleFunc("PATCH /api/v1/prompts/{path...}", h.DispatchUnifiedPatch)
	mux.HandleFunc("DELETE /api/v1/prompts/{path...}", h.DispatchUnifiedDelete)
}

func (h *Handler) DispatchUnifiedGet(w http.ResponseWriter, r *http.Request) {
	parts := splitPromptPath(r.PathValue("path"))
	switch {
	case len(parts) == 2 && parts[0] == "versions":
		h.GetUnifiedPromptVersion(w, r)
	case len(parts) == 1:
		h.GetUnifiedPrompt(w, r)
	case len(parts) == 2 && parts[1] == "diff":
		h.DiffUnifiedPrompt(w, r)
	case len(parts) == 2 && parts[1] == "effective":
		h.EffectiveUnifiedPrompt(w, r)
	default:
		common.WriteFailure(w, http.StatusNotFound, "Prompt 路由不存在")
	}
}

func (h *Handler) DispatchUnifiedPost(w http.ResponseWriter, r *http.Request) {
	parts := splitPromptPath(r.PathValue("path"))
	switch {
	case len(parts) == 2 && parts[1] == "versions":
		h.CreateUnifiedPromptDraft(w, r)
	case len(parts) == 3 && parts[0] == "versions" && parts[2] == "publish":
		h.PublishUnifiedPrompt(w, r)
	case len(parts) == 2 && parts[1] == "preview":
		h.PreviewUnifiedPrompt(w, r)
	case len(parts) == 2 && parts[1] == "sync":
		h.SyncUnifiedPrompt(w, r)
	case len(parts) == 2 && parts[1] == "rollback":
		h.RollbackUnifiedPrompt(w, r)
	case len(parts) == 2 && parts[1] == "restore-builtin":
		h.RestoreUnifiedBuiltin(w, r)
	default:
		common.WriteFailure(w, http.StatusNotFound, "Prompt 路由不存在")
	}
}

func (h *Handler) DispatchUnifiedPatch(w http.ResponseWriter, r *http.Request) {
	parts := splitPromptPath(r.PathValue("path"))
	if len(parts) == 2 && parts[0] == "versions" {
		h.UpdateUnifiedPromptDraft(w, r)
		return
	}
	common.WriteFailure(w, http.StatusNotFound, "Prompt 路由不存在")
}

func (h *Handler) DispatchUnifiedDelete(w http.ResponseWriter, r *http.Request) {
	parts := splitPromptPath(r.PathValue("path"))
	if len(parts) == 2 && parts[0] == "versions" {
		h.DeleteUnifiedPromptVersion(w, r)
		return
	}
	common.WriteFailure(w, http.StatusNotFound, "Prompt 路由不存在")
}

func splitPromptPath(raw string) []string {
	raw = strings.Trim(raw, "/")
	if raw == "" {
		return nil
	}
	return strings.Split(raw, "/")
}

func pathPromptID(r *http.Request) string {
	parts := pathPartsForPromptRequest(r)
	if len(parts) > 0 && parts[0] != "versions" {
		return parts[0]
	}
	return ""
}

func pathVersionID(r *http.Request) string {
	parts := pathPartsForPromptRequest(r)
	if len(parts) >= 2 && parts[0] == "versions" {
		return parts[1]
	}
	return ""
}

func pathPartsForPromptRequest(r *http.Request) []string {
	raw := strings.TrimPrefix(r.URL.Path, "/api/v1/prompts")
	return splitPromptPath(raw)
}

func (h *Handler) ListUnifiedPrompts(w http.ResponseWriter, r *http.Request) {
	if h.svcCtx.Prompts == nil {
		common.WriteFailure(w, http.StatusServiceUnavailable, "Prompt 存储不可用")
		return
	}
	records, err := h.svcCtx.Prompts.ListAll(r.Context())
	if err != nil {
		common.WriteFailure(w, http.StatusInternalServerError, "读取 Prompt 列表失败")
		return
	}
	botID, err := requiredBotID(r)
	if err != nil {
		common.WriteFailure(w, http.StatusBadRequest, err.Error())
		return
	}
	if raw := r.URL.Query().Get("scope_id"); raw != "" && raw != "0" {
		common.WriteFailure(w, http.StatusUnprocessableEntity, "scope_id 必须为 0")
		return
	}
	kindFilter := r.URL.Query().Get("kind")
	scopeFilter := r.URL.Query().Get("scope_type")
	providerFilter := r.URL.Query().Get("provider")
	if scopeFilter != "" && scopeFilter != intentdomain.PromptScopeAccount && scopeFilter != intentdomain.PromptScopeGroup && scopeFilter != intentdomain.PromptScopePrivate {
		common.WriteFailure(w, http.StatusBadRequest, "scope_type 必须为 account|group|private")
		return
	}
	items := []v1.UnifiedPromptSummary{}
	for _, promptID := range []string{intentdomain.PromptIDChatReply, intentdomain.PromptIDIntentParticipation, intentdomain.PromptIDMemoryExtraction, intentdomain.PromptIDMemoryRecall} {
		kind, _ := intentdomain.PromptKindForID(promptID)
		if kindFilter != "" && kind != kindFilter {
			continue
		}
		providers := []string{defaultPromptProvider(promptID)}
		if promptID == intentdomain.PromptIDIntentParticipation {
			if providerFilter != "" {
				if providerFilter != intentdomain.PromptProviderJev && providerFilter != intentdomain.PromptProviderOpenAI {
					common.WriteFailure(w, http.StatusBadRequest, "provider 必须为 jev 或 openai")
					return
				}
				providers = []string{providerFilter}
			} else {
				providers = []string{intentdomain.PromptProviderJev, intentdomain.PromptProviderOpenAI}
			}
		} else if providerFilter != "" && providerFilter != providers[0] {
			continue
		}
		for _, provider := range providers {
			builtin := intentdomain.BuiltinPromptFor(kind, provider)
			byLayer := map[string]v1.UnifiedPromptLayer{}
			for _, rec := range records {
				if rec.PromptID != promptID || rec.Provider != provider {
					continue
				}
				if rec.BotID != botID {
					continue
				}
				if kindFilter != "" && rec.Kind != kindFilter {
					continue
				}
				if scopeFilter != "" && rec.ScopeType != scopeFilter {
					continue
				}
				layer := byLayer[rec.ScopeType]
				if rec.Status == intentdomain.PromptStatusPublished {
					layer = v1.UnifiedPromptLayer{BotID: rec.BotID, ScopeType: rec.ScopeType, ScopeID: rec.ScopeID, ActiveVersion: rec.Version, ActiveVersionID: rec.ID, ActiveSource: rec.Source, Status: rec.Status, UpdatedAt: rec.PublishedAt}
				} else if layer.ActiveVersion == 0 {
					layer.BotID, layer.ScopeType, layer.ScopeID, layer.Status, layer.UpdatedAt = rec.BotID, rec.ScopeType, rec.ScopeID, rec.Status, rec.CreatedAt
				}
				byLayer[rec.ScopeType] = layer
			}
			layers := make([]v1.UnifiedPromptLayer, 0, len(byLayer))
			for _, layer := range byLayer {
				layers = append(layers, layer)
			}
			sort.Slice(layers, func(i, j int) bool { return layers[i].ScopeType < layers[j].ScopeType })
			items = append(items, v1.UnifiedPromptSummary{PromptID: promptID, Kind: kind, Name: builtin.Name, Provider: provider, BuiltinVersion: builtin.Version, Layers: layers})
		}
	}
	common.WriteSuccess(w, map[string]any{"Items": items})
}

func (h *Handler) GetUnifiedPrompt(w http.ResponseWriter, r *http.Request) {
	if h.svcCtx.Prompts == nil {
		common.WriteFailure(w, http.StatusServiceUnavailable, "Prompt 存储不可用")
		return
	}
	promptID := pathPromptID(r)
	kind, ok := intentdomain.PromptKindForID(promptID)
	if !ok {
		common.WriteFailure(w, http.StatusNotFound, "Prompt 不存在")
		return
	}
	provider := queryPromptProvider(r, promptID)
	if err := validatePromptProvider(promptID, provider); err != nil {
		common.WriteFailure(w, http.StatusBadRequest, err.Error())
		return
	}
	botID, err := requiredBotID(r)
	if err != nil {
		common.WriteFailure(w, http.StatusBadRequest, err.Error())
		return
	}
	scopeType, err := queryPromptScope(r)
	if err != nil {
		common.WriteFailure(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	records, err := h.svcCtx.Prompts.VersionsFor(r.Context(), promptID, kind, provider, botID, scopeType)
	if err != nil {
		common.WriteFailure(w, http.StatusInternalServerError, "读取 Prompt 版本失败")
		return
	}
	builtin := intentdomain.BuiltinPromptFor(kind, provider)
	versions := make([]v1.UnifiedPromptVersion, 0, len(records))
	active := (*v1.UnifiedPromptVersion)(nil)
	for _, rec := range records {
		item := toUnifiedPromptVersion(rec)
		versions = append(versions, item)
		if rec.Status == intentdomain.PromptStatusPublished && active == nil {
			active = &item
		}
	}
	var builtinView any = map[string]any{"Version": builtin.Version, "Status": "builtin", "Source": intentdomain.PromptSourceBuiltin, "Hash": builtin.Hash, "Content": intentdomain.PromptContentJSON(builtin)}
	common.WriteSuccess(w, map[string]any{"PromptID": promptID, "Kind": kind, "Provider": provider, "Builtin": builtinView, "Active": active, "Versions": versions})
}

func (h *Handler) GetUnifiedPromptVersion(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(pathVersionID(r), 10, 64)
	if err != nil || id <= 0 {
		common.WriteFailure(w, http.StatusBadRequest, "Prompt 版本 id 无效")
		return
	}
	if h.svcCtx.Prompts == nil {
		common.WriteFailure(w, http.StatusServiceUnavailable, "Prompt 存储不可用")
		return
	}
	rec, err := h.svcCtx.Prompts.Get(r.Context(), id)
	if errors.Is(err, intentinfra.ErrPromptNotFound) {
		common.WriteFailure(w, http.StatusNotFound, "Prompt 版本不存在")
		return
	}
	if err != nil {
		common.WriteFailure(w, http.StatusInternalServerError, "读取 Prompt 版本失败")
		return
	}
	common.WriteSuccess(w, toUnifiedPromptVersion(rec))
}

// DeleteUnifiedPromptVersion soft-deletes one draft or archived local version.
// Published and builtin versions are rejected; deleted versions disappear from
// admin list and detail queries while remaining readable for historical snapshots.
// DeleteUnifiedPromptVersion 软删除一个草稿或归档的本地版本；已发布与内置版本拒绝删除，
// 已删除版本从管理列表与详情中消失，但仍可按版本 ID 读取以支持历史快照。
func (h *Handler) DeleteUnifiedPromptVersion(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(pathVersionID(r), 10, 64)
	if err != nil || id <= 0 {
		common.WriteFailure(w, http.StatusBadRequest, "Prompt 版本 id 无效")
		return
	}
	if h.svcCtx.Prompts == nil {
		common.WriteFailure(w, http.StatusServiceUnavailable, "Prompt 存储不可用")
		return
	}
	botID, err := requiredBotID(r)
	if err != nil {
		common.WriteFailure(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := h.svcCtx.Prompts.DeleteVersion(r.Context(), id, botID); err != nil {
		switch {
		case errors.Is(err, intentinfra.ErrPromptNotFound):
			common.WriteFailure(w, http.StatusNotFound, "Prompt 版本不存在")
		case errors.Is(err, intentinfra.ErrPromptPublished):
			common.WriteFailure(w, http.StatusConflict, "已发布版本不可删除，请先恢复内置或发布其他版本")
		case errors.Is(err, intentinfra.ErrPromptBuiltin):
			common.WriteFailure(w, http.StatusConflict, "内置版本不可删除")
		case errors.Is(err, intentinfra.ErrPromptImmutable):
			common.WriteFailure(w, http.StatusConflict, "只有草稿或归档版本可以删除")
		default:
			common.WriteFailure(w, http.StatusInternalServerError, "删除 Prompt 版本失败")
		}
		return
	}
	common.WriteSuccess(w, map[string]any{"ID": id})
}

func (h *Handler) CreateUnifiedPromptDraft(w http.ResponseWriter, r *http.Request) {
	if h.svcCtx.Prompts == nil {
		common.WriteFailure(w, http.StatusServiceUnavailable, "Prompt 存储不可用")
		return
	}
	promptID := pathPromptID(r)
	kind, ok := intentdomain.PromptKindForID(promptID)
	if !ok {
		common.WriteFailure(w, http.StatusNotFound, "Prompt 不存在")
		return
	}
	var body struct {
		BotID         int64           `json:"bot_id"`
		ScopeType     string          `json:"scope_type"`
		ScopeID       int64           `json:"scope_id"`
		Provider      string          `json:"provider"`
		Name          string          `json:"name"`
		Content       json.RawMessage `json:"content"`
		Base          string          `json:"base"`
		BaseVersionID int64           `json:"base_version_id"`
		Note          string          `json:"note"`
		Source        string          `json:"source"`
		SourceRef     string          `json:"source_ref"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		common.WriteFailure(w, http.StatusBadRequest, "请求体不合法")
		return
	}
	if body.BotID <= 0 {
		common.WriteFailure(w, http.StatusBadRequest, "bot_id 必填")
		return
	}
	if body.ScopeID != 0 {
		common.WriteFailure(w, http.StatusUnprocessableEntity, "scope_id 必须为 0")
		return
	}
	scopeType := body.ScopeType
	if scopeType == "" {
		scopeType = intentdomain.PromptScopeAccount
	}
	if scopeType != intentdomain.PromptScopeAccount && scopeType != intentdomain.PromptScopeGroup && scopeType != intentdomain.PromptScopePrivate {
		common.WriteFailure(w, http.StatusBadRequest, "scope_type 必须为 account|group|private")
		return
	}
	provider := body.Provider
	if provider == "" {
		provider = defaultPromptProvider(promptID)
	}
	if err := validatePromptProvider(promptID, provider); err != nil {
		common.WriteFailure(w, http.StatusBadRequest, err.Error())
		return
	}
	var content *intentdomain.Prompt
	if len(body.Content) > 0 {
		var parsed intentdomain.Prompt
		var err error
		if scopeType == intentdomain.PromptScopeAccount {
			parsed, err = intentapp.DecodePromptRequestFor(kind, provider, body.Content)
		} else {
			parsed, err = intentapp.DecodePromptSupplementRequestFor(kind, provider, body.Content)
		}
		if err != nil {
			common.WriteFailure(w, http.StatusUnprocessableEntity, "Prompt 内容不合法")
			return
		}
		content = &parsed
	}
	if body.Source != "" && body.Source != intentdomain.PromptSourceLocal || body.SourceRef != "" {
		common.WriteFailure(w, http.StatusUnprocessableEntity, "远端来源由 Langfuse 同步记录")
		return
	}
	rec, err := h.svcCtx.Prompts.CreateDraftFor(r.Context(), promptID, kind, provider, body.BotID, scopeType, content, body.Base, body.BaseVersionID, body.Name, body.Note, intentdomain.PromptSourceLocal, "")
	if err != nil {
		common.WriteFailure(w, http.StatusUnprocessableEntity, "创建 Prompt 草稿失败")
		return
	}
	writePromptSuccess(w, http.StatusCreated, toUnifiedPromptVersion(rec))
}

func (h *Handler) UpdateUnifiedPromptDraft(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(pathVersionID(r), 10, 64)
	if err != nil || id <= 0 {
		common.WriteFailure(w, http.StatusBadRequest, "Prompt 版本 id 无效")
		return
	}
	if h.svcCtx.Prompts == nil {
		common.WriteFailure(w, http.StatusServiceUnavailable, "Prompt 存储不可用")
		return
	}
	rec, err := h.svcCtx.Prompts.Get(r.Context(), id)
	if errors.Is(err, intentinfra.ErrPromptNotFound) {
		common.WriteFailure(w, http.StatusNotFound, "Prompt 版本不存在")
		return
	}
	if err != nil {
		common.WriteFailure(w, http.StatusInternalServerError, "读取 Prompt 版本失败")
		return
	}
	var body struct {
		Content json.RawMessage `json:"content"`
		Note    string          `json:"note"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || len(body.Content) == 0 {
		common.WriteFailure(w, http.StatusBadRequest, "content 为必填")
		return
	}
	var content intentdomain.Prompt
	if rec.ScopeType == intentdomain.PromptScopeAccount {
		content, err = intentapp.DecodePromptRequestFor(rec.Kind, rec.Provider, body.Content)
	} else {
		content, err = intentapp.DecodePromptSupplementRequestFor(rec.Kind, rec.Provider, body.Content)
	}
	if err != nil {
		common.WriteFailure(w, http.StatusUnprocessableEntity, "Prompt 内容不合法")
		return
	}
	updated, err := h.svcCtx.Prompts.UpdateDraftFor(r.Context(), id, body.Note, content)
	if errors.Is(err, intentinfra.ErrPromptImmutable) || errors.Is(err, intentapp.ErrPromptImmutable) {
		common.WriteFailure(w, http.StatusConflict, "已发布或已归档版本不可修改")
		return
	}
	if err != nil {
		common.WriteFailure(w, http.StatusUnprocessableEntity, "保存 Prompt 草稿失败")
		return
	}
	common.WriteSuccess(w, toUnifiedPromptVersion(updated))
}

func (h *Handler) DiffUnifiedPrompt(w http.ResponseWriter, r *http.Request) {
	promptID := pathPromptID(r)
	kind, ok := intentdomain.PromptKindForID(promptID)
	if !ok {
		common.WriteFailure(w, http.StatusNotFound, "Prompt 不存在")
		return
	}
	if h.svcCtx.Prompts == nil {
		common.WriteFailure(w, http.StatusServiceUnavailable, "Prompt 存储不可用")
		return
	}
	provider := queryPromptProvider(r, promptID)
	if err := validatePromptProvider(promptID, provider); err != nil {
		common.WriteFailure(w, http.StatusBadRequest, err.Error())
		return
	}
	botID, err := requiredBotID(r)
	if err != nil {
		common.WriteFailure(w, http.StatusBadRequest, err.Error())
		return
	}
	scopeType, err := queryPromptScope(r)
	if err != nil {
		common.WriteFailure(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	fromSource := r.URL.Query().Get("from")
	if fromSource == "" {
		fromSource = "auto"
	}
	if fromSource == "draft" {
		common.WriteFailure(w, http.StatusUnprocessableEntity, "diff 起始版本不支持 draft")
		return
	}
	from, err := resolveDiffPrompt(r.Context(), h.svcCtx.Prompts, promptID, kind, provider, botID, scopeType, fromSource, r.URL.Query().Get("from_version_id"))
	if err != nil {
		common.WriteFailure(w, http.StatusUnprocessableEntity, "diff 起始版本不可用")
		return
	}
	toSource := r.URL.Query().Get("to")
	if toSource == "" {
		toSource = "draft"
	}
	if toSource != "draft" && toSource != "active" && toSource != "version" {
		common.WriteFailure(w, http.StatusUnprocessableEntity, "diff 目标版本不支持该来源")
		return
	}
	to, err := resolveDiffPrompt(r.Context(), h.svcCtx.Prompts, promptID, kind, provider, botID, scopeType, toSource, r.URL.Query().Get("to_version_id"))
	if err != nil {
		common.WriteFailure(w, http.StatusUnprocessableEntity, "diff 目标版本不可用")
		return
	}
	fromJSON, _ := json.MarshalIndent(from.Content, "", "  ")
	toJSON, _ := json.MarshalIndent(to.Content, "", "  ")
	lines := lineDiff(string(fromJSON), string(toJSON))
	diffText := make([]string, 0, len(lines))
	for _, line := range lines {
		diffText = append(diffText, line["Op"]+" "+line["Text"])
	}
	common.WriteSuccess(w, map[string]any{
		"PromptID": promptID, "Provider": provider,
		"From": promptDiffRef(from), "To": promptDiffRef(to),
		"Changed": string(fromJSON) != string(toJSON), "UnifiedDiff": strings.Join(diffText, "\n"), "Lines": lines,
	})
}

func (h *Handler) EffectiveUnifiedPrompt(w http.ResponseWriter, r *http.Request) {
	prompt, conversationID, err := h.resolveUnifiedRuntimePrompt(w, r)
	if err != nil {
		return
	}
	common.WriteSuccess(w, effectivePromptResponse(prompt, conversationID))
}

func (h *Handler) PreviewUnifiedPrompt(w http.ResponseWriter, r *http.Request) {
	if h.svcCtx.Prompts == nil {
		common.WriteFailure(w, http.StatusServiceUnavailable, "Prompt 存储不可用")
		return
	}
	var body struct {
		BotID       int64          `json:"bot_id"`
		Provider    string         `json:"provider"`
		PreviewMode string         `json:"preview_mode"`
		VersionID   int64          `json:"version_id"`
		Sample      map[string]any `json:"sample"`
		Context     map[string]any `json:"context"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		common.WriteFailure(w, http.StatusBadRequest, "请求体不合法")
		return
	}
	promptID := pathPromptID(r)
	kind, ok := intentdomain.PromptKindForID(promptID)
	if !ok {
		common.WriteFailure(w, http.StatusNotFound, "Prompt 不存在")
		return
	}
	if body.BotID <= 0 {
		common.WriteFailure(w, http.StatusBadRequest, "bot_id 必填")
		return
	}
	if body.Provider == "" {
		body.Provider = defaultPromptProvider(promptID)
	}
	if err := validatePromptProvider(promptID, body.Provider); err != nil {
		common.WriteFailure(w, http.StatusBadRequest, err.Error())
		return
	}
	if body.PreviewMode == "" {
		body.PreviewMode = "example"
	}
	if body.PreviewMode != "example" && body.PreviewMode != "real" {
		common.WriteFailure(w, http.StatusBadRequest, "preview_mode 必须为 example 或 real")
		return
	}
	targetType, targetID, targetErr := previewTarget(body.Context)
	if targetErr != nil {
		common.WriteFailure(w, http.StatusBadRequest, targetErr.Error())
		return
	}
	if body.PreviewMode == "real" && (targetType == "" || targetID <= 0) {
		common.WriteFailure(w, http.StatusBadRequest, "真实预览必须提供 context.target_type 与 context.target_id")
		return
	}
	var version *intentdomain.PromptRecord
	if body.VersionID > 0 {
		rec, err := h.svcCtx.Prompts.Get(r.Context(), body.VersionID)
		if err != nil || rec.PromptID != promptID || rec.BotID != body.BotID || rec.Provider != body.Provider {
			common.WriteFailure(w, http.StatusNotFound, "Prompt 版本不存在")
			return
		}
		version = &rec
	}
	if targetType == "" {
		targetType, targetID = "group", 1000
		if version != nil && version.ScopeType == intentdomain.PromptScopePrivate {
			targetType = "private"
		}
	}
	if version != nil && version.ScopeType != intentdomain.PromptScopeAccount && version.ScopeType != targetType {
		common.WriteFailure(w, http.StatusUnprocessableEntity, "指定版本与预览会话类型不匹配")
		return
	}
	variables := map[string]string{"bot_name": settingsBotName(h.svcCtx)}
	if senderID := intFromAny(body.Context["sender_id"]); senderID > 0 {
		variables["sender_id"] = strconv.FormatInt(senderID, 10)
	} else if targetType == "private" {
		variables["sender_id"] = strconv.FormatInt(targetID, 10)
	} else if body.PreviewMode == "example" {
		variables["sender_id"] = "10001"
	}
	var prompt intentdomain.Prompt
	if version == nil {
		prompt = h.svcCtx.Prompts.ResolveFor(r.Context(), promptID, kind, body.Provider, body.BotID, targetType, targetID, variables)
	} else {
		prompt = h.svcCtx.Prompts.PreviewForVersion(r.Context(), *version, targetType, targetID, variables)
		if !slices.ContainsFunc(prompt.Layers, func(layer intentdomain.PromptLayer) bool { return layer.VersionID == version.ID }) {
			common.WriteFailure(w, http.StatusUnprocessableEntity, "指定 Prompt 不能与当前层合并")
			return
		}
	}
	prompt, err := h.composeEffectiveQuestionPrompt(r, prompt)
	if err != nil {
		common.WriteFailure(w, http.StatusInternalServerError, "读取生效参与规则失败")
		return
	}
	prompt = renderPreviewSample(prompt, body.Sample)
	prompt.RenderedHash = intentdomain.PromptHash(prompt)
	common.WriteSuccess(w, previewPromptResponse(prompt, body.PreviewMode, body.VersionID, previewConversationID(body.Context), body.Sample, body.Context))
}

func (h *Handler) SyncUnifiedPrompt(w http.ResponseWriter, r *http.Request) {
	if h.svcCtx.Prompts == nil {
		common.WriteFailure(w, http.StatusServiceUnavailable, "Prompt 存储不可用")
		return
	}
	id := pathPromptID(r)
	kind, ok := intentdomain.PromptKindForID(id)
	if !ok {
		common.WriteFailure(w, http.StatusNotFound, "Prompt 不存在")
		return
	}
	var body struct {
		BotID     int64  `json:"bot_id"`
		ScopeType string `json:"scope_type"`
		Provider  string `json:"provider"`
		Name      string `json:"name"`
		Version   int    `json:"version"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.BotID <= 0 || body.Version <= 0 ||
		(body.ScopeType != "account" && body.ScopeType != "group" && body.ScopeType != "private") {
		common.WriteFailure(w, http.StatusBadRequest, "需要账号、层级和指定的 Langfuse 版本")
		return
	}
	if body.Provider == "" {
		body.Provider = defaultPromptProvider(id)
	}
	if err := validatePromptProvider(id, body.Provider); err != nil {
		common.WriteFailure(w, http.StatusBadRequest, err.Error())
		return
	}
	key := intentdomain.PromptKey{PromptID: id, Kind: kind, Provider: body.Provider, BotID: body.BotID, ScopeType: body.ScopeType}
	version, err := h.svcCtx.Prompts.ImportRemoteVersion(r.Context(), key, body.Name, body.Version)
	if err != nil {
		_, message := intentapp.PromptSyncError(err)
		common.WriteFailure(w, http.StatusUnprocessableEntity, message)
		return
	}
	common.WriteSuccess(w, toUnifiedPromptVersion(version))
}

func (h *Handler) PublishUnifiedPrompt(w http.ResponseWriter, r *http.Request) {
	if h.svcCtx.Prompts == nil {
		common.WriteFailure(w, http.StatusServiceUnavailable, "Prompt 存储不可用")
		return
	}
	id, err := strconv.ParseInt(pathVersionID(r), 10, 64)
	if err != nil || id <= 0 {
		common.WriteFailure(w, http.StatusBadRequest, "Prompt 版本 id 无效")
		return
	}
	version, archivedID, err := h.svcCtx.Prompts.PublishVersion(r.Context(), id)
	if err != nil {
		if errors.Is(err, intentinfra.ErrPromptNotFound) {
			common.WriteFailure(w, http.StatusNotFound, "Prompt 版本不存在")
			return
		}
		if errors.Is(err, intentinfra.ErrPromptImmutable) {
			common.WriteFailure(w, http.StatusConflict, "只有草稿可以发布")
			return
		}
		common.WriteFailure(w, http.StatusInternalServerError, "发布 Prompt 失败")
		return
	}
	common.WriteSuccess(w, map[string]any{"Version": toUnifiedPromptVersion(version), "ArchivedVersionID": archivedID})
}

func (h *Handler) RollbackUnifiedPrompt(w http.ResponseWriter, r *http.Request) {
	if h.svcCtx.Prompts == nil {
		common.WriteFailure(w, http.StatusServiceUnavailable, "Prompt 存储不可用")
		return
	}
	var body struct {
		BotID     int64  `json:"bot_id"`
		ScopeType string `json:"scope_type"`
		ScopeID   int64  `json:"scope_id"`
		Provider  string `json:"provider"`
		VersionID int64  `json:"version_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.BotID <= 0 || body.VersionID <= 0 {
		common.WriteFailure(w, http.StatusBadRequest, "bot_id 与 version_id 必填")
		return
	}
	if body.ScopeID != 0 {
		common.WriteFailure(w, http.StatusUnprocessableEntity, "scope_id 必须为 0")
		return
	}
	if body.ScopeType == "" {
		body.ScopeType = intentdomain.PromptScopeAccount
	}
	if body.ScopeType != intentdomain.PromptScopeAccount && body.ScopeType != intentdomain.PromptScopeGroup && body.ScopeType != intentdomain.PromptScopePrivate {
		common.WriteFailure(w, http.StatusBadRequest, "scope_type 必须为 account|group|private")
		return
	}
	promptID := pathPromptID(r)
	kind, ok := intentdomain.PromptKindForID(promptID)
	if !ok {
		common.WriteFailure(w, http.StatusNotFound, "Prompt 不存在")
		return
	}
	if body.Provider == "" {
		body.Provider = defaultPromptProvider(promptID)
	}
	if err := validatePromptProvider(promptID, body.Provider); err != nil {
		common.WriteFailure(w, http.StatusBadRequest, err.Error())
		return
	}
	version, archivedID, err := h.svcCtx.Prompts.RollbackVersion(r.Context(), intentdomain.PromptKey{PromptID: promptID, Kind: kind, Provider: body.Provider, BotID: body.BotID, ScopeType: body.ScopeType}, body.VersionID)
	if err != nil {
		if errors.Is(err, intentinfra.ErrPromptNotFound) {
			common.WriteFailure(w, http.StatusNotFound, "Prompt 版本不存在")
			return
		}
		if errors.Is(err, intentinfra.ErrPromptNotArchived) {
			common.WriteFailure(w, http.StatusConflict, "只有已归档且曾发布的版本可以回滚")
			return
		}
		common.WriteFailure(w, http.StatusInternalServerError, "回滚 Prompt 失败")
		return
	}
	common.WriteSuccess(w, map[string]any{"Version": toUnifiedPromptVersion(version), "ArchivedVersionID": archivedID})
}

func (h *Handler) RestoreUnifiedBuiltin(w http.ResponseWriter, r *http.Request) {
	if h.svcCtx.Prompts == nil {
		common.WriteFailure(w, http.StatusServiceUnavailable, "Prompt 存储不可用")
		return
	}
	promptID := pathPromptID(r)
	kind, ok := intentdomain.PromptKindForID(promptID)
	if !ok {
		common.WriteFailure(w, http.StatusNotFound, "Prompt 不存在")
		return
	}
	var body struct {
		BotID     int64  `json:"bot_id"`
		ScopeType string `json:"scope_type"`
		ScopeID   int64  `json:"scope_id"`
		Provider  string `json:"provider"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.BotID <= 0 {
		common.WriteFailure(w, http.StatusBadRequest, "bot_id 必填")
		return
	}
	if body.ScopeID != 0 {
		common.WriteFailure(w, http.StatusUnprocessableEntity, "scope_id 必须为 0")
		return
	}
	if body.ScopeType == "" {
		body.ScopeType = intentdomain.PromptScopeAccount
	}
	if body.ScopeType != intentdomain.PromptScopeAccount && body.ScopeType != intentdomain.PromptScopeGroup && body.ScopeType != intentdomain.PromptScopePrivate {
		common.WriteFailure(w, http.StatusBadRequest, "scope_type 必须为 account|group|private")
		return
	}
	if body.Provider == "" {
		body.Provider = defaultPromptProvider(promptID)
	}
	if err := validatePromptProvider(promptID, body.Provider); err != nil {
		common.WriteFailure(w, http.StatusBadRequest, err.Error())
		return
	}
	archivedID, err := h.svcCtx.Prompts.RestoreBuiltin(r.Context(), promptID, kind, body.Provider, body.BotID, body.ScopeType)
	if err != nil {
		common.WriteFailure(w, http.StatusInternalServerError, "恢复内置 Prompt 失败")
		return
	}
	builtin := intentdomain.BuiltinPromptFor(kind, body.Provider)
	common.WriteSuccess(w, map[string]any{"PromptID": promptID, "ScopeType": body.ScopeType, "ScopeID": 0, "Provider": body.Provider, "Active": nil, "Resolved": map[string]any{"Source": intentdomain.PromptSourceBuiltin, "Version": builtin.Version, "Hash": builtin.Hash}, "ArchivedVersionID": archivedID})
}

func (h *Handler) resolveUnifiedRuntimePrompt(w http.ResponseWriter, r *http.Request) (intentdomain.Prompt, string, error) {
	if h.svcCtx.Prompts == nil {
		common.WriteFailure(w, http.StatusServiceUnavailable, "Prompt 存储不可用")
		return intentdomain.Prompt{}, "", errors.New("prompt store unavailable")
	}
	promptID := pathPromptID(r)
	kind, ok := intentdomain.PromptKindForID(promptID)
	if !ok {
		common.WriteFailure(w, http.StatusNotFound, "Prompt 不存在")
		return intentdomain.Prompt{}, "", errors.New("prompt not found")
	}
	botID, err := requiredBotID(r)
	if err != nil {
		common.WriteFailure(w, http.StatusBadRequest, err.Error())
		return intentdomain.Prompt{}, "", err
	}
	targetType := r.URL.Query().Get("target_type")
	targetID, _ := strconv.ParseInt(r.URL.Query().Get("target_id"), 10, 64)
	if targetType == "" || targetID <= 0 || (targetType != "group" && targetType != "private") {
		common.WriteFailure(w, http.StatusBadRequest, "target_type/target_id 必填且必须为 group|private")
		return intentdomain.Prompt{}, "", errors.New("invalid target")
	}
	provider := queryPromptProvider(r, promptID)
	if err := validatePromptProvider(promptID, provider); err != nil {
		common.WriteFailure(w, http.StatusBadRequest, err.Error())
		return intentdomain.Prompt{}, "", err
	}
	vars := map[string]string{"bot_name": settingsBotName(h.svcCtx), "scope_type": targetType}
	if senderID, err := strconv.ParseInt(r.URL.Query().Get("sender_id"), 10, 64); err == nil && senderID > 0 {
		vars["sender_id"] = strconv.FormatInt(senderID, 10)
	} else if targetType == "private" {
		vars["sender_id"] = strconv.FormatInt(targetID, 10)
	}
	prompt := h.svcCtx.Prompts.ResolveFor(r.Context(), promptID, kind, provider, botID, targetType, targetID, vars)
	prompt, err = h.composeEffectiveQuestionPrompt(r, prompt)
	if err != nil {
		common.WriteFailure(w, http.StatusInternalServerError, "读取生效参与规则失败")
		return intentdomain.Prompt{}, "", err
	}
	return prompt, r.URL.Query().Get("conversation_id"), nil
}

func (h *Handler) composeEffectiveQuestionPrompt(r *http.Request, prompt intentdomain.Prompt) (intentdomain.Prompt, error) {
	if h.svcCtx.Rulesets == nil {
		return prompt, nil
	}
	return intentapp.ApplyActiveQuestionRuleset(r.Context(), h.svcCtx.Rulesets, prompt)
}

func effectivePromptResponse(p intentdomain.Prompt, conversationID string) map[string]any {
	if p.Kind == intentdomain.PromptKindChatReply {
		p = intentdomain.ReplySystem(p, "")
	}
	target := map[string]any{"Type": p.TargetType, "ID": p.TargetID}
	if conversationID != "" {
		target["ConversationID"] = conversationID
	}
	finalHash := p.FinalHash
	if finalHash == "" {
		finalHash = p.Hash
	}
	return map[string]any{
		"PromptID": p.ID, "Kind": p.Kind, "Provider": p.Provider, "BotID": p.BotID,
		"Target": target,
		"Layers": promptLayersResponse(p.Layers),
		"Final":  map[string]any{"Content": intentdomain.PromptContentJSON(p), "Variables": promptVariablesResponse(p.Variables)},
		"Hash":   finalHash, "RenderedHash": p.RenderedHash,
	}
}

func previewPromptResponse(p intentdomain.Prompt, mode string, versionID int64, conversationID string, sample, context map[string]any) map[string]any {
	if p.Kind == intentdomain.PromptKindChatReply {
		p = intentdomain.ReplySystem(p, "")
	}
	warnings := []string{}
	if mode == "real" && p.TargetType == "group" && p.Variables["sender_id"] == "" &&
		p.OpenAI != nil && strings.Contains(p.OpenAI.SystemPrompt, "{{sender_id}}") {
		warnings = append(warnings, "填写发言人 QQ 号后可计算这条消息的实际渲染哈希。")
	}
	target := map[string]any{"Type": p.TargetType, "ID": p.TargetID}
	if conversationID != "" {
		target["ConversationID"] = conversationID
	}
	return map[string]any{
		"PromptID": p.ID, "Kind": p.Kind, "Provider": p.Provider, "PreviewMode": mode,
		"VersionID": versionID, "Target": target,
		"Layers":   promptLayersResponse(p.Layers),
		"Final":    map[string]any{"Content": intentdomain.PromptContentJSON(p), "Variables": promptVariablesResponse(p.Variables)},
		"Warnings": warnings, "PreviewHash": previewHash(p, mode, sample, context), "RenderedHash": p.RenderedHash,
	}
}

func previewHash(p intentdomain.Prompt, mode string, sample, context map[string]any) string {
	raw, _ := json.Marshal(map[string]any{
		"RenderedHash": p.RenderedHash, "FinalHash": p.FinalHash,
		"PreviewMode": mode, "Sample": sample, "Context": context,
	})
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

func renderPreviewSample(p intentdomain.Prompt, sample map[string]any) intentdomain.Prompt {
	if sample == nil || p.OpenAI == nil {
		return p
	}
	values := map[string]string{}
	for _, key := range []string{"state", "question", "messages_json"} {
		if value, ok := sample[key]; ok {
			values[key] = previewSampleValue(value)
		}
	}
	currentMessage, hasCurrentMessage := sample["current_message"]
	if _, ok := values["state"]; !ok && (hasCurrentMessage || sample["recent"] != nil) {
		state := map[string]any{"message": currentMessage}
		if recent, ok := sample["recent"]; ok {
			state["recent"] = recent
		} else {
			state["recent"] = []any{}
		}
		raw, _ := json.Marshal(state)
		values["state"] = string(raw)
	}
	if _, ok := values["question"]; !ok && hasCurrentMessage {
		values["question"] = "当前消息是否在向机器人说话？A=是，B=否。"
	}
	if _, ok := values["messages_json"]; !ok && hasCurrentMessage {
		raw, _ := json.Marshal(sample)
		values["messages_json"] = string(raw)
	}
	if len(values) == 0 {
		return p
	}
	openAI := *p.OpenAI
	for key, value := range values {
		openAI.UserTemplate = strings.ReplaceAll(openAI.UserTemplate, "{{"+key+"}}", value)
	}
	p.OpenAI = &openAI
	return p
}

func previewSampleValue(value any) string {
	if text, ok := value.(string); ok {
		return text
	}
	raw, _ := json.Marshal(value)
	return string(raw)
}

func promptVariablesResponse(variables map[string]string) map[string]string {
	if variables == nil {
		return map[string]string{}
	}
	out := make(map[string]string, len(variables))
	for key, value := range variables {
		out[key] = value
	}
	return out
}

func promptLayersResponse(layers []intentdomain.PromptLayer) []map[string]any {
	out := make([]map[string]any, 0, len(layers))
	for _, layer := range layers {
		item := map[string]any{"Layer": layer.Layer, "VersionID": layer.VersionID, "Version": layer.Version, "Source": layer.Source, "Hash": layer.Hash, "Editable": layer.Editable}
		if layer.SourceRef != "" {
			item["SourceRef"] = layer.SourceRef
		}
		if layer.Summary != "" {
			item["Summary"] = layer.Summary
		}
		if len(layer.Content) > 0 {
			item["Content"] = layer.Content
		}
		out = append(out, item)
	}
	return out
}

func toUnifiedPromptVersion(rec intentdomain.PromptRecord) v1.UnifiedPromptVersion {
	return v1.UnifiedPromptVersion{
		ID: rec.ID, PromptID: rec.PromptID, Kind: rec.Kind, Name: rec.Name, Provider: rec.Provider,
		BotID: rec.BotID, ScopeType: rec.ScopeType, ScopeID: rec.ScopeID, Version: rec.Version,
		Status: rec.Status, Source: rec.Source, SourceRef: rec.SourceRef, Hash: rec.Hash,
		Note: rec.Note, Content: rec.Content, CreatedAt: rec.CreatedAt, PublishedAt: rec.PublishedAt, ArchivedAt: rec.ArchivedAt,
	}
}

func resolveDiffPrompt(ctx context.Context, svc *intentapp.PromptService, promptID, kind, provider string, botID int64, scopeType, source, versionIDText string) (intentdomain.PromptRecord, error) {
	if source == "" {
		source = "auto"
	}
	if source == "builtin" {
		if scopeType != intentdomain.PromptScopeAccount {
			return intentdomain.PromptRecord{}, errors.New("builtin diff is only available for account scope")
		}
		return intentdomain.PromptRecord{ID: 0, PromptID: promptID, Kind: kind, Name: domainNameForKind(kind), Provider: provider, Version: 1, Status: "builtin", Source: intentdomain.PromptSourceBuiltin, Hash: intentdomain.PromptHash(intentdomain.BuiltinPromptFor(kind, provider)), Content: intentdomain.PromptContentJSON(intentdomain.BuiltinPromptFor(kind, provider))}, nil
	}
	if source == "empty" {
		if scopeType == intentdomain.PromptScopeAccount {
			return intentdomain.PromptRecord{}, errors.New("empty diff is only available for scope supplements")
		}
		p := intentdomain.EmptyPromptSupplement(kind, provider)
		return intentdomain.PromptRecord{ID: 0, PromptID: promptID, Kind: kind, Name: "空补充", Provider: provider, ScopeType: scopeType, Version: 0, Status: "empty", Source: intentdomain.PromptSourceBuiltin, Hash: intentdomain.PromptHash(p), Content: intentdomain.PromptContentJSON(p)}, nil
	}
	if source == "auto" {
		if rec, found, err := svc.ActiveRecord(ctx, promptID, kind, provider, botID, scopeType); err != nil {
			return intentdomain.PromptRecord{}, err
		} else if found {
			return rec, nil
		}
		if scopeType == intentdomain.PromptScopeAccount {
			return resolveDiffPrompt(ctx, svc, promptID, kind, provider, botID, scopeType, "builtin", "")
		}
		return resolveDiffPrompt(ctx, svc, promptID, kind, provider, botID, scopeType, "empty", "")
	}
	if source == "active" {
		rec, found, err := svc.ActiveRecord(ctx, promptID, kind, provider, botID, scopeType)
		if err != nil {
			return intentdomain.PromptRecord{}, err
		}
		if !found {
			return intentdomain.PromptRecord{}, errors.New("active not found")
		}
		return rec, nil
	}
	if source == "version" {
		id, err := strconv.ParseInt(versionIDText, 10, 64)
		if err != nil || id <= 0 {
			return intentdomain.PromptRecord{}, errors.New("version_id required")
		}
		rec, err := svc.Get(ctx, id)
		if err != nil {
			return intentdomain.PromptRecord{}, err
		}
		if rec.PromptID != promptID || rec.Kind != kind || rec.Provider != provider || rec.BotID != botID || rec.ScopeType != scopeType {
			return intentdomain.PromptRecord{}, errors.New("version belongs to another prompt layer")
		}
		return rec, nil
	}
	if source == "draft" {
		records, err := svc.VersionsFor(ctx, promptID, kind, provider, botID, scopeType)
		if err != nil {
			return intentdomain.PromptRecord{}, err
		}
		for _, rec := range records {
			if rec.Status == intentdomain.PromptStatusDraft {
				return rec, nil
			}
		}
		return intentdomain.PromptRecord{}, errors.New("draft not found")
	}
	return intentdomain.PromptRecord{}, errors.New("invalid diff source")
}

func promptDiffRef(rec intentdomain.PromptRecord) map[string]any {
	ref := map[string]any{"Version": rec.Version, "Status": rec.Status, "Hash": rec.Hash}
	if rec.ID > 0 {
		ref["ID"] = rec.ID
	}
	if rec.Source != "" {
		ref["Source"] = rec.Source
	}
	return ref
}

func lineDiff(from, to string) []map[string]string {
	fromLines := strings.Split(from, "\n")
	toLines := strings.Split(to, "\n")
	out := make([]map[string]string, 0, len(fromLines)+len(toLines))
	max := len(fromLines)
	if len(toLines) > max {
		max = len(toLines)
	}
	for i := 0; i < max; i++ {
		var left, right string
		if i < len(fromLines) {
			left = fromLines[i]
		}
		if i < len(toLines) {
			right = toLines[i]
		}
		if left == right {
			out = append(out, map[string]string{"Op": " ", "Text": left})
			continue
		}
		if left != "" {
			out = append(out, map[string]string{"Op": "-", "Text": left})
		}
		if right != "" {
			out = append(out, map[string]string{"Op": "+", "Text": right})
		}
	}
	return out
}

func validatePromptProvider(promptID, provider string) error {
	switch promptID {
	case intentdomain.PromptIDChatReply, intentdomain.PromptIDMemoryExtraction, intentdomain.PromptIDMemoryRecall:
		if provider != intentdomain.PromptProviderOpenAI {
			return errors.New("该 Prompt 仅支持 openai provider")
		}
	case intentdomain.PromptIDIntentParticipation:
		if provider != intentdomain.PromptProviderJev && provider != intentdomain.PromptProviderOpenAI {
			return errors.New("provider 必须为 jev 或 openai")
		}
	default:
		return errors.New("Prompt 不存在")
	}
	return nil
}

func defaultPromptProvider(promptID string) string {
	if promptID == intentdomain.PromptIDIntentParticipation {
		return intentdomain.PromptProviderJev
	}
	return intentdomain.PromptProviderOpenAI
}

func queryPromptProvider(r *http.Request, promptID string) string {
	if provider := r.URL.Query().Get("provider"); provider != "" {
		return provider
	}
	return defaultPromptProvider(promptID)
}

func queryPromptScope(r *http.Request) (string, error) {
	scopeType := r.URL.Query().Get("scope_type")
	if scopeType == "" {
		scopeType = intentdomain.PromptScopeAccount
	}
	if scopeType != intentdomain.PromptScopeAccount && scopeType != intentdomain.PromptScopeGroup && scopeType != intentdomain.PromptScopePrivate {
		return "", errors.New("scope_type 必须为 account|group|private")
	}
	if raw := r.URL.Query().Get("scope_id"); raw != "" && raw != "0" {
		return "", errors.New("scope_id 必须为 0")
	}
	return scopeType, nil
}

func requiredBotID(r *http.Request) (int64, error) {
	raw := r.URL.Query().Get("bot_id")
	id, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || id <= 0 {
		return 0, errors.New("bot_id 必填")
	}
	return id, nil
}

func previewTarget(context map[string]any) (string, int64, error) {
	if context == nil {
		return "", 0, nil
	}
	targetType, _ := context["target_type"].(string)
	targetID := intFromAny(context["target_id"])
	if targetType == "" && targetID == 0 {
		return "", 0, nil
	}
	if targetType != "group" && targetType != "private" {
		return "", 0, errors.New("context.target_type 必须为 group|private")
	}
	if targetID <= 0 {
		return "", 0, errors.New("context.target_id 必须为正整数")
	}
	return targetType, targetID, nil
}

func previewConversationID(context map[string]any) string {
	if context == nil {
		return ""
	}
	value, _ := context["conversation_id"].(string)
	return value
}

func settingsBotName(svcCtx *svc.ServiceContext) string {
	if svcCtx.SettingsProvider == nil {
		return "Nagi"
	}
	name := svcCtx.SettingsProvider.Settings().Role.BotName
	if strings.TrimSpace(name) == "" {
		return "Nagi"
	}
	return name
}

func intFromAny(value any) int64 {
	switch v := value.(type) {
	case float64:
		return int64(v)
	case int64:
		return v
	case int:
		return int64(v)
	case json.Number:
		n, _ := v.Int64()
		return n
	default:
		return 0
	}
}

func domainNameForKind(kind string) string {
	return "内置 Prompt"
}
