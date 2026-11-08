package infrastructure

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/minio/minio-go/v7"

	auditcontracts "github.com/HappyLadySauceM/Nagi/internal/audit/contracts"
	auditdomain "github.com/HappyLadySauceM/Nagi/internal/audit/domain"
	media "github.com/HappyLadySauceM/Nagi/internal/media/contracts"
	"github.com/HappyLadySauceM/Nagi/internal/observability"
)

type archiveQueueFixture struct {
	mu           sync.Mutex
	artifacts    []auditdomain.AIArtifact
	expiredClaim []auditdomain.AIArtifact
	expired      []string
}

func (f *archiveQueueFixture) StartRun(context.Context, auditdomain.AIRun) (auditdomain.AIRun, error) {
	return auditdomain.AIRun{}, nil
}
func (f *archiveQueueFixture) FinishRun(context.Context, string, string, string) error { return nil }
func (f *archiveQueueFixture) RecordEvent(context.Context, auditdomain.AIEvent) (auditdomain.AIEvent, error) {
	return auditdomain.AIEvent{}, nil
}
func (f *archiveQueueFixture) FinishEvent(context.Context, string, string, string, []byte) error {
	return nil
}
func (f *archiveQueueFixture) RecordArtifact(_ context.Context, artifact auditdomain.AIArtifact) (auditdomain.AIArtifact, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if artifact.ID == "" {
		artifact.ID = uuid.NewString()
	}
	if artifact.CreatedAt.IsZero() {
		artifact.CreatedAt = time.Now().UTC()
	}
	f.artifacts = append(f.artifacts, artifact)
	return artifact, nil
}
func (f *archiveQueueFixture) ListRuns(context.Context, auditdomain.AIRunQuery) ([]auditdomain.AIRun, error) {
	return nil, nil
}
func (f *archiveQueueFixture) GetRun(context.Context, string) (auditdomain.AIRun, error) {
	return auditdomain.AIRun{}, auditdomain.ErrNotFound
}
func (f *archiveQueueFixture) GetArtifact(_ context.Context, id string) (auditdomain.AIArtifact, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, artifact := range f.artifacts {
		if artifact.ID == id {
			return artifact, nil
		}
	}
	return auditdomain.AIArtifact{}, auditdomain.ErrNotFound
}
func (f *archiveQueueFixture) FindUploadedArtifactBySHA256(_ context.Context, contentHash string) (auditdomain.AIArtifact, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	now := time.Now()
	for i := len(f.artifacts) - 1; i >= 0; i-- {
		artifact := f.artifacts[i]
		if artifact.SHA256 == contentHash && artifact.Status == "uploaded" &&
			(artifact.ExpiresAt == nil || artifact.ExpiresAt.After(now)) {
			return artifact, nil
		}
	}
	return auditdomain.AIArtifact{}, auditdomain.ErrNotFound
}
func (f *archiveQueueFixture) ClaimExpiredArtifacts(context.Context, int, time.Duration) ([]auditdomain.AIArtifact, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	claimed := append([]auditdomain.AIArtifact(nil), f.expiredClaim...)
	f.expiredClaim = nil
	return claimed, nil
}
func (f *archiveQueueFixture) MarkArtifactExpired(_ context.Context, id, _ string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.expired = append(f.expired, id)
	return nil
}
func (f *archiveQueueFixture) RetryArtifactExpiry(context.Context, string, string, time.Time, string) error {
	return nil
}

type objectStoreFixture struct {
	mu       sync.Mutex
	puts     map[string][]byte
	putCount int
	removed  []string
}

func (f *objectStoreFixture) PutObject(_ context.Context, _ string, key string, reader io.Reader, _ int64, _ minio.PutObjectOptions) (minio.UploadInfo, error) {
	b, err := io.ReadAll(reader)
	if err != nil {
		return minio.UploadInfo{}, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.puts == nil {
		f.puts = map[string][]byte{}
	}
	f.puts[key] = b
	f.putCount++
	return minio.UploadInfo{Key: key, Size: int64(len(b))}, nil
}
func (f *objectStoreFixture) RemoveObject(_ context.Context, _ string, key string, _ minio.RemoveObjectOptions) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.removed = append(f.removed, key)
	return nil
}
func (f *objectStoreFixture) PresignedGetObject(_ context.Context, _ string, key string, _ time.Duration, _ url.Values) (*url.URL, error) {
	return url.Parse("https://media.example/" + key)
}

func enableContentExport(t *testing.T, enabled bool) {
	t.Helper()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	if _, err := observability.Setup(context.Background(), observability.Config{ExportRawContent: enabled}, logger); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = observability.Setup(context.Background(), observability.Config{ExportRawContent: false}, logger)
	})
}

func newTestArchive(queue *archiveQueueFixture, store *objectStoreFixture) *MediaArchive {
	return newMediaArchive(queue, store, store, MediaArchiveOptions{
		Bucket: "nagi-audit", Prefix: "nagi/", Retention: 30 * 24 * time.Hour,
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
}

func TestMediaArchiveUploadsOnceAndReusesRetainedObject(t *testing.T) {
	enableContentExport(t, true)
	queue := &archiveQueueFixture{}
	store := &objectStoreFixture{}
	archive := newTestArchive(queue, store)
	ctx := auditcontracts.WithRun(context.Background(), queue, uuid.NewString())
	request := media.ArchiveRequest{Kind: "image.inbound", ContentType: "image/png", Data: []byte("png bytes")}
	first, err := archive.Archive(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	if first.ID == "" || first.Status != "uploaded" || !strings.HasSuffix(first.URI, ".png") {
		t.Fatalf("first reference = %+v", first)
	}
	second, err := archive.Archive(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	if second.ID == first.ID || second.Status != "uploaded" {
		t.Fatalf("second reference = %+v", second)
	}
	queue.mu.Lock()
	artifacts := append([]auditdomain.AIArtifact(nil), queue.artifacts...)
	queue.mu.Unlock()
	if len(artifacts) != 2 {
		t.Fatalf("artifact rows = %d", len(artifacts))
	}
	if artifacts[0].Bucket != "nagi-audit" || artifacts[0].ObjectKey == "" || artifacts[0].ObjectKey != artifacts[1].ObjectKey {
		t.Fatalf("artifact storage = %+v / %+v", artifacts[0], artifacts[1])
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	if store.putCount != 1 {
		t.Fatalf("put count = %d", store.putCount)
	}
	if !bytes.Equal(store.puts[artifacts[0].ObjectKey], []byte("png bytes")) {
		t.Fatalf("stored bytes = %q", store.puts[artifacts[0].ObjectKey])
	}
}

func TestMediaArchiveConcurrentArchivesUploadOnce(t *testing.T) {
	enableContentExport(t, true)
	queue := &archiveQueueFixture{}
	store := &objectStoreFixture{}
	archive := newTestArchive(queue, store)
	ctx := auditcontracts.WithRun(context.Background(), queue, uuid.NewString())
	request := media.ArchiveRequest{Kind: "image.inbound", ContentType: "image/png", Data: []byte("same png bytes")}
	const workers = 8
	var wg sync.WaitGroup
	references := make([]media.ArchiveReference, workers)
	errors := make([]error, workers)
	for index := 0; index < workers; index++ {
		index := index
		wg.Add(1)
		go func() {
			defer wg.Done()
			references[index], errors[index] = archive.Archive(ctx, request)
		}()
	}
	wg.Wait()
	for index, err := range errors {
		if err != nil {
			t.Fatalf("worker %d: %v", index, err)
		}
		if references[index].Status != "uploaded" {
			t.Fatalf("worker %d reference = %+v", index, references[index])
		}
	}
	queue.mu.Lock()
	artifacts := append([]auditdomain.AIArtifact(nil), queue.artifacts...)
	queue.mu.Unlock()
	if len(artifacts) != workers {
		t.Fatalf("artifact rows = %d", len(artifacts))
	}
	for _, artifact := range artifacts {
		if artifact.ObjectKey != artifacts[0].ObjectKey {
			t.Fatalf("object keys = %q / %q", artifacts[0].ObjectKey, artifact.ObjectKey)
		}
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	if store.putCount != 1 {
		t.Fatalf("put count = %d", store.putCount)
	}
}

func TestMediaArchiveRedactsWithoutUploading(t *testing.T) {
	queue := &archiveQueueFixture{}
	store := &objectStoreFixture{}
	archive := newTestArchive(queue, store)
	ctx := observability.WithContentState(context.Background())
	observability.SuppressContent(ctx)
	ctx = auditcontracts.WithRun(ctx, queue, uuid.NewString())
	ref, err := archive.Archive(ctx, media.ArchiveRequest{Kind: "image.inbound", ContentType: "image/png", Data: []byte("png bytes")})
	if err != nil {
		t.Fatal(err)
	}
	if ref.Status != "redacted" {
		t.Fatalf("status = %q", ref.Status)
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	if store.putCount != 0 {
		t.Fatalf("put count = %d", store.putCount)
	}
}

func TestMediaArchiveCleanupRemovesExpiredObject(t *testing.T) {
	enableContentExport(t, true)
	queue := &archiveQueueFixture{expiredClaim: []auditdomain.AIArtifact{{
		ID: "artifact-1", Bucket: "nagi-audit", ObjectKey: "nagi/artifacts/expired.png", LeaseToken: "lease-1", CleanupAttempts: 0,
	}}}
	store := &objectStoreFixture{}
	archive := newTestArchive(queue, store)
	archive.cleanup(context.Background())
	store.mu.Lock()
	removed := append([]string(nil), store.removed...)
	store.mu.Unlock()
	queue.mu.Lock()
	expired := append([]string(nil), queue.expired...)
	queue.mu.Unlock()
	if len(removed) != 1 || removed[0] != "nagi/artifacts/expired.png" {
		t.Fatalf("removed = %v", removed)
	}
	if len(expired) != 1 || expired[0] != "artifact-1" {
		t.Fatalf("expired = %v", expired)
	}
}

func TestMediaArchiveUploadsWhenGlobalContentExportDisabled(t *testing.T) {
	enableContentExport(t, false)
	queue := &archiveQueueFixture{}
	store := &objectStoreFixture{}
	archive := newTestArchive(queue, store)
	ctx := auditcontracts.WithRun(context.Background(), queue, uuid.NewString())
	ref, err := archive.Archive(ctx, media.ArchiveRequest{Kind: "image.inbound", ContentType: "image/png", Data: []byte("png bytes")})
	if err != nil {
		t.Fatal(err)
	}
	if ref.Status != "uploaded" {
		t.Fatalf("status = %q", ref.Status)
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	if store.putCount != 1 {
		t.Fatalf("put count = %d", store.putCount)
	}
}
