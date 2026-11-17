package intent

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/HappyLadySauceM/Nagi/cmd/app/svc"
)

func TestUnifiedPromptRoutesAreRegistered(t *testing.T) {
	mux := http.NewServeMux()
	NewHandler(&svc.ServiceContext{Logger: slog.Default()}).Register(mux)
	cases := []struct {
		method string
		path   string
	}{
		{http.MethodGet, "/api/v1/prompts"},
		{http.MethodGet, "/api/v1/prompts/chat.reply"},
		{http.MethodGet, "/api/v1/prompts/versions/12"},
		{http.MethodGet, "/api/v1/prompts/chat.reply/diff"},
		{http.MethodGet, "/api/v1/prompts/chat.reply/effective"},
		{http.MethodPost, "/api/v1/prompts/chat.reply/versions"},
		{http.MethodPost, "/api/v1/prompts/chat.reply/preview"},
		{http.MethodPost, "/api/v1/prompts/chat.reply/rollback"},
		{http.MethodPost, "/api/v1/prompts/chat.reply/restore-builtin"},
		{http.MethodPost, "/api/v1/prompts/versions/12/publish"},
		{http.MethodPatch, "/api/v1/prompts/versions/12"},
		{http.MethodDelete, "/api/v1/prompts/versions/12"},
	}
	for _, tc := range cases {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest(tc.method, tc.path, nil))
		if rec.Code == http.StatusNotFound {
			t.Fatalf("%s %s not registered", tc.method, tc.path)
		}
	}
}
