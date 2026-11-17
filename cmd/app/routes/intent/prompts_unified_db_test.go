package intent

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/HappyLadySauceM/Nagi/cmd/app/svc"
	intentapp "github.com/HappyLadySauceM/Nagi/internal/intent/application"
	intentinfra "github.com/HappyLadySauceM/Nagi/internal/intent/infrastructure"
	"github.com/HappyLadySauceM/Nagi/pkg/db/dbtest"
)

func unifiedPromptFixture(t *testing.T) *http.ServeMux {
	t.Helper()
	pool := dbtest.Open(t)
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM intent_prompt_versions WHERE bot_id IN (990101,990102,990103,990104,990105,990106)`)
	})
	repo, err := intentinfra.NewPromptsRepo(pool)
	if err != nil {
		t.Fatalf("new prompt repo: %v", err)
	}
	mux := http.NewServeMux()
	NewHandler(&svc.ServiceContext{Prompts: intentapp.NewPromptService(repo, slog.Default())}).Register(mux)
	return mux
}

func unifiedRequest(t *testing.T, mux *http.ServeMux, method, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var reader *bytes.Reader
	if body == nil {
		reader = bytes.NewReader(nil)
	} else {
		payload, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal body: %v", err)
		}
		reader = bytes.NewReader(payload)
	}
	req := httptest.NewRequest(method, path, reader)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

func TestUnifiedPromptHTTPCreatePublishEffectiveRestore(t *testing.T) {
	mux := unifiedPromptFixture(t)
	rec := unifiedRequest(t, mux, http.MethodGet, "/api/v1/prompts?bot_id=990101", nil)
	if rec.Code != http.StatusOK || !bytes.Contains(rec.Body.Bytes(), []byte("chat.reply")) || !bytes.Contains(rec.Body.Bytes(), []byte("memory.extraction")) {
		t.Fatalf("list status = %d body=%s", rec.Code, rec.Body.String())
	}
	rec = unifiedRequest(t, mux, http.MethodPost, "/api/v1/prompts/chat.reply/versions", map[string]any{
		"bot_id": 990101, "scope_type": "account", "scope_id": 0, "provider": "openai",
		"name": "chat account", "base": "builtin",
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create status = %d body=%s", rec.Code, rec.Body.String())
	}
	var created struct {
		Code int `json:"code"`
		Data struct {
			ID int64
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil || created.Data.ID == 0 {
		t.Fatalf("create response = %s err=%v", rec.Body.String(), err)
	}
	rec = unifiedRequest(t, mux, http.MethodGet, "/api/v1/prompts/chat.reply/diff?bot_id=990101&scope_type=account&provider=openai&from=builtin&to=draft", nil)
	if rec.Code != http.StatusOK || !bytes.Contains(rec.Body.Bytes(), []byte(`"Changed"`)) {
		t.Fatalf("diff status = %d body=%s", rec.Code, rec.Body.String())
	}
	rec = unifiedRequest(t, mux, http.MethodPost, "/api/v1/prompts/versions/"+strconv.FormatInt(created.Data.ID, 10)+"/publish", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("publish status = %d body=%s", rec.Code, rec.Body.String())
	}
	rec = unifiedRequest(t, mux, http.MethodGet, "/api/v1/prompts/chat.reply/effective?bot_id=990101&target_type=group&target_id=700&provider=openai", nil)
	if rec.Code != http.StatusOK || !bytes.Contains(rec.Body.Bytes(), []byte(`"Final"`)) || !bytes.Contains(rec.Body.Bytes(), []byte(`"Layers"`)) {
		t.Fatalf("effective status = %d body=%s", rec.Code, rec.Body.String())
	}
	rec = unifiedRequest(t, mux, http.MethodPost, "/api/v1/prompts/chat.reply/restore-builtin", map[string]any{
		"bot_id": 990101, "scope_type": "account", "scope_id": 0, "provider": "openai",
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("restore status = %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestUnifiedPromptDeleteHTTPContract(t *testing.T) {
	mux := unifiedPromptFixture(t)
	create := func() int64 {
		rec := unifiedRequest(t, mux, http.MethodPost, "/api/v1/prompts/chat.reply/versions", map[string]any{
			"bot_id": 990105, "scope_type": "account", "scope_id": 0, "provider": "openai",
			"name": "delete fixture", "base": "builtin",
		})
		if rec.Code != http.StatusCreated {
			t.Fatalf("create = %d body=%s", rec.Code, rec.Body.String())
		}
		var body struct {
			Data struct{ ID int64 }
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil || body.Data.ID == 0 {
			t.Fatalf("create response = %s err=%v", rec.Body.String(), err)
		}
		return body.Data.ID
	}

	draftID := create()
	path := "/api/v1/prompts/versions/" + strconv.FormatInt(draftID, 10) + "?bot_id=990105"
	if rec := unifiedRequest(t, mux, http.MethodDelete, path, nil); rec.Code != http.StatusOK {
		t.Fatalf("delete draft = %d body=%s", rec.Code, rec.Body.String())
	}
	if rec := unifiedRequest(t, mux, http.MethodGet, "/api/v1/prompts/versions/"+strconv.FormatInt(draftID, 10), nil); rec.Code != http.StatusNotFound {
		t.Fatalf("get deleted draft = %d body=%s", rec.Code, rec.Body.String())
	}
	if rec := unifiedRequest(t, mux, http.MethodDelete, path, nil); rec.Code != http.StatusNotFound {
		t.Fatalf("repeat delete = %d body=%s", rec.Code, rec.Body.String())
	}

	wrongBotID := create()
	if rec := unifiedRequest(t, mux, http.MethodDelete, "/api/v1/prompts/versions/"+strconv.FormatInt(wrongBotID, 10)+"?bot_id=990106", nil); rec.Code != http.StatusNotFound {
		t.Fatalf("wrong bot delete = %d body=%s", rec.Code, rec.Body.String())
	}
	if rec := unifiedRequest(t, mux, http.MethodGet, "/api/v1/prompts/versions/"+strconv.FormatInt(wrongBotID, 10), nil); rec.Code != http.StatusOK {
		t.Fatalf("wrong bot delete removed version = %d body=%s", rec.Code, rec.Body.String())
	}

	publishedID := create()
	if rec := unifiedRequest(t, mux, http.MethodPost, "/api/v1/prompts/versions/"+strconv.FormatInt(publishedID, 10)+"/publish", nil); rec.Code != http.StatusOK {
		t.Fatalf("publish = %d body=%s", rec.Code, rec.Body.String())
	}
	if rec := unifiedRequest(t, mux, http.MethodDelete, "/api/v1/prompts/versions/"+strconv.FormatInt(publishedID, 10)+"?bot_id=990105", nil); rec.Code != http.StatusConflict {
		t.Fatalf("delete published = %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestUnifiedPromptMemorySupplementAndVariableValidation(t *testing.T) {
	mux := unifiedPromptFixture(t)
	body := map[string]any{
		"bot_id": 990102, "scope_type": "group", "scope_id": 0, "provider": "openai",
		"content": map[string]any{"Provider": "openai", "OpenAI": map[string]any{"SystemPrompt": "补充：优先提取偏好。"}},
	}
	rec := unifiedRequest(t, mux, http.MethodPost, "/api/v1/prompts/memory.extraction/versions", body)
	if rec.Code != http.StatusCreated {
		t.Fatalf("memory supplement status = %d body=%s", rec.Code, rec.Body.String())
	}
	var created struct {
		Data struct {
			ID int64
		}
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil || created.Data.ID == 0 {
		t.Fatalf("memory supplement response = %s err=%v", rec.Body.String(), err)
	}
	rec = unifiedRequest(t, mux, http.MethodPost, "/api/v1/prompts/versions/"+strconv.FormatInt(created.Data.ID, 10)+"/publish", nil)
	if rec.Code != http.StatusOK || !bytes.Contains(rec.Body.Bytes(), []byte(`"Version"`)) || !bytes.Contains(rec.Body.Bytes(), []byte(`"ArchivedVersionID"`)) {
		t.Fatalf("memory supplement publish = %d body=%s", rec.Code, rec.Body.String())
	}
	rec = unifiedRequest(t, mux, http.MethodGet, "/api/v1/prompts/memory.extraction/effective?bot_id=990102&target_type=group&target_id=700&provider=openai", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("memory effective status = %d body=%s", rec.Code, rec.Body.String())
	}
	if !bytes.Contains(rec.Body.Bytes(), []byte("补充：优先提取偏好。")) {
		t.Fatalf("memory supplement not merged: %s", rec.Body.String())
	}
	invalid := map[string]any{
		"bot_id": 990102, "scope_type": "group", "scope_id": 0, "provider": "openai",
		"content": map[string]any{"Provider": "openai", "OpenAI": map[string]any{"SystemPrompt": "{{messages_json}}"}},
	}
	rec = unifiedRequest(t, mux, http.MethodPost, "/api/v1/prompts/memory.extraction/versions", invalid)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("invalid variable status = %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestUnifiedPromptHTTPPublishRollbackContract(t *testing.T) {
	mux := unifiedPromptFixture(t)
	create := func(name string) int64 {
		rec := unifiedRequest(t, mux, http.MethodPost, "/api/v1/prompts/chat.reply/versions", map[string]any{
			"bot_id": 990103, "scope_type": "account", "scope_id": 0, "provider": "openai",
			"name": name, "base": "auto",
		})
		if rec.Code != http.StatusCreated {
			t.Fatalf("create %s = %d body=%s", name, rec.Code, rec.Body.String())
		}
		var body struct {
			Data struct{ ID int64 }
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil || body.Data.ID == 0 {
			t.Fatalf("create response = %s err=%v", rec.Body.String(), err)
		}
		return body.Data.ID
	}
	first := create("v1")
	if rec := unifiedRequest(t, mux, http.MethodPost, "/api/v1/prompts/versions/"+strconv.FormatInt(first, 10)+"/publish", nil); rec.Code != http.StatusOK {
		t.Fatalf("publish v1 = %d body=%s", rec.Code, rec.Body.String())
	}
	second := create("v2")
	if rec := unifiedRequest(t, mux, http.MethodPost, "/api/v1/prompts/versions/"+strconv.FormatInt(second, 10)+"/publish", nil); rec.Code != http.StatusOK {
		t.Fatalf("publish v2 = %d body=%s", rec.Code, rec.Body.String())
	}
	rec := unifiedRequest(t, mux, http.MethodPost, "/api/v1/prompts/chat.reply/rollback", map[string]any{
		"bot_id": 990103, "scope_type": "account", "scope_id": 0, "provider": "openai", "version_id": first,
	})
	if rec.Code != http.StatusOK || !bytes.Contains(rec.Body.Bytes(), []byte(`"Version"`)) || !bytes.Contains(rec.Body.Bytes(), []byte(`"ArchivedVersionID"`)) {
		t.Fatalf("rollback = %d body=%s", rec.Code, rec.Body.String())
	}
	draft := create("v3")
	rec = unifiedRequest(t, mux, http.MethodPost, "/api/v1/prompts/chat.reply/rollback", map[string]any{
		"bot_id": 990103, "scope_type": "account", "scope_id": 0, "provider": "openai", "version_id": draft,
	})
	if rec.Code != http.StatusConflict {
		t.Fatalf("draft rollback = %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestUnifiedPromptPreviewExampleRendersVariablesAndSample(t *testing.T) {
	mux := unifiedPromptFixture(t)
	rec := unifiedRequest(t, mux, http.MethodPost, "/api/v1/prompts/chat.reply/preview", map[string]any{
		"bot_id": 990104, "provider": "openai", "preview_mode": "example",
		"sample": map[string]any{"current_message": "在吗"},
	})
	var chatPreview struct {
		Data struct {
			Final struct {
				Content map[string]any
			}
		}
	}
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &chatPreview) != nil {
		t.Fatalf("chat example preview = %d body=%s", rec.Code, rec.Body.String())
	}
	chatOpenAI, _ := chatPreview.Data.Final.Content["OpenAI"].(map[string]any)
	chatSystem, _ := chatOpenAI["SystemPrompt"].(string)
	if strings.Contains(chatSystem, "{{scope_type}}") || !strings.Contains(chatSystem, "group") {
		t.Fatalf("chat rendered system = %q", chatSystem)
	}
	rec = unifiedRequest(t, mux, http.MethodPost, "/api/v1/prompts/memory.extraction/preview", map[string]any{
		"bot_id": 990104, "provider": "openai", "preview_mode": "example",
		"sample": map[string]any{"messages_json": "hello"},
	})
	var memoryPreview struct {
		Data struct {
			Final struct {
				Content map[string]any
			}
		}
	}
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &memoryPreview) != nil {
		t.Fatalf("memory example preview = %d body=%s", rec.Code, rec.Body.String())
	}
	memoryOpenAI, _ := memoryPreview.Data.Final.Content["OpenAI"].(map[string]any)
	memoryUser, _ := memoryOpenAI["UserTemplate"].(string)
	if !strings.Contains(memoryUser, "hello") || strings.Contains(memoryUser, "{{messages_json}}") {
		t.Fatalf("memory rendered user template = %q", memoryUser)
	}
	rec = unifiedRequest(t, mux, http.MethodPost, "/api/v1/prompts/intent.participation/preview", map[string]any{
		"bot_id": 990104, "provider": "openai", "preview_mode": "example",
		"sample": map[string]any{"current_message": "在吗", "recent": []any{map[string]any{"Text": "你好"}}},
	})
	var intentPreview struct {
		Data struct {
			Final struct {
				Content map[string]any
			}
		}
	}
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &intentPreview) != nil {
		t.Fatalf("intent example preview = %d body=%s", rec.Code, rec.Body.String())
	}
	intentOpenAI, _ := intentPreview.Data.Final.Content["OpenAI"].(map[string]any)
	intentUser, _ := intentOpenAI["UserTemplate"].(string)
	if strings.Contains(intentUser, "{{") || !strings.Contains(intentUser, "在吗") || !strings.Contains(intentUser, "你好") {
		t.Fatalf("intent rendered user template = %q", intentUser)
	}
}
