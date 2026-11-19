package infrastructure

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/HappyLadySauceM/Nagi/internal/conversation/domain"
)

func TestHistoryIndexUsesScopedSearchAndDetectsPartialBulkFailure(t *testing.T) {
	var failBulk atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "ApiKey fixture" {
			t.Error("missing Elasticsearch authorization")
		}
		switch r.URL.Path {
		case "/" + historyIndexName:
			body, _ := io.ReadAll(r.Body)
			if !strings.Contains(string(body), `"analyzer":"cjk"`) || !strings.Contains(string(body), `"excludes":["text"]`) {
				t.Error("history index must analyze Chinese text without storing it in _source")
			}
			w.WriteHeader(http.StatusBadRequest)
			_, _ = io.WriteString(w, `{"error":{"type":"resource_already_exists_exception"}}`)
		case "/" + historyIndexName + "/_bulk":
			payload, _ := io.ReadAll(r.Body)
			if !strings.Contains(string(payload), `"_id":"12"`) || !strings.Contains(string(payload), `"message_id":12`) ||
				!strings.Contains(string(payload), `"_id":"13"`) {
				t.Error("bulk request is missing stable PostgreSQL IDs")
			}
			if failBulk.Load() {
				_, _ = io.WriteString(w, `{"errors":true,"items":[{"index":{"status":429}},{"delete":{"status":404}}]}`)
			} else {
				_, _ = io.WriteString(w, `{"errors":true,"items":[{"index":{"status":201}},{"delete":{"status":404}}]}`)
			}
		case "/" + historyIndexName + "/_search":
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Error(err)
			}
			query, _ := json.Marshal(body["query"])
			for _, filter := range []string{`"bot_id":2`, `"conversation_id":"conversation-a"`, `"sender_id":8`, `"message_id":{"lt":40}`, `"text":"接口协议"`} {
				if !strings.Contains(string(query), filter) {
					t.Errorf("missing query constraint %s", filter)
				}
			}
			if body["_source"] != false {
				t.Error("Elasticsearch hits must return IDs only")
			}
			_, _ = io.WriteString(w, `{"hits":{"hits":[{"_id":"12"},{"_id":"15"}]}}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	index, err := NewHistoryIndex(server.URL, "fixture", server.Client())
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := index.Ensure(ctx); err != nil {
		t.Fatal(err)
	}
	docs := []domain.HistoryDocument{{ID: 12, BotID: 2, ConversationID: "conversation-a", Text: "接口协议"}}
	if err := index.Index(ctx, docs, []int64{13}); err != nil {
		t.Fatalf("deleting an already missing document must be idempotent: %v", err)
	}
	failBulk.Store(true)
	if err := index.Index(ctx, docs, []int64{13}); err == nil {
		t.Fatal("partial bulk failure was accepted")
	}
	ids, err := index.Search(ctx, domain.HistorySearch{
		BotID: 2, ConversationID: "conversation-a", BeforeID: 40, Before: time.Now(),
		SenderID: 8, Query: "接口协议", Limit: 5,
	})
	if err != nil || len(ids) != 2 || ids[0] != 12 || ids[1] != 15 {
		t.Fatalf("ids=%v err=%v", ids, err)
	}
}
