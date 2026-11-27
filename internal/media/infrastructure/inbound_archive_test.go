//go:build integration

package infrastructure

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/minio/minio-go/v7"

	conversationdomain "github.com/HappyLadySauceM/Nagi/internal/conversation/domain"
	conversationinfra "github.com/HappyLadySauceM/Nagi/internal/conversation/infrastructure"
	media "github.com/HappyLadySauceM/Nagi/internal/media/contracts"
	ob "github.com/HappyLadySauceM/Nagi/internal/onebot/domain"
	"github.com/HappyLadySauceM/Nagi/pkg/db/dbtest"
)

func TestInboundArchiveStoresOnceAndRevokesAccess(t *testing.T) {
	s3, endpoint := newMemoryS3(t)
	pool := dbtest.Open(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	auth := &inboundArchiveAuth{allowed: true}
	bucket := "nagi-inbound-test-" + uuid.NewString()[:8]
	archive, err := NewInboundArchive(pool, NewService(nil, nil, t.TempDir()), auth, InboundArchiveOptions{
		Endpoint: endpoint, AccessKey: "test", SecretKey: "test",
		Bucket: bucket, MaxBytes: 512, Store: s3,
	}, slog.Default())
	if err != nil {
		t.Fatal(err)
	}
	if err := archive.store.MakeBucket(ctx, bucket, minio.MakeBucketOptions{}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = archive.store.RemoveBucket(context.Background(), bucket) })
	repo, err := conversationinfra.NewRepo(pool, conversationinfra.WithInboundAttachments(30*24*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	payload := []byte("file from the receiving adapter")
	in := conversationdomain.IngestInput{
		BotID: 1996722, TargetID: time.Now().UnixNano() % 1e9, SenderID: 2902,
		Kind: conversationdomain.KindGroup, PlatformMessageID: 1001,
		Segments: []ob.Segment{{Type: ob.SegImage, Data: map[string]string{"file": "base64://" + base64.StdEncoding.EncodeToString(payload)}}},
	}
	message, err := repo.Ingest(ctx, in)
	if err != nil {
		t.Fatal(err)
	}
	duplicate, err := repo.Ingest(ctx, in)
	if err != nil || !duplicate.Duplicate {
		t.Fatalf("duplicate = %+v, error=%v", duplicate, err)
	}
	task, err := archive.claim(ctx)
	if err != nil || task.ID == "" {
		t.Fatalf("claim = %+v, error=%v", task, err)
	}
	if _, code := archive.storeTask(ctx, task); code != "" {
		t.Fatalf("upload: %s", code)
	}
	// The worker died after S3 accepted the upload. The original URL is gone.
	if _, err := pool.DB.ExecContext(ctx, "UPDATE inbound_attachments SET lease_expires_at=now(), source='{}'::jsonb WHERE id=$1", task.ID); err != nil {
		t.Fatal(err)
	}
	if worked, err := archive.step(ctx); err != nil || !worked {
		t.Fatalf("recover upload = %v, error=%v", worked, err)
	}
	items, err := archive.List(ctx, in.BotID, message.MessageID)
	if err != nil || len(items) != 1 || items[0].Status != "uploaded" || items[0].SizeBytes != int64(len(payload)) || items[0].SHA256 == "" {
		t.Fatalf("items = %+v, error=%v", items, err)
	}
	item := items[0]
	if err := repo.BackfillInboundAttachments(ctx, in.BotID, message.MessageID); err != nil {
		t.Fatal(err)
	}
	items, err = archive.List(ctx, in.BotID, message.MessageID)
	if err != nil || len(items) != 1 || items[0].ID != item.ID || items[0].Status != "uploaded" {
		t.Fatalf("backfill after upload = %+v, error=%v", items, err)
	}
	object, _, err := archive.Open(ctx, in.BotID, message.MessageID, item.ID)
	if err != nil {
		t.Fatal(err)
	}
	data, err := io.ReadAll(object)
	object.Close()
	if err != nil || !bytes.Equal(data, payload) {
		t.Fatalf("archived bytes=%q, error=%v", data, err)
	}
	auth.allowed = false
	if _, _, err := archive.Open(ctx, in.BotID, message.MessageID, item.ID); !errors.Is(err, media.ErrAttachmentNotFound) {
		t.Fatalf("revoked access: %v", err)
	}
	if _, err := pool.DB.ExecContext(ctx, "UPDATE inbound_attachments SET expires_at=now() WHERE id=$1", item.ID); err != nil {
		t.Fatal(err)
	}
	if worked, err := archive.step(ctx); err != nil || !worked {
		t.Fatalf("cleanup step = %v, error=%v", worked, err)
	}
	auth.allowed = true
	items, err = archive.List(ctx, in.BotID, message.MessageID)
	if err != nil || len(items) != 1 || items[0].Status != "expired" {
		t.Fatalf("expired items = %+v, error=%v", items, err)
	}
	if _, err := archive.store.StatObject(ctx, bucket, "inbound/"+item.ID, minio.StatObjectOptions{}); err == nil {
		t.Fatal("expired object still exists")
	}
}
