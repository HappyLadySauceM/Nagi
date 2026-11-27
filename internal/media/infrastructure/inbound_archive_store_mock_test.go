package infrastructure

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"log/slog"
	"testing"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
	"github.com/google/uuid"
	"github.com/minio/minio-go/v7"

	scope "github.com/HappyLadySauceM/Nagi/internal/scope/domain"
	"github.com/HappyLadySauceM/Nagi/pkg/db"
)

// TestInboundArchiveStoreTaskScopeRevokedNeedsNoDatabase verifies early exit on scope policy.
// TestInboundArchiveStoreTaskScopeRevokedNeedsNoDatabase 验证作用域撤销时无需数据库即可失败。
func TestInboundArchiveStoreTaskScopeRevokedNeedsNoDatabase(t *testing.T) {
	archive := &InboundArchive{
		scopes: &inboundArchiveAuth{allowed: false},
		opts:   InboundArchiveOptions{MaxBytes: 512},
		logger: slog.Default(),
	}
	meta, code := archive.storeTask(context.Background(), archiveTask{
		ID: "id-1", Token: "lease", Scope: string(scope.ScopeGroup), BotID: 1, TargetID: 2,
		Source: []byte(`{"file":"base64://AQ=="}`),
	})
	if code != "scope_revoked" || meta.hash != "" {
		t.Fatalf("meta=%+v code=%q", meta, code)
	}
}

// TestInboundArchiveStoreTaskBase64UploadsToMemoryS3 exercises S3 + sqlmock without PostgreSQL.
// TestInboundArchiveStoreTaskBase64UploadsToMemoryS3 用 S3 与 sqlmock 验证上传，不连 PostgreSQL。
func TestInboundArchiveStoreTaskBase64UploadsToMemoryS3(t *testing.T) {
	s3, _ := newMemoryS3(t)
	payload := []byte("hello-mock")
	sqlDB, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherRegexp))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	mock.ExpectExec(`UPDATE inbound_attachments SET filename`).
		WithArgs("att-1", "tok-1", sqlmock.AnyArg(), sqlmock.AnyArg(), int64(len(payload)), sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(0, 1))
	source, err := json.Marshal(map[string]string{
		"file": "base64://" + base64.StdEncoding.EncodeToString(payload),
		"name": "probe.png",
	})
	if err != nil {
		t.Fatal(err)
	}
	bucket := "nagi-inbound-mock-" + uuid.NewString()[:8]
	ctx := context.Background()
	if err := s3.MakeBucket(ctx, bucket, minio.MakeBucketOptions{}); err != nil {
		t.Fatal(err)
	}
	auth := &inboundArchiveAuth{allowed: true}
	archive := &InboundArchive{
		pool:   &db.Pool{DB: sqlDB},
		store:  s3,
		media:  NewService(nil, nil, t.TempDir()),
		scopes: auth,
		opts:   InboundArchiveOptions{Bucket: bucket, Prefix: "inbound", MaxBytes: 512, Store: s3},
		logger: slog.Default(),
	}
	meta, code := archive.storeTask(ctx, archiveTask{
		ID: "att-1", Token: "tok-1", Kind: "image", Bucket: bucket, Key: "inbound/att-1",
		Scope: string(scope.ScopeGroup), BotID: 1, TargetID: 2, Attempts: 1, Source: source,
	})
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("sqlmock: %v (storeTask code=%q)", err, code)
	}
	if code != "" {
		t.Fatalf("storeTask code=%q meta=%+v", code, meta)
	}
	if meta.size != int64(len(payload)) || meta.hash == "" {
		t.Fatalf("meta=%+v", meta)
	}
	obj, err := s3.GetObject(ctx, bucket, "inbound/att-1", minio.GetObjectOptions{})
	if err != nil {
		t.Fatal(err)
	}
	got, err := io.ReadAll(obj)
	obj.Close()
	if err != nil || string(got) != string(payload) {
		t.Fatalf("object=%q err=%v", got, err)
	}
}

// TestInboundArchiveSourceInlineBase64 reads embedded base64 payloads without adapters.
// TestInboundArchiveSourceInlineBase64 在无账号适配器时读取 base64 内联载荷。
func TestInboundArchiveSourceInlineBase64(t *testing.T) {
	archive := &InboundArchive{
		media: NewService(nil, nil, t.TempDir()),
		opts:  InboundArchiveOptions{MaxBytes: 4096},
	}
	want := []byte("inline-bytes")
	body, code := archive.source(context.Background(), archiveTask{Kind: "image"}, map[string]string{
		"file": "base64://" + base64.StdEncoding.EncodeToString(want),
	})
	if code != "" || body == nil {
		t.Fatalf("code=%q body=%v", code, body)
	}
	defer body.Close()
	got, err := io.ReadAll(body)
	if err != nil || string(got) != string(want) {
		t.Fatalf("got=%q err=%v", got, err)
	}
}
