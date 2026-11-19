package infrastructure

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/HappyLadySauceM/Nagi/internal/conversation/domain"
)

// HistoryIndex stores searchable message projections and returns PostgreSQL IDs.
// HistoryIndex 保存可检索的消息投影，查询返回 PostgreSQL 消息 ID。
type HistoryIndex struct {
	base   *url.URL
	apiKey string
	client *http.Client
}

const historyIndexName = "nagi-chat-history-v1"

const historyMapping = `{
  "settings":{"number_of_shards":1,"number_of_replicas":0},
  "mappings":{
    "dynamic":"strict",
    "_source":{"excludes":["text"]},
    "properties":{
      "bot_id":{"type":"long"},
      "conversation_id":{"type":"keyword"},
      "sender_id":{"type":"long"},
      "created_at":{"type":"date_nanos"},
      "message_id":{"type":"long"},
      "text":{"type":"text","analyzer":"cjk"}
    }
  }
}`

// NewHistoryIndex connects to a configured Elasticsearch origin without following redirects.
// NewHistoryIndex 连接配置的 Elasticsearch 源站，并禁止跟随重定向。
func NewHistoryIndex(endpoint, apiKey string, client *http.Client) (*HistoryIndex, error) {
	base, err := url.Parse(endpoint)
	if err != nil || base.Host == "" || base.User != nil || base.RawQuery != "" || base.Fragment != "" ||
		(base.Scheme != "https" && base.Scheme != "http") || strings.TrimSpace(apiKey) == "" {
		return nil, errors.New("conversation: invalid Elasticsearch endpoint or API key")
	}
	if base.Scheme == "http" {
		ip := net.ParseIP(base.Hostname())
		if base.Hostname() != "localhost" && (ip == nil || !ip.IsLoopback()) {
			return nil, errors.New("conversation: Elasticsearch requires HTTPS")
		}
	}
	if client == nil {
		client = http.DefaultClient
	}
	secure := *client
	secure.Timeout = 5 * time.Second
	secure.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &HistoryIndex{base: base, apiKey: apiKey, client: &secure}, nil
}

// Ensure creates the fixed versioned index if another replica has not done so.
// Ensure 创建固定版本索引，允许其他副本先创建。
func (h *HistoryIndex) Ensure(ctx context.Context) error {
	var result struct {
		Error struct {
			Type string `json:"type"`
		} `json:"error"`
	}
	status, err := h.request(ctx, http.MethodPut, historyIndexName, "application/json", strings.NewReader(historyMapping), &result)
	if err != nil {
		return err
	}
	if status == http.StatusBadRequest && result.Error.Type == "resource_already_exists_exception" {
		return nil
	}
	return historyHTTPError(status)
}

// Index replaces documents idempotently and checks item-level bulk errors.
// Index 幂等覆盖文档，并检查批量响应的逐项错误。
func (h *HistoryIndex) Index(ctx context.Context, documents []domain.HistoryDocument, removals []int64) error {
	if len(documents) == 0 && len(removals) == 0 {
		return nil
	}
	var body bytes.Buffer
	encoder := json.NewEncoder(&body)
	for _, document := range documents {
		if err := encoder.Encode(map[string]any{"index": map[string]string{"_id": strconv.FormatInt(document.ID, 10)}}); err != nil {
			return err
		}
		if err := encoder.Encode(struct {
			domain.HistoryDocument
			MessageID int64 `json:"message_id"`
		}{HistoryDocument: document, MessageID: document.ID}); err != nil {
			return err
		}
	}
	for _, id := range removals {
		if err := encoder.Encode(map[string]any{"delete": map[string]string{"_id": strconv.FormatInt(id, 10)}}); err != nil {
			return err
		}
	}
	var result struct {
		Errors bool `json:"errors"`
		Items  []map[string]struct {
			Status int `json:"status"`
		} `json:"items"`
	}
	code, err := h.request(ctx, http.MethodPost, historyIndexName+"/_bulk", "application/x-ndjson", &body, &result)
	if err != nil {
		return err
	}
	if err := historyHTTPError(code); err != nil {
		return err
	}
	if len(result.Items) != len(documents)+len(removals) {
		return errors.New("conversation: Elasticsearch bulk response is incomplete")
	}
	if result.Errors {
		for _, item := range result.Items {
			for action, result := range item {
				if (result.Status < 200 || result.Status >= 300) && !(action == "delete" && result.Status == 404) {
					return errors.New("conversation: Elasticsearch rejected one or more history documents")
				}
			}
		}
	}
	return nil
}

// Search returns ranked IDs only. The caller must reread them from PostgreSQL.
// Search 只返回按相关度排名的 ID，调用方必须从 PostgreSQL 重新读取。
func (h *HistoryIndex) Search(ctx context.Context, q domain.HistorySearch) ([]int64, error) {
	filters := []any{
		map[string]any{"term": map[string]any{"bot_id": q.BotID}},
		map[string]any{"term": map[string]any{"conversation_id": q.ConversationID}},
		map[string]any{"bool": map[string]any{
			"minimum_should_match": 1,
			"should": []any{
				map[string]any{"range": map[string]any{"created_at": map[string]any{"lt": q.Before}}},
				map[string]any{"bool": map[string]any{"filter": []any{
					map[string]any{"term": map[string]any{"created_at": q.Before}},
					map[string]any{"range": map[string]any{"message_id": map[string]any{"lt": q.BeforeID}}},
				}}},
			},
		}},
	}
	if q.SenderID > 0 {
		filters = append(filters, map[string]any{"term": map[string]any{"sender_id": q.SenderID}})
	}
	if !q.After.IsZero() {
		filters = append(filters, map[string]any{"range": map[string]any{"created_at": map[string]any{"gte": q.After}}})
	}
	body, err := json.Marshal(map[string]any{
		"size": q.Limit, "_source": false, "track_total_hits": false, "timeout": "1500ms",
		"query": map[string]any{"bool": map[string]any{
			"filter": filters,
			"must":   []any{map[string]any{"match": map[string]any{"text": q.Query}}},
		}},
		"sort": []any{"_score", map[string]string{"created_at": "desc"}, map[string]string{"message_id": "desc"}},
	})
	if err != nil {
		return nil, err
	}
	var response struct {
		TimedOut bool `json:"timed_out"`
		Shards   struct {
			Failed int `json:"failed"`
		} `json:"_shards"`
		Hits struct {
			Hits []struct {
				ID string `json:"_id"`
			} `json:"hits"`
		} `json:"hits"`
	}
	code, err := h.request(ctx, http.MethodPost, historyIndexName+"/_search", "application/json", bytes.NewReader(body), &response)
	if err != nil {
		return nil, err
	}
	if err := historyHTTPError(code); err != nil {
		return nil, err
	}
	if response.TimedOut || response.Shards.Failed != 0 {
		return nil, errors.New("conversation: Elasticsearch returned an incomplete search")
	}
	ids := make([]int64, 0, len(response.Hits.Hits))
	for _, hit := range response.Hits.Hits {
		id, err := strconv.ParseInt(hit.ID, 10, 64)
		if err != nil || id <= 0 {
			return nil, errors.New("conversation: invalid history document ID from Elasticsearch")
		}
		ids = append(ids, id)
	}
	return ids, nil
}

// DeleteBefore evicts projections outside the configured history retention window.
// DeleteBefore 清除超出聊天记录留存期限的投影。
func (h *HistoryIndex) DeleteBefore(ctx context.Context, cutoff time.Time) error {
	body, err := json.Marshal(map[string]any{"query": map[string]any{"range": map[string]any{"created_at": map[string]any{"lt": cutoff}}}})
	if err != nil {
		return err
	}
	var result struct {
		TimedOut bool              `json:"timed_out"`
		Failures []json.RawMessage `json:"failures"`
	}
	code, err := h.request(ctx, http.MethodPost, historyIndexName+"/_delete_by_query?conflicts=proceed", "application/json", bytes.NewReader(body), &result)
	if err != nil {
		return err
	}
	if err := historyHTTPError(code); err != nil {
		return err
	}
	if result.TimedOut || len(result.Failures) != 0 {
		return errors.New("conversation: Elasticsearch history retention incomplete")
	}
	return nil
}

func (h *HistoryIndex) request(ctx context.Context, method, path, contentType string, body io.Reader, result any) (int, error) {
	route, query, _ := strings.Cut(path, "?")
	endpoint := h.base.JoinPath(route)
	endpoint.RawQuery = query
	req, err := http.NewRequestWithContext(ctx, method, endpoint.String(), body)
	if err != nil {
		return 0, err
	}
	req.Header.Set("Authorization", "ApiKey "+h.apiKey)
	req.Header.Set("Content-Type", contentType)
	response, err := h.client.Do(req)
	if err != nil {
		return 0, fmt.Errorf("conversation: call Elasticsearch: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode < http.StatusBadRequest || response.StatusCode == http.StatusBadRequest && method == http.MethodPut {
		if err := json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(result); err != nil {
			return 0, fmt.Errorf("conversation: decode Elasticsearch response: %w", err)
		}
	}
	return response.StatusCode, nil
}

func historyHTTPError(code int) error {
	if code < 200 || code >= 300 {
		return fmt.Errorf("conversation: Elasticsearch HTTP %d", code)
	}
	return nil
}
