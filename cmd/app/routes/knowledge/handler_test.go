package knowledge

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/HappyLadySauceM/Nagi/cmd/app/svc"
	conversationapp "github.com/HappyLadySauceM/Nagi/internal/conversation/application"
	conversationdomain "github.com/HappyLadySauceM/Nagi/internal/conversation/domain"
	memoryapp "github.com/HappyLadySauceM/Nagi/internal/memory/application"
	memorydomain "github.com/HappyLadySauceM/Nagi/internal/memory/domain"
	scopeapp "github.com/HappyLadySauceM/Nagi/internal/scope/application"
	scopedomain "github.com/HappyLadySauceM/Nagi/internal/scope/domain"
	"github.com/HappyLadySauceM/Nagi/pkg/config"
)

type fakeMemoryRepo struct {
	summary              memorydomain.KnowledgeSummary
	searchCalled         bool
	searchConversationID string
	memories             []memorydomain.Memory
}

func (f *fakeMemoryRepo) Insert(context.Context, memorydomain.Memory) (string, error) {
	return "", nil
}
func (f *fakeMemoryRepo) Get(context.Context, string) (memorydomain.Memory, bool, error) {
	return memorydomain.Memory{}, false, nil
}
func (f *fakeMemoryRepo) Update(context.Context, memorydomain.Memory) error { return nil }
func (f *fakeMemoryRepo) Delete(context.Context, string) (bool, error)      { return false, nil }
func (f *fakeMemoryRepo) List(context.Context, memorydomain.ListFilter) ([]memorydomain.Memory, int64, error) {
	return nil, 0, nil
}
func (f *fakeMemoryRepo) FindSame(context.Context, int64, memorydomain.Visibility, string, string) (memorydomain.Memory, bool, error) {
	return memorydomain.Memory{}, false, nil
}
func (f *fakeMemoryRepo) SearchVisible(_ context.Context, request memorydomain.SearchRequest) ([]memorydomain.Memory, error) {
	f.searchCalled = true
	f.searchConversationID = request.ConversationID
	return f.memories, nil
}

type fakeConversationRepo struct {
	conversationapp.Repository
	conversation conversationdomain.Conversation
	found        bool
	recentCalled bool
	kind         conversationdomain.Kind
	targetID     int64
}

func (f *fakeConversationRepo) GetConversation(_ context.Context, _ int64, kind conversationdomain.Kind, targetID int64) (conversationdomain.Conversation, bool, error) {
	f.kind = kind
	f.targetID = targetID
	if !f.found {
		return conversationdomain.Conversation{}, false, nil
	}
	if f.conversation.Kind != kind || f.conversation.TargetID != targetID {
		return conversationdomain.Conversation{}, false, nil
	}
	return f.conversation, true, nil
}

func (f *fakeConversationRepo) RecentMessages(context.Context, string, int) ([]conversationdomain.Message, error) {
	f.recentCalled = true
	return nil, nil
}

func testConversationService(t *testing.T, repo *fakeConversationRepo) *conversationapp.Service {
	t.Helper()
	service, err := conversationapp.NewService(repo, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("new conversation service: %v", err)
	}
	return service
}
func (f *fakeMemoryRepo) MemorySummary(context.Context, int64) (memorydomain.MemorySummary, error) {
	return f.summary.Memories, nil
}
func (f *fakeMemoryRepo) ExtractionSummary(context.Context, int64) (memorydomain.ExtractionSummary, error) {
	return f.summary.Extraction, nil
}

type fakeScopeRepo struct {
	policy scopedomain.Policy
}

func (f *fakeScopeRepo) Get(context.Context, int64, scopedomain.ScopeType, int64) (scopedomain.Policy, bool, error) {
	return f.policy, true, nil
}
func (f *fakeScopeRepo) List(context.Context, *int64) ([]scopedomain.Policy, error) {
	return nil, nil
}
func (f *fakeScopeRepo) Upsert(context.Context, int64, scopedomain.Policy) error { return nil }
func (f *fakeScopeRepo) Delete(context.Context, int64, scopedomain.ScopeType, int64) (bool, error) {
	return true, nil
}

func decodeData(t *testing.T, body []byte) map[string]any {
	t.Helper()
	var envelope struct {
		Data map[string]any `json:"data"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		t.Fatalf("decode response: %v body=%s", err, body)
	}
	return envelope.Data
}

func TestSummaryReturnsMemoryExtractionAndPromptCounters(t *testing.T) {
	repo := &fakeMemoryRepo{summary: memorydomain.KnowledgeSummary{
		Memories: memorydomain.MemorySummary{
			Total: 4, Active: 2, Candidate: 1, Superseded: 1,
			ByVisibility: map[memorydomain.Visibility]int64{memorydomain.VisibilityGroup: 3},
		},
		Extraction: memorydomain.ExtractionSummary{PendingJobs: 2, ProcessingJobs: 1, FailedJobs: 1, LastErrorCode: "model_timeout"},
	}}
	memoryService, err := memoryapp.NewService(repo)
	if err != nil {
		t.Fatalf("new memory service: %v", err)
	}
	handler := NewHandler(&svc.ServiceContext{
		Memory: memoryService,
		Config: &config.Config{Memory: config.Memory{Extraction: config.Extraction{Enabled: true}}},
	})
	rec := httptest.NewRecorder()
	handler.Summary(rec, httptest.NewRequest(http.MethodGet, "/api/v1/knowledge/summary?bot_id=7", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("summary status = %d body=%s", rec.Code, rec.Body.String())
	}
	data := decodeData(t, rec.Body.Bytes())
	memories, _ := data["Memories"].(map[string]any)
	if memories["Total"] != float64(4) || memories["Active"] != float64(2) || memories["Candidate"] != float64(1) || memories["Superseded"] != float64(1) {
		t.Fatalf("memory counters = %#v", memories)
	}
	byVisibility, _ := memories["ByVisibility"].(map[string]any)
	if byVisibility["group"] != float64(3) || byVisibility["bot_shared"] != float64(0) {
		t.Fatalf("visibility counters = %#v", byVisibility)
	}
	extraction, _ := data["Extraction"].(map[string]any)
	if extraction["Enabled"] != true || extraction["PendingJobs"] != float64(2) || extraction["LastErrorCode"] != "model_timeout" {
		t.Fatalf("extraction counters = %#v", extraction)
	}
	prompts, _ := data["Prompts"].(map[string]any)
	chat, _ := prompts["chat.reply"].(map[string]any)
	if chat["ActiveVersion"] != float64(1) || chat["Source"] != "builtin" {
		t.Fatalf("prompt summary = %#v", chat)
	}
}

func TestPreviewExampleNeverReadsRealMemory(t *testing.T) {
	repo := &fakeMemoryRepo{}
	memoryService, err := memoryapp.NewService(repo)
	if err != nil {
		t.Fatalf("new memory service: %v", err)
	}
	handler := NewHandler(&svc.ServiceContext{Memory: memoryService})
	rec := httptest.NewRecorder()
	handler.Preview(rec, httptest.NewRequest(http.MethodPost, "/api/v1/knowledge/preview", strings.NewReader(`{"preview_mode":"example"}`)))
	if rec.Code != http.StatusOK {
		t.Fatalf("example preview status = %d body=%s", rec.Code, rec.Body.String())
	}
	if repo.searchCalled {
		t.Fatal("example preview called real memory search")
	}
	data := decodeData(t, rec.Body.Bytes())
	if data["PreviewMode"] != "example" {
		t.Fatalf("preview mode = %#v", data["PreviewMode"])
	}
	defaults, _ := data["Defaults"].(map[string]any)
	if defaults["ChatSystem"] == "" || defaults["ExtractionSystem"] == "" {
		t.Fatalf("defaults = %#v", defaults)
	}
}

func TestPreviewRealSkipsUnauthorizedMemoryAndHistory(t *testing.T) {
	repo := &fakeMemoryRepo{}
	memoryService, err := memoryapp.NewService(repo)
	if err != nil {
		t.Fatalf("new memory service: %v", err)
	}
	policy := scopedomain.Default(scopedomain.ScopeGroup, 700)
	policy.Enabled = true
	policy.StoreMessages = true
	policy.AllowMemoryRead = false
	policy.AllowHistoryRead = false
	scopeService, err := scopeapp.NewService(&fakeScopeRepo{policy: policy})
	if err != nil {
		t.Fatalf("new scope service: %v", err)
	}
	handler := NewHandler(&svc.ServiceContext{Memory: memoryService, Scope: scopeService})
	rec := httptest.NewRecorder()
	handler.Preview(rec, httptest.NewRequest(http.MethodPost, "/api/v1/knowledge/preview", strings.NewReader(`{"bot_id":7,"preview_mode":"real","target_type":"group","target_id":700}`)))
	if rec.Code != http.StatusOK {
		t.Fatalf("real preview status = %d body=%s", rec.Code, rec.Body.String())
	}
	if repo.searchCalled {
		t.Fatal("unauthorized real preview called memory search")
	}
	data := decodeData(t, rec.Body.Bytes())
	contextData, _ := data["Context"].(map[string]any)
	memory, _ := contextData["Memory"].(map[string]any)
	if memory["Skipped"] != true || memory["Reason"] != "memory_not_authorized" {
		t.Fatalf("memory skip = %#v", memory)
	}
	history, _ := contextData["History"].(map[string]any)
	if history["Skipped"] != true || history["Reason"] != "history_not_authorized" {
		t.Fatalf("history skip = %#v", history)
	}
}

func TestPreviewRealReadsMemoryWhenAuthorized(t *testing.T) {
	repo := &fakeMemoryRepo{memories: []memorydomain.Memory{{
		ID: "11111111-1111-4111-8111-111111111111", Content: "喜欢美式", Visibility: memorydomain.VisibilityGroup, OwnerKey: "700",
		SubjectType: memorydomain.SubjectUser, SubjectID: "42", Status: memorydomain.StatusActive,
	}}}
	memoryService, err := memoryapp.NewService(repo)
	if err != nil {
		t.Fatalf("new memory service: %v", err)
	}
	policy := scopedomain.Default(scopedomain.ScopeGroup, 700)
	policy.Enabled = true
	policy.StoreMessages = true
	policy.AllowMemoryRead = true
	scopeService, err := scopeapp.NewService(&fakeScopeRepo{policy: policy})
	if err != nil {
		t.Fatalf("new scope service: %v", err)
	}
	handler := NewHandler(&svc.ServiceContext{Memory: memoryService, Scope: scopeService})
	rec := httptest.NewRecorder()
	handler.Preview(rec, httptest.NewRequest(http.MethodPost, "/api/v1/knowledge/preview", strings.NewReader(`{"bot_id":7,"preview_mode":"real","target_type":"group","target_id":700,"query":"美式"}`)))
	if rec.Code != http.StatusOK {
		t.Fatalf("authorized real preview status = %d body=%s", rec.Code, rec.Body.String())
	}
	if !repo.searchCalled {
		t.Fatal("authorized real preview did not read memory")
	}
	data := decodeData(t, rec.Body.Bytes())
	contextData, _ := data["Context"].(map[string]any)
	memory, _ := contextData["Memory"].(map[string]any)
	items, _ := memory["Items"].([]any)
	if len(items) != 1 || memory["Skipped"] != false {
		t.Fatalf("memory context = %#v", memory)
	}
	item, _ := items[0].(map[string]any)
	for _, key := range []string{"ID", "Status", "OwnerKey", "UpdatedAt"} {
		if _, ok := item[key]; !ok {
			t.Fatalf("preview memory lost %s: %#v", key, item)
		}
	}
	if item["ID"] != "11111111-1111-4111-8111-111111111111" || item["Status"] != "active" || item["OwnerKey"] != "700" {
		t.Fatalf("preview memory fields = %#v", item)
	}
}

func TestPreviewRealRequiresExplicitTargetAndBot(t *testing.T) {
	handler := NewHandler(&svc.ServiceContext{})
	cases := []string{
		`{"preview_mode":"real","target_id":700}`,
		`{"preview_mode":"real","bot_id":7,"target_id":700}`,
		`{"preview_mode":"real","bot_id":7,"target_type":"group"}`,
		`{"preview_mode":"real","target_type":"group","target_id":700}`,
	}
	for _, body := range cases {
		rec := httptest.NewRecorder()
		handler.Preview(rec, httptest.NewRequest(http.MethodPost, "/api/v1/knowledge/preview", strings.NewReader(body)))
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("body=%s status=%d body=%s, want 400", body, rec.Code, rec.Body.String())
		}
	}
}

func TestPreviewExampleUsesSyntheticTarget(t *testing.T) {
	handler := NewHandler(&svc.ServiceContext{})
	rec := httptest.NewRecorder()
	handler.Preview(rec, httptest.NewRequest(http.MethodPost, "/api/v1/knowledge/preview", strings.NewReader(
		`{"preview_mode":"example","target_type":"private","target_id":700,"conversation_id":"real-conversation"}`,
	)))
	if rec.Code != http.StatusOK {
		t.Fatalf("example preview status = %d body=%s", rec.Code, rec.Body.String())
	}
	data := decodeData(t, rec.Body.Bytes())
	target, _ := data["Target"].(map[string]any)
	if target["ID"] != float64(1000) || target["ConversationID"] != "" {
		t.Fatalf("example target = %#v, want synthetic id and empty conversation", target)
	}
}

func TestPreviewRealRejectsConversationIDMismatchBeforeMemoryRead(t *testing.T) {
	repo := &fakeMemoryRepo{}
	memoryService, err := memoryapp.NewService(repo)
	if err != nil {
		t.Fatalf("new memory service: %v", err)
	}
	policy := scopedomain.Default(scopedomain.ScopeGroup, 700)
	policy.Enabled = true
	policy.StoreMessages = true
	policy.AllowMemoryRead = true
	policy.AllowHistoryRead = false
	scopeService, err := scopeapp.NewService(&fakeScopeRepo{policy: policy})
	if err != nil {
		t.Fatalf("new scope service: %v", err)
	}
	conversationRepo := &fakeConversationRepo{
		found:        true,
		conversation: conversationdomain.Conversation{ID: "conv-target", BotID: 7, Kind: conversationdomain.KindGroup, TargetID: 700},
	}
	handler := NewHandler(&svc.ServiceContext{
		Memory: memoryService, Scope: scopeService,
		Conversation: testConversationService(t, conversationRepo),
	})
	rec := httptest.NewRecorder()
	handler.Preview(rec, httptest.NewRequest(http.MethodPost, "/api/v1/knowledge/preview", strings.NewReader(
		`{"bot_id":7,"preview_mode":"real","target_type":"group","target_id":700,"conversation_id":"conv-other"}`,
	)))
	if rec.Code != http.StatusOK {
		t.Fatalf("mismatched conversation status = %d body=%s, want 200 skipped context", rec.Code, rec.Body.String())
	}
	if repo.searchCalled {
		t.Fatal("mismatched conversation id reached memory search")
	}
	data := decodeData(t, rec.Body.Bytes())
	target, _ := data["Target"].(map[string]any)
	if target["ConversationID"] != "" {
		t.Fatalf("mismatched conversation id echoed in Target: %#v", target)
	}
	contextData, _ := data["Context"].(map[string]any)
	for _, key := range []string{"Memory", "History"} {
		layer, _ := contextData[key].(map[string]any)
		if layer["Skipped"] != true || layer["Reason"] != "conversation_target_mismatch" {
			t.Fatalf("%s after mismatch = %#v", key, layer)
		}
	}
}

func TestPreviewRealGroupPrivateSameNumberDoesNotShareConversation(t *testing.T) {
	repo := &fakeMemoryRepo{}
	memoryService, err := memoryapp.NewService(repo)
	if err != nil {
		t.Fatalf("new memory service: %v", err)
	}
	policy := scopedomain.Default(scopedomain.ScopePrivate, 700)
	policy.Enabled = true
	policy.StoreMessages = true
	policy.AllowMemoryRead = true
	policy.AllowHistoryRead = false
	scopeService, err := scopeapp.NewService(&fakeScopeRepo{policy: policy})
	if err != nil {
		t.Fatalf("new scope service: %v", err)
	}
	conversationRepo := &fakeConversationRepo{
		found:        true,
		conversation: conversationdomain.Conversation{ID: "conv-group", BotID: 7, Kind: conversationdomain.KindGroup, TargetID: 700},
	}
	handler := NewHandler(&svc.ServiceContext{
		Memory: memoryService, Scope: scopeService,
		Conversation: testConversationService(t, conversationRepo),
	})
	rec := httptest.NewRecorder()
	handler.Preview(rec, httptest.NewRequest(http.MethodPost, "/api/v1/knowledge/preview", strings.NewReader(
		`{"bot_id":7,"preview_mode":"real","target_type":"private","target_id":700,"conversation_id":"conv-group"}`,
	)))
	if rec.Code != http.StatusOK {
		t.Fatalf("same-number group/private status = %d body=%s", rec.Code, rec.Body.String())
	}
	if conversationRepo.kind != conversationdomain.KindPrivate || conversationRepo.targetID != 700 {
		t.Fatalf("conversation lookup kind=%s target=%d, want private/700", conversationRepo.kind, conversationRepo.targetID)
	}
	if repo.searchCalled {
		t.Fatal("group conversation id reached private memory search")
	}
	contextData, _ := decodeData(t, rec.Body.Bytes())["Context"].(map[string]any)
	memory, _ := contextData["Memory"].(map[string]any)
	if memory["Skipped"] != true || memory["Reason"] != "conversation_not_found" {
		t.Fatalf("same-number memory = %#v", memory)
	}
}

func TestPreviewRealMissingConversationServiceSkipsContext(t *testing.T) {
	repo := &fakeMemoryRepo{}
	memoryService, err := memoryapp.NewService(repo)
	if err != nil {
		t.Fatalf("new memory service: %v", err)
	}
	policy := scopedomain.Default(scopedomain.ScopeGroup, 700)
	policy.Enabled = true
	policy.StoreMessages = true
	policy.AllowMemoryRead = true
	policy.AllowHistoryRead = false
	scopeService, err := scopeapp.NewService(&fakeScopeRepo{policy: policy})
	if err != nil {
		t.Fatalf("new scope service: %v", err)
	}
	handler := NewHandler(&svc.ServiceContext{Memory: memoryService, Scope: scopeService})
	rec := httptest.NewRecorder()
	handler.Preview(rec, httptest.NewRequest(http.MethodPost, "/api/v1/knowledge/preview", strings.NewReader(
		`{"bot_id":7,"preview_mode":"real","target_type":"group","target_id":700,"conversation_id":"conv-unknown"}`,
	)))
	if rec.Code != http.StatusOK {
		t.Fatalf("missing conversation service status = %d body=%s", rec.Code, rec.Body.String())
	}
	if repo.searchCalled {
		t.Fatal("unverifiable conversation id reached memory search")
	}
	data := decodeData(t, rec.Body.Bytes())
	target, _ := data["Target"].(map[string]any)
	if target["ConversationID"] != "" {
		t.Fatalf("unverifiable conversation id echoed in Target: %#v", target)
	}
	contextData, _ := data["Context"].(map[string]any)
	for _, key := range []string{"Memory", "History"} {
		layer, _ := contextData[key].(map[string]any)
		if layer["Skipped"] != true || layer["Reason"] != "conversation_unavailable" {
			t.Fatalf("%s without conversation service = %#v", key, layer)
		}
	}
}

func TestPreviewRealValidatesConversationIDWhenHistoryDenied(t *testing.T) {
	repo := &fakeMemoryRepo{}
	memoryService, err := memoryapp.NewService(repo)
	if err != nil {
		t.Fatalf("new memory service: %v", err)
	}
	policy := scopedomain.Default(scopedomain.ScopeGroup, 700)
	policy.Enabled = true
	policy.StoreMessages = true
	policy.AllowMemoryRead = true
	policy.AllowHistoryRead = false
	scopeService, err := scopeapp.NewService(&fakeScopeRepo{policy: policy})
	if err != nil {
		t.Fatalf("new scope service: %v", err)
	}
	conversationRepo := &fakeConversationRepo{
		found:        true,
		conversation: conversationdomain.Conversation{ID: "conv-target", BotID: 7, Kind: conversationdomain.KindGroup, TargetID: 700},
	}
	handler := NewHandler(&svc.ServiceContext{
		Memory: memoryService, Scope: scopeService,
		Conversation: testConversationService(t, conversationRepo),
	})
	rec := httptest.NewRecorder()
	handler.Preview(rec, httptest.NewRequest(http.MethodPost, "/api/v1/knowledge/preview", strings.NewReader(
		`{"bot_id":7,"preview_mode":"real","target_type":"group","target_id":700,"conversation_id":"conv-target","query":"长期"}`,
	)))
	if rec.Code != http.StatusOK {
		t.Fatalf("matching conversation status = %d body=%s", rec.Code, rec.Body.String())
	}
	if !repo.searchCalled || repo.searchConversationID != "conv-target" {
		t.Fatalf("memory search conversation id = %q called=%t, want validated conv-target", repo.searchConversationID, repo.searchCalled)
	}
}
