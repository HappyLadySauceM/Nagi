package modelhttp

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHeadersOverrideWithoutRedirectLeak(t *testing.T) {
	leaked := false
	sink := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { leaked = true }))
	defer sink.Close()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Private") != "private-value" || r.Header.Get("Authorization") != "custom-auth" {
			t.Error("missing custom headers")
		}
		if r.URL.Path == "/redirect" {
			http.Redirect(w, r, sink.URL, http.StatusFound)
			return
		}
		io.WriteString(w, "ok")
	}))
	defer server.Close()
	headers := map[string]string{"X-Private": "private-value", "Authorization": "custom-auth"}
	client := Client(nil, headers)
	headers["X-Private"] = "mutated"
	for _, path := range []string{"/", "/redirect"} {
		req, _ := http.NewRequest("GET", server.URL+path, nil)
		req.Header.Set("Authorization", "default")
		resp, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if req.Header.Get("X-Private") != "" {
			t.Fatal("request mutated")
		}
	}
	if leaked {
		t.Fatal("redirect received private headers")
	}
}

func TestInvalidHeaderDrafts(t *testing.T) {
	for _, headers := range []map[string]string{{"X-A": "a\r\nb"}, {"X A": "b"}, {"x-a": "one", "X-A": "two"}, {"Host": "other"}, {"X-A": string(make([]byte, 8193))}} {
		if _, err := Normalize(headers); err == nil {
			t.Fatal("invalid draft accepted")
		}
	}
	if _, err := Normalize(map[string]string{"X-Custom": "value", "Authorization": "Bearer secret"}); err != nil {
		t.Fatal(err)
	}
}
