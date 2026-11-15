// Package knowledge serves the aggregate read and preview API for the
// knowledge-and-memory page. It reuses the Prompt and memory services instead
// of storing a second copy of roles or facts.
// Package knowledge 提供「知识与记忆」页面的聚合读取与预览接口；
// 复用 Prompt 与记忆服务，不重复存储角色或事实。
package knowledge

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"sort"
	"strings"

	"github.com/HappyLadySauceM/Nagi/cmd/app/svc"
	"github.com/HappyLadySauceM/Nagi/cmd/app/types/common"
	conversationdomain "github.com/HappyLadySauceM/Nagi/internal/conversation/domain"
	intentdomain "github.com/HappyLadySauceM/Nagi/internal/intent/domain"
	memorydomain "github.com/HappyLadySauceM/Nagi/internal/memory/domain"
	scopedomain "github.com/HappyLadySauceM/Nagi/internal/scope/domain"
)

// Handler serves knowledge aggregates and read-only context previews.
// Handler 提供知识聚合与只读上下文预览。
type Handler struct {
	svcCtx *svc.ServiceContext
}

// NewHandler builds the knowledge handler.
// NewHandler 构建知识处理器。
func NewHandler(svcCtx *svc.ServiceContext) *Handler {
	return &Handler{svcCtx: svcCtx}
}

// Register wires the knowledge routes.
// Register 注册知识路由。
func (h *Handler) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/knowledge/summary", h.Summary)
	mux.HandleFunc("POST /api/v1/knowledge/preview", h.Preview)
}

// Summary returns the aggregate counters used by the three knowledge entries.
// It never returns memory or message bodies.
// Summary 返回知识三个入口使用的聚合计数，不返回记忆或消息正文。
func (h *Handler) Summary(w http.ResponseWriter, r *http.Request) {
	if h.svcCtx.Memory == nil {
		common.WriteFailure(w, http.StatusServiceUnavailable, "database not configured")
		return
	}
	botID, err := resolveBotID(h.svcCtx, r.URL.Query().Get("bot_id"))
	if err != nil {
		writeBotIDError(w, err)
		return
	}
	summary, err := h.svcCtx.Memory.Summary(r.Context(), botID)
	if err != nil {
		common.WriteFailure(w, http.StatusInternalServerError, "read knowledge summary failed")
		return
	}
	views := make(map[string]any, 3)
	for _, item := range []struct {
		PromptID string
		Kind     string
		Provider string
	}{
		{intentdomain.PromptIDChatReply, intentdomain.PromptKindChatReply, intentdomain.PromptProviderOpenAI},
		{intentdomain.PromptIDIntentParticipation, intentdomain.PromptKindIntentParticipation, intentProvider(h.svcCtx)},
		{intentdomain.PromptIDMemoryExtraction, intentdomain.PromptKindMemoryExtraction, intentdomain.PromptProviderOpenAI},
	} {
		prompt := intentdomain.BuiltinPromptFor(item.Kind, item.Provider)
		prompt.BotID = botID
		if h.svcCtx.Prompts != nil {
			prompt = h.svcCtx.Prompts.ResolveFor(r.Context(), item.PromptID, item.Kind, item.Provider, botID, "", 0, nil)
		}
		views[item.PromptID] = map[string]any{"ActiveVersion": prompt.Version, "Source": prompt.Source}
	}
	common.WriteSuccess(w, map[string]any{
		"Memories": map[string]any{
			"Total":        summary.Memories.Total,
			"Active":       summary.Memories.Active,
			"Candidate":    summary.Memories.Candidate,
			"Superseded":   summary.Memories.Superseded,
			"ByVisibility": visibilityCounts(summary.Memories.ByVisibility),
		},
		"Extraction": map[string]any{
			"Enabled":        h.svcCtx.Config != nil && h.svcCtx.Config.Memory.Extraction.Enabled,
			"PendingJobs":    summary.Extraction.PendingJobs,
			"ProcessingJobs": summary.Extraction.ProcessingJobs,
			"FailedJobs":     summary.Extraction.FailedJobs,
			"LastErrorCode":  summary.Extraction.LastErrorCode,
		},
		"Prompts": views,
	})
}

// previewRequest is the read-only context preview input.
// previewRequest 是只读上下文预览请求。
type previewRequest struct {
	BotID          int64  `json:"bot_id"`
	PreviewMode    string `json:"preview_mode"`
	TargetType     string `json:"target_type"`
	TargetID       int64  `json:"target_id"`
	ConversationID string `json:"conversation_id"`
	Query          string `json:"query"`
}

// Preview returns prompt defaults, effective prompt versions, tool constraints,
// memory and history for one target. Example mode never touches scope, memory
// or conversation storage; real mode resolves the live policy before reading.
// Preview 返回目标上下文中的 Prompt 默认值、实际生效版本、工具约束、记忆与历史。
// 示例模式不访问作用域、记忆或会话存储；真实模式先解析实时策略再读取。
func (h *Handler) Preview(w http.ResponseWriter, r *http.Request) {
	var body previewRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		common.WriteFailure(w, http.StatusBadRequest, "invalid request body")
		return
	}
	switch body.PreviewMode {
	case "example", "real":
	default:
		common.WriteFailure(w, http.StatusBadRequest, "preview_mode must be example or real")
		return
	}
	targetType := strings.TrimSpace(body.TargetType)
	targetID := body.TargetID
	botID := body.BotID
	responseConversationID := strings.TrimSpace(body.ConversationID)
	if body.PreviewMode == "real" {
		// A real preview must name its target explicitly. In particular do not
		// default target_type or resolve a single account implicitly: either
		// shortcut can preview a different target than the caller asked for.
		// 真实预览必须显式给出目标；target_type 缺省或自动解析单账号都可能
		// 预览到调用方未指定的目标。
		if targetType == "" {
			common.WriteFailure(w, http.StatusBadRequest, "target_type must be group or private")
			return
		}
		if targetID <= 0 {
			common.WriteFailure(w, http.StatusBadRequest, "target_id must be positive")
			return
		}
		if botID <= 0 {
			common.WriteFailure(w, http.StatusBadRequest, "bot_id is required")
			return
		}
	} else {
		// Example mode is always synthetic: it may use a default fake target
		// but never echo a caller-supplied conversation id or real target.
		// 示例模式始终使用合成数据：可使用默认假目标，但不得回显调用方提供的
		// 真实会话 ID 或真实目标。
		if targetType == "" {
			targetType = "group"
		}
		targetID = 1000
		responseConversationID = ""
		if botID <= 0 && h.svcCtx.Accounts != nil {
			resolved, err := resolveBotID(h.svcCtx, "")
			if err == nil {
				botID = resolved
			}
		}
	}
	if targetType != "group" && targetType != "private" {
		common.WriteFailure(w, http.StatusBadRequest, "target_type must be group or private")
		return
	}
	botName := settingsBotName(h.svcCtx)
	variables := map[string]string{"bot_name": botName, "scope_type": targetType}
	response := map[string]any{
		"PreviewMode": body.PreviewMode,
		"Target":      map[string]any{"Type": targetType, "ID": targetID, "ConversationID": responseConversationID},
		"Defaults": map[string]any{
			"ChatSystem":       defaultSystem(intentdomain.PromptKindChatReply, intentdomain.PromptProviderOpenAI, variables),
			"ExtractionSystem": defaultSystem(intentdomain.PromptKindMemoryExtraction, intentdomain.PromptProviderOpenAI, variables),
		},
		"Prompts": h.previewPrompts(r.Context(), botID, targetType, targetID, variables),
	}
	if body.PreviewMode == "example" {
		response["Context"] = exampleContext()
	} else {
		contextView, resolvedConversationID, status, message := h.realContext(r, body, botID, targetType, targetID)
		if status != 0 {
			common.WriteFailure(w, status, message)
			return
		}
		response["Context"] = contextView
		if target, ok := response["Target"].(map[string]any); ok {
			target["ConversationID"] = resolvedConversationID
		}
	}
	common.WriteSuccess(w, response)
}

func (h *Handler) previewPrompts(ctx context.Context, botID int64, targetType string, targetID int64, variables map[string]string) map[string]any {
	items := []struct {
		PromptID string
		Kind     string
		Provider string
	}{
		{intentdomain.PromptIDChatReply, intentdomain.PromptKindChatReply, intentdomain.PromptProviderOpenAI},
		{intentdomain.PromptIDIntentParticipation, intentdomain.PromptKindIntentParticipation, intentProvider(h.svcCtx)},
		{intentdomain.PromptIDMemoryExtraction, intentdomain.PromptKindMemoryExtraction, intentdomain.PromptProviderOpenAI},
	}
	out := make(map[string]any, len(items))
	for _, item := range items {
		prompt := intentdomain.BuiltinPromptFor(item.Kind, item.Provider)
		prompt.BotID = botID
		prompt.TargetType = targetType
		prompt.TargetID = targetID
		prompt.Variables = copyVariables(variables)
		if h.svcCtx.Prompts != nil {
			prompt = h.svcCtx.Prompts.ResolveFor(ctx, item.PromptID, item.Kind, item.Provider, botID, targetType, targetID, variables)
		} else {
			prompt = intentdomain.RenderPromptVariables(prompt, variables)
			prompt.Layers = []intentdomain.PromptLayer{
				{Layer: intentdomain.PromptLayerAccountDefault, VersionID: 0, Version: 1, Source: intentdomain.PromptSourceBuiltin, Hash: prompt.Hash, Editable: false, Content: intentdomain.PromptContentJSON(prompt)},
				{Layer: intentdomain.PromptLayerServerConstraints, Version: 1, Source: intentdomain.PromptServerConstraintsVersion, Hash: intentdomain.PromptTextHash(intentdomain.PromptServerConstraintsText), Editable: false, Summary: intentdomain.PromptServerConstraintsText},
			}
			prompt.FinalHash = intentdomain.PromptFinalHash(prompt)
			prompt.RenderedHash = intentdomain.PromptHash(prompt)
		}
		out[item.PromptID] = promptView(prompt)
	}
	return out
}

func (h *Handler) realContext(r *http.Request, body previewRequest, botID int64, targetType string, targetID int64) (map[string]any, string, int, string) {
	if h.svcCtx.Scope == nil || h.svcCtx.Memory == nil {
		return nil, "", http.StatusServiceUnavailable, "database not configured"
	}
	policy, err := h.svcCtx.Scope.Resolve(r.Context(), botID, scopedomain.ScopeType(targetType), targetID)
	if err != nil {
		return nil, "", http.StatusInternalServerError, "resolve scope policy failed"
	}
	active := policy.Enabled && policy.StoreMessages && policy.ReplyMode != scopedomain.ModeObserveOnly
	toolsSkipped := !active
	toolsReason := ""
	if toolsSkipped {
		toolsReason = "scope_disabled"
	}
	explicitOverrides := policy.ToolPermissions
	if explicitOverrides == nil {
		explicitOverrides = map[string]bool{}
	}
	effectivePermissions := policy.EffectiveToolPermissions
	if effectivePermissions == nil {
		effectivePermissions = explicitOverrides
	}
	view := map[string]any{
		"Tools": map[string]any{
			"Skipped":              toolsSkipped,
			"Reason":               toolsReason,
			"Policy":               policyView(policy),
			"EffectivePermissions": permissionList(effectivePermissions),
			"ExplicitOverrides":    explicitOverrides,
		},
		"Memory":  map[string]any{"Skipped": false, "Reason": "", "Items": []memorydomain.Memory{}},
		"History": map[string]any{"Skipped": false, "Reason": "", "Messages": []map[string]any{}},
	}
	conversationID := strings.TrimSpace(body.ConversationID)
	conversationSkipReason := ""
	var conversation conversationdomain.Conversation
	conversationFound := false
	// A caller-supplied conversation_id must be checked against the requested
	// bot and target independently of history authorization. The same id is
	// used for conversation-scoped memory reads, so accepting it unchecked
	// would allow a cross-conversation read. On mismatch or when it cannot be
	// checked, drop the id and skip both context layers.
	// 调用方给出的 conversation_id 必须独立于历史读取授权，校验 bot 与目标
	// 归属；该 ID 也用于 conversation 可见性记忆读取，未校验会跨会话读取。
	// 不匹配或无法校验时清空 ID，并跳过 Memory/History 两层。
	if conversationID != "" || (active && (policy.AllowHistoryRead || policy.AllowMemoryRead)) {
		if h.svcCtx.Conversation == nil {
			if conversationID != "" {
				conversationSkipReason = "conversation_unavailable"
				conversationID = ""
			}
		} else {
			var err error
			conversation, conversationFound, err = h.svcCtx.Conversation.GetConversation(r.Context(), botID, conversationdomain.Kind(targetType), targetID)
			if err != nil {
				return nil, "", http.StatusInternalServerError, "read conversation failed"
			}
			if conversationID != "" {
				switch {
				case !conversationFound:
					conversationSkipReason = "conversation_not_found"
					conversationID = ""
				case conversation.ID != conversationID:
					conversationSkipReason = "conversation_target_mismatch"
					conversationID = ""
				}
			}
		}
	}
	if conversationSkipReason == "" && conversationFound {
		conversationID = conversation.ID
	}
	if conversationSkipReason != "" {
		view["Memory"] = map[string]any{"Skipped": true, "Reason": conversationSkipReason, "Items": []memorydomain.Memory{}}
		view["History"] = map[string]any{"Skipped": true, "Reason": conversationSkipReason, "Messages": []map[string]any{}}
	} else {
		if policy.AllowMemoryRead && active {
			items, err := h.svcCtx.Memory.SearchVisible(r.Context(), memorydomain.SearchRequest{
				BotID: botID, Kind: targetType, TargetID: targetID, ConversationID: conversationID,
				Query: body.Query, Limit: 3,
			})
			if err != nil {
				return nil, "", http.StatusInternalServerError, "read memory context failed"
			}
			view["Memory"] = map[string]any{"Skipped": false, "Reason": "", "Items": items}
		} else {
			reason := "memory_not_authorized"
			if !active {
				reason = "scope_disabled"
			}
			view["Memory"] = map[string]any{"Skipped": true, "Reason": reason, "Items": []memorydomain.Memory{}}
		}
		if !active || !policy.AllowHistoryRead || h.svcCtx.Conversation == nil || conversationID == "" {
			reason := "history_not_authorized"
			switch {
			case !active:
				reason = "scope_disabled"
			case !policy.AllowHistoryRead:
				reason = "history_not_authorized"
			case h.svcCtx.Conversation == nil:
				reason = "conversation_unavailable"
			case conversationID == "":
				reason = "conversation_not_found"
			}
			view["History"] = map[string]any{"Skipped": true, "Reason": reason, "Messages": []map[string]any{}}
		} else {
			messages, err := h.svcCtx.Conversation.RecentMessages(r.Context(), conversationID, 30)
			if err != nil {
				return nil, "", http.StatusInternalServerError, "read history context failed"
			}
			view["History"] = map[string]any{"Skipped": false, "Reason": "", "Messages": historyView(messages)}
		}
	}
	return view, conversationID, 0, ""
}

func exampleContext() map[string]any {
	return map[string]any{
		"Tools": map[string]any{
			"Skipped":              false,
			"Reason":               "",
			"Policy":               map[string]any{"Enabled": true, "ReplyMode": "intent_based", "StoreMessages": true, "AllowMemoryRead": true, "AllowHistoryRead": true},
			"EffectivePermissions": []string{"qq.reply.current", "memory.detail", "memory.source"},
			"ExplicitOverrides":    map[string]bool{},
		},
		"Memory": map[string]any{
			"Skipped": false, "Reason": "",
			"Items": []memorydomain.Memory{{
				ID: "example-memory-1", BotID: 1, Visibility: memorydomain.VisibilityPrivateUser,
				OwnerKey: "10001", Content: "示例记忆：这位用户喜欢喝美式咖啡。",
				Source: "example", Status: memorydomain.StatusActive, Confidence: 1,
				SubjectType: memorydomain.SubjectUser, SubjectID: "10001",
			}},
		},
		"History": map[string]any{
			"Skipped": false, "Reason": "",
			"Messages": []map[string]any{
				{"ID": 0, "MessageID": 0, "SenderID": 10001, "IsBot": false, "Text": "示例消息：周末通常做什么？", "CreatedAt": "2026-01-01T00:00:00Z"},
				{"ID": 0, "MessageID": 0, "SenderID": 0, "IsBot": true, "Text": "示例回复：可以一起喝杯咖啡。", "CreatedAt": "2026-01-01T00:00:01Z"},
			},
		},
	}
}

func promptView(prompt intentdomain.Prompt) map[string]any {
	return map[string]any{
		"PromptID": prompt.ID, "Name": prompt.Name, "Kind": prompt.Kind, "Provider": prompt.Provider,
		"Version": prompt.Version, "Source": prompt.Source,
		"Hash": prompt.Hash, "FinalHash": prompt.FinalHash,
		"Layers":    promptLayersView(prompt.Layers),
		"Content":   json.RawMessage(intentdomain.PromptContentJSON(prompt)),
		"Variables": copyVariables(prompt.Variables),
	}
}

func promptLayersView(layers []intentdomain.PromptLayer) []map[string]any {
	out := make([]map[string]any, 0, len(layers))
	for _, layer := range layers {
		item := map[string]any{
			"Layer": layer.Layer, "VersionID": layer.VersionID, "Version": layer.Version,
			"Source": layer.Source, "Hash": layer.Hash, "Editable": layer.Editable,
		}
		if layer.Summary != "" {
			item["Summary"] = layer.Summary
		}
		if len(layer.Content) > 0 {
			item["Content"] = json.RawMessage(layer.Content)
		}
		out = append(out, item)
	}
	return out
}

func defaultSystem(kind, provider string, variables map[string]string) string {
	prompt := intentdomain.BuiltinPromptFor(kind, provider)
	prompt.Variables = copyVariables(variables)
	prompt = intentdomain.RenderPromptVariables(prompt, variables)
	if prompt.OpenAI == nil {
		return ""
	}
	return prompt.OpenAI.SystemPrompt
}

func visibilityCounts(counts map[memorydomain.Visibility]int64) map[string]int64 {
	out := map[string]int64{
		string(memorydomain.VisibilityBotShared):    0,
		string(memorydomain.VisibilityGroup):        0,
		string(memorydomain.VisibilityPrivateUser):  0,
		string(memorydomain.VisibilityConversation): 0,
	}
	for visibility, count := range counts {
		out[string(visibility)] = count
	}
	return out
}

func policyView(policy scopedomain.Policy) map[string]any {
	return map[string]any{
		"Enabled":          policy.Enabled,
		"ReplyMode":        string(policy.ReplyMode),
		"StoreMessages":    policy.StoreMessages,
		"AllowMemoryRead":  policy.AllowMemoryRead,
		"AllowHistoryRead": policy.AllowHistoryRead,
	}
}

func permissionList(permissions map[string]bool) []string {
	out := make([]string, 0, len(permissions))
	for permission, granted := range permissions {
		if granted {
			out = append(out, permission)
		}
	}
	sort.Strings(out)
	return out
}

func historyView(messages []conversationdomain.Message) []map[string]any {
	out := make([]map[string]any, 0, len(messages))
	for _, message := range messages {
		out = append(out, map[string]any{
			"ID": message.ID, "MessageID": message.ID, "SenderID": message.SenderID,
			"IsBot": message.Direction == conversationdomain.Outgoing,
			"Text":  message.Text, "CreatedAt": message.CreatedAt,
		})
	}
	return out
}

func copyVariables(variables map[string]string) map[string]string {
	if len(variables) == 0 {
		return map[string]string{}
	}
	out := make(map[string]string, len(variables))
	for key, value := range variables {
		out[key] = value
	}
	return out
}

func intentProvider(svcCtx *svc.ServiceContext) string {
	if svcCtx != nil && svcCtx.SettingsProvider != nil {
		provider := strings.TrimSpace(svcCtx.SettingsProvider.Settings().Intent.Provider)
		if provider == intentdomain.PromptProviderJev || provider == intentdomain.PromptProviderOpenAI {
			return provider
		}
	}
	return intentdomain.PromptProviderJev
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

var errBotIDRequired = errors.New("bot_id is required")

func resolveBotID(svcCtx *svc.ServiceContext, raw string) (int64, error) {
	if strings.TrimSpace(raw) == "" && svcCtx.Accounts == nil {
		return 0, svc.ErrAccountUnknown
	}
	botID, err := svcCtx.ResolveBotID(raw)
	if err != nil {
		if errors.Is(err, svc.ErrInvalidBotID) {
			return 0, svc.ErrInvalidBotID
		}
		if errors.Is(err, svc.ErrBotIDRequired) {
			return 0, errBotIDRequired
		}
		return 0, err
	}
	return botID, nil
}

func writeBotIDError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, svc.ErrInvalidBotID):
		common.WriteFailure(w, http.StatusBadRequest, "invalid bot_id")
	case errors.Is(err, errBotIDRequired):
		common.WriteFailure(w, http.StatusBadRequest, "bot_id is required")
	default:
		common.WriteFailure(w, http.StatusServiceUnavailable, "bot account identity is not available")
	}
}
