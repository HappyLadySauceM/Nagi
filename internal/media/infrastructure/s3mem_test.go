package infrastructure

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

// newMemoryS3 starts a path-style object store for integration tests.
// newMemoryS3 为集成测试启动路径风格的对象存储模拟。
func newMemoryS3(t *testing.T) (*minio.Client, string) {
	t.Helper()
	type object struct {
		data []byte
		meta map[string]string
	}
	store := struct {
		sync.Mutex
		buckets map[string]map[string]object
	}{buckets: make(map[string]map[string]object)}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path, err := url.PathUnescape(strings.TrimPrefix(r.URL.Path, "/"))
		if err != nil {
			http.Error(w, "bad path", http.StatusBadRequest)
			return
		}
		parts := strings.SplitN(path, "/", 2)
		bucket := ""
		key := ""
		if len(parts) > 0 {
			bucket = parts[0]
		}
		if len(parts) > 1 {
			key = parts[1]
		}
		switch r.Method {
		case http.MethodPut:
			store.Lock()
			defer store.Unlock()
			if key == "" {
				if store.buckets[bucket] == nil {
					store.buckets[bucket] = make(map[string]object)
				}
				w.WriteHeader(http.StatusOK)
				return
			}
			if store.buckets[bucket] == nil {
				http.Error(w, "NoSuchBucket", http.StatusNotFound)
				return
			}
			body, _ := io.ReadAll(io.LimitReader(r.Body, 64<<20))
			meta := map[string]string{}
			for k, v := range r.Header {
				lk := strings.ToLower(k)
				if strings.HasPrefix(lk, "x-amz-meta-") {
					meta[strings.TrimPrefix(lk, "x-amz-meta-")] = v[0]
				}
			}
			store.buckets[bucket][key] = object{data: stripAWSChunked(body), meta: meta}
			w.Header().Set("ETag", `"mock"`)
			w.WriteHeader(http.StatusOK)
		case http.MethodGet:
			store.Lock()
			defer store.Unlock()
			obj, ok := store.buckets[bucket][key]
			if !ok {
				http.Error(w, "NoSuchKey", http.StatusNotFound)
				return
			}
			w.Header().Set("Content-Length", strconv.Itoa(len(obj.data)))
			w.Header().Set("ETag", `"mock"`)
			w.Header().Set("Last-Modified", time.Now().UTC().Format(http.TimeFormat))
			for k, v := range obj.meta {
				w.Header().Set("X-Amz-Meta-"+k, v)
			}
			w.Write(obj.data)
		case http.MethodHead:
			store.Lock()
			defer store.Unlock()
			obj, ok := store.buckets[bucket][key]
			if !ok {
				http.Error(w, "NoSuchKey", http.StatusNotFound)
				return
			}
			w.Header().Set("Content-Length", strconv.Itoa(len(obj.data)))
			w.Header().Set("ETag", `"mock"`)
			w.Header().Set("Last-Modified", time.Now().UTC().Format(http.TimeFormat))
			for k, v := range obj.meta {
				w.Header().Set("X-Amz-Meta-"+k, v)
			}
			w.WriteHeader(http.StatusOK)
		case http.MethodDelete:
			store.Lock()
			defer store.Unlock()
			if key == "" {
				delete(store.buckets, bucket)
				w.WriteHeader(http.StatusNoContent)
				return
			}
			delete(store.buckets[bucket], key)
			w.WriteHeader(http.StatusNoContent)
		default:
			http.Error(w, "unsupported", http.StatusMethodNotAllowed)
		}
	}))
	t.Cleanup(server.Close)
	host := strings.TrimPrefix(server.URL, "http://")
	client, err := minio.New(host, &minio.Options{
		Creds:  credentials.NewStaticV4("test", "test", ""),
		Secure: false, BucketLookup: minio.BucketLookupPath, Transport: server.Client().Transport,
	})
	if err != nil {
		t.Fatal(err)
	}
	return client, server.URL
}

func stripAWSChunked(body []byte) []byte {
	if !bytes.Contains(body, []byte("chunk-signature=")) {
		return body
	}
	var out bytes.Buffer
	rest := body
	for len(rest) > 0 {
		lineEnd := bytes.Index(rest, []byte("\r\n"))
		if lineEnd < 0 {
			break
		}
		line := rest[:lineEnd]
		rest = rest[lineEnd+2:]
		semi := bytes.Index(line, []byte(";"))
		if semi < 0 {
			continue
		}
		size, err := strconv.ParseInt(string(line[:semi]), 16, 64)
		if err != nil || size == 0 {
			break
		}
		if len(rest) < int(size)+2 {
			break
		}
		out.Write(rest[:size])
		rest = rest[size+2:]
	}
	if out.Len() == 0 {
		return body
	}
	return out.Bytes()
}

func TestMemoryS3PutObjectFromTempFile(t *testing.T) {
	client, _ := newMemoryS3(t)
	ctx := context.Background()
	const bucket = "file-bucket"
	if err := client.MakeBucket(ctx, bucket, minio.MakeBucketOptions{}); err != nil {
		t.Fatal(err)
	}
	tmp, err := os.CreateTemp("", "nagi-put-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(tmp.Name())
	payload := []byte("temp-file-body")
	if _, err := tmp.Write(payload); err != nil {
		t.Fatal(err)
	}
	if _, err := tmp.Seek(0, io.SeekStart); err != nil {
		t.Fatal(err)
	}
	if _, err := client.PutObject(ctx, bucket, "key", tmp, int64(len(payload)), minio.PutObjectOptions{
		ContentType: "text/plain", UserMetadata: map[string]string{"sha256": "abc"},
	}); err != nil {
		t.Fatal(err)
	}
}

func TestMemoryS3RoundTrip(t *testing.T) {
	client, _ := newMemoryS3(t)
	ctx := context.Background()
	const bucket = "probe-bucket"
	if err := client.MakeBucket(ctx, bucket, minio.MakeBucketOptions{}); err != nil {
		t.Fatal(err)
	}
	payload := []byte("hello s3 mock")
	if _, err := client.PutObject(ctx, bucket, "folder/object", bytes.NewReader(payload), int64(len(payload)), minio.PutObjectOptions{}); err != nil {
		t.Fatal(err)
	}
	if _, err := client.StatObject(ctx, bucket, "folder/object", minio.StatObjectOptions{}); err != nil {
		t.Fatal(err)
	}
	obj, err := client.GetObject(ctx, bucket, "folder/object", minio.GetObjectOptions{})
	if err != nil {
		t.Fatal(err)
	}
	got, err := io.ReadAll(obj)
	obj.Close()
	if err != nil || !bytes.Equal(got, payload) {
		t.Fatalf("payload = %q, err=%v", got, err)
	}
}
