package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

func TestLangfusePublishDoesNotOverwriteAnnotation(t *testing.T) {
	var writes atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/public/v2/datasets/nagi":
			w.WriteHeader(http.StatusOK)
		case r.Method == http.MethodGet && r.URL.Path == "/api/public/dataset-items/annotated":
			_, _ = w.Write([]byte(`{"datasetName":"nagi","metadata":{"split":"tuning"},"expectedOutput":{"participation":"must_reply"}}`))
		case r.Method == http.MethodGet && r.URL.Path == "/api/public/dataset-items/new":
			w.WriteHeader(http.StatusNotFound)
		case r.Method == http.MethodPost && r.URL.Path == "/api/public/dataset-items":
			writes.Add(1)
			w.WriteHeader(http.StatusCreated)
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusBadRequest)
		}
	}))
	defer server.Close()
	client, err := newLangfuseClient(server.URL, "public-fixture", "secret-fixture")
	if err != nil {
		t.Fatal(err)
	}
	meta := itemMetadata{Split: "tuning"}
	err = client.publish(context.Background(), "nagi", []datasetItem{{ID: "annotated", Metadata: meta}, {ID: "new", Metadata: meta}})
	if err != nil || writes.Load() != 1 {
		t.Fatalf("writes=%d error=%v", writes.Load(), err)
	}
}
