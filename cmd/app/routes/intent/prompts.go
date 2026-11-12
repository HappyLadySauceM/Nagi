// Admin API for local intent prompt versions.
// 本地意图 Prompt 版本的管理 API。
package intent

import (
	"encoding/json"
	"errors"
	"net/http"
	"sort"
	"strconv"

	"github.com/HappyLadySauceM/Nagi/cmd/app/types/common"
	intentapp "github.com/HappyLadySauceM/Nagi/internal/intent/application"
	intentdomain "github.com/HappyLadySauceM/Nagi/internal/intent/domain"
	intentinfra "github.com/HappyLadySauceM/Nagi/internal/intent/infrastructure"
)

type promptSummary struct {
	PromptID      string
	Name          string
	ActiveVersion int
	Status        string
	Source        string
	UpdatedAt     string
}

type promptVersionInfo struct {
	ID          int64
	PromptID    string
	Name        string
	Provider    string
	Version     int
	Status      string
	Source      string
	SourceRef   string
	Hash        string
	Note        string
	Content     json.RawMessage
	CreatedAt   string
	PublishedAt string
}

func (h *Handler) RegisterPrompts(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/intent/prompts", h.ListPrompts)
	mux.HandleFunc("GET /api/v1/intent/prompts/versions/{id}", h.GetPromptVersion)
	mux.HandleFunc("GET /api/v1/intent/prompts/{prompt_id}", h.GetPrompt)
	mux.HandleFunc("POST /api/v1/intent/prompts/{prompt_id}/versions", h.CreatePromptDraft)
	mux.HandleFunc("PATCH /api/v1/intent/prompts/versions/{id}", h.UpdatePromptDraft)
	mux.HandleFunc("POST /api/v1/intent/prompts/versions/{id}/publish", h.PublishPrompt)
	mux.HandleFunc("POST /api/v1/intent/prompts/{prompt_id}/rollback", h.RollbackPrompt)
}

func (h *Handler) ListPrompts(w http.ResponseWriter, r *http.Request) {
	if h.svcCtx.Prompts == nil {
		common.WriteFailure(w, http.StatusServiceUnavailable, "Prompt 存储不可用")
		return
	}
	records, err := h.svcCtx.Prompts.List(r.Context())
	if err != nil {
		common.WriteFailure(w, http.StatusInternalServerError, "读取 Prompt 列表失败")
		return
	}
	if len(records) == 0 {
		builtin := intentdomain.BuiltinPrompt(intentdomain.PromptProviderJev)
		common.WriteSuccess(w, map[string]any{"Items": []promptSummary{{
			PromptID: builtin.ID, Name: builtin.Name, ActiveVersion: builtin.Version,
			Status: intentdomain.PromptStatusPublished, Source: intentdomain.PromptSourceBuiltin,
		}}})
		return
	}
	byID := make(map[string][]intentdomain.PromptRecord)
	order := make([]string, 0)
	for _, rec := range records {
		if _, ok := byID[rec.PromptID]; !ok {
			order = append(order, rec.PromptID)
		}
		byID[rec.PromptID] = append(byID[rec.PromptID], rec)
	}
	items := make([]promptSummary, 0, len(order))
	for _, id := range order {
		versions := byID[id]
		sort.Slice(versions, func(i, j int) bool { return versions[i].Version > versions[j].Version })
		active := versions[0]
		for _, version := range versions {
			if version.Status == intentdomain.PromptStatusPublished {
				active = version
				break
			}
		}
		items = append(items, promptSummary{
			PromptID: active.PromptID, Name: active.Name, ActiveVersion: active.Version,
			Status: active.Status, Source: active.Source, UpdatedAt: active.CreatedAt,
		})
	}
	common.WriteSuccess(w, map[string]any{"Items": items})
}

func (h *Handler) GetPrompt(w http.ResponseWriter, r *http.Request) {
	if h.svcCtx.Prompts == nil {
		common.WriteFailure(w, http.StatusServiceUnavailable, "Prompt 存储不可用")
		return
	}
	promptID := r.PathValue("prompt_id")
	provider := r.URL.Query().Get("provider")
	if provider == "" {
		provider = intentdomain.PromptProviderJev
	}
	records, err := h.svcCtx.Prompts.Versions(r.Context(), promptID)
	if err != nil {
		common.WriteFailure(w, http.StatusInternalServerError, "读取 Prompt 版本失败")
		return
	}
	items := make([]promptVersionInfo, 0, len(records))
	for _, rec := range records {
		items = append(items, toPromptVersion(rec))
	}
	if len(items) == 0 {
		builtin := intentdomain.BuiltinPrompt(provider)
		items = append(items, promptVersionInfo{
			PromptID: builtin.ID, Name: builtin.Name, Provider: builtin.Provider, Version: builtin.Version,
			Status: intentdomain.PromptStatusPublished, Source: intentdomain.PromptSourceBuiltin,
			Hash: builtin.Hash, Content: intentdomain.PromptContentJSON(builtin),
		})
	}
	common.WriteSuccess(w, map[string]any{"PromptID": promptID, "Name": items[0].Name, "Versions": items})
}

func (h *Handler) GetPromptVersion(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
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
	common.WriteSuccess(w, toPromptVersion(rec))
}

func (h *Handler) CreatePromptDraft(w http.ResponseWriter, r *http.Request) {
	if h.svcCtx.Prompts == nil {
		common.WriteFailure(w, http.StatusServiceUnavailable, "Prompt 存储不可用")
		return
	}
	var body struct {
		Name      string          `json:"name"`
		Provider  string          `json:"provider"`
		Content   json.RawMessage `json:"content"`
		Note      string          `json:"note"`
		Source    string          `json:"source"`
		SourceRef string          `json:"source_ref"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Name == "" || body.Provider == "" || len(body.Content) == 0 {
		common.WriteFailure(w, http.StatusBadRequest, "name、provider 与 content 均为必填")
		return
	}
	content, err := intentapp.DecodePromptRequest(body.Provider, body.Content)
	if err != nil {
		common.WriteFailure(w, http.StatusBadRequest, "Prompt 内容不合法")
		return
	}
	rec, err := h.svcCtx.Prompts.CreateDraft(r.Context(), r.PathValue("prompt_id"), body.Name, body.Provider, content, body.Note, body.Source, body.SourceRef)
	if err != nil {
		common.WriteFailure(w, http.StatusBadRequest, "创建 Prompt 草稿失败")
		return
	}
	common.WriteSuccess(w, toPromptVersion(rec))
}

func (h *Handler) UpdatePromptDraft(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
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
	content, err := intentapp.DecodePromptRequest(rec.Provider, body.Content)
	if err != nil {
		common.WriteFailure(w, http.StatusBadRequest, "Prompt 内容不合法")
		return
	}
	updated, err := h.svcCtx.Prompts.UpdateDraft(r.Context(), id, body.Note, content)
	if errors.Is(err, intentinfra.ErrPromptImmutable) {
		common.WriteFailure(w, http.StatusConflict, "已发布或已归档版本不可修改")
		return
	}
	if err != nil {
		common.WriteFailure(w, http.StatusBadRequest, "保存 Prompt 草稿失败")
		return
	}
	common.WriteSuccess(w, toPromptVersion(updated))
}

func (h *Handler) PublishPrompt(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		common.WriteFailure(w, http.StatusBadRequest, "Prompt 版本 id 无效")
		return
	}
	if h.svcCtx.Prompts == nil {
		common.WriteFailure(w, http.StatusServiceUnavailable, "Prompt 存储不可用")
		return
	}
	if err := h.svcCtx.Prompts.Publish(r.Context(), id); err != nil {
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
	common.WriteSuccess(w, map[string]any{"ID": id})
}

func (h *Handler) RollbackPrompt(w http.ResponseWriter, r *http.Request) {
	if h.svcCtx.Prompts == nil {
		common.WriteFailure(w, http.StatusServiceUnavailable, "Prompt 存储不可用")
		return
	}
	var body struct {
		Version int `json:"version"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Version <= 0 {
		common.WriteFailure(w, http.StatusBadRequest, "version 必须为正整数")
		return
	}
	if err := h.svcCtx.Prompts.Rollback(r.Context(), r.PathValue("prompt_id"), body.Version); err != nil {
		if errors.Is(err, intentinfra.ErrPromptNotFound) {
			common.WriteFailure(w, http.StatusNotFound, "Prompt 版本不存在")
			return
		}
		common.WriteFailure(w, http.StatusInternalServerError, "回滚 Prompt 失败")
		return
	}
	common.WriteSuccess(w, map[string]any{"Version": body.Version})
}

func toPromptVersion(rec intentdomain.PromptRecord) promptVersionInfo {
	return promptVersionInfo{
		ID: rec.ID, PromptID: rec.PromptID, Name: rec.Name, Provider: rec.Provider,
		Version: rec.Version, Status: rec.Status, Source: rec.Source, SourceRef: rec.SourceRef,
		Hash: rec.Hash, Note: rec.Note, Content: rec.Content,
		CreatedAt: rec.CreatedAt, PublishedAt: rec.PublishedAt,
	}
}
