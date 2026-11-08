package infrastructure

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime"
	"net/url"
	"path"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"

	auditcontracts "github.com/HappyLadySauceM/Nagi/internal/audit/contracts"
	auditdomain "github.com/HappyLadySauceM/Nagi/internal/audit/domain"
	media "github.com/HappyLadySauceM/Nagi/internal/media/contracts"
	"github.com/HappyLadySauceM/Nagi/internal/observability"
)

const (
	cleanupBatchSize = 200
	cleanupLease     = 2 * time.Minute
)

// MediaArchiveOptions configures the S3-compatible audit-media archive.
// MediaArchiveOptions 配置兼容 S3 的审计媒体归档。
type MediaArchiveOptions struct {
	Endpoint        string
	PublicEndpoint  string
	AccessKey       string
	SecretKey       string
	Bucket          string
	Prefix          string
	Retention       time.Duration
	CleanupInterval time.Duration
	Logger          *slog.Logger
}

type objectStore interface {
	PutObject(context.Context, string, string, io.Reader, int64, minio.PutObjectOptions) (minio.UploadInfo, error)
	RemoveObject(context.Context, string, string, minio.RemoveObjectOptions) error
	PresignedGetObject(context.Context, string, string, time.Duration, url.Values) (*url.URL, error)
}

// MediaArchive uploads authorized media to S3-compatible storage and records
// the stable object reference in the audit database. Identical content is
// content-addressed, so concurrent or repeated archives share one stored object.
// MediaArchive 将已授权媒体上传到兼容 S3 的存储，并在审计数据库中记录稳定的对象引用。
// 相同内容使用内容寻址键，并发或重复归档共享同一存储对象。
type MediaArchive struct {
	queue           auditcontracts.ArtifactQueue
	store           objectStore
	publicStore     objectStore
	bucket          string
	prefix          string
	retention       time.Duration
	cleanupInterval time.Duration
	logger          *slog.Logger
	hashMu          sync.Mutex
	hashLocks       map[string]*contentHashLock
}

// NewMediaArchive builds an archive backed by an S3-compatible endpoint.
// NewMediaArchive 构建由兼容 S3 端点支撑的归档。
func NewMediaArchive(queue auditcontracts.ArtifactQueue, opts MediaArchiveOptions) (*MediaArchive, error) {
	if queue == nil || opts.Logger == nil || opts.Retention <= 0 || strings.TrimSpace(opts.Bucket) == "" || strings.TrimSpace(opts.PublicEndpoint) == "" {
		return nil, fmt.Errorf("ai audit media: queue, logger, bucket, public endpoint and retention required")
	}
	store, err := newObjectStore(opts.Endpoint, opts.AccessKey, opts.SecretKey)
	if err != nil {
		return nil, fmt.Errorf("ai audit media: invalid endpoint")
	}
	publicStore, err := newObjectStore(opts.PublicEndpoint, opts.AccessKey, opts.SecretKey)
	if err != nil {
		return nil, fmt.Errorf("ai audit media: invalid public endpoint")
	}
	return newMediaArchive(queue, store, publicStore, opts), nil
}

func newObjectStore(rawEndpoint, accessKey, secretKey string) (objectStore, error) {
	u, err := url.Parse(strings.TrimSpace(rawEndpoint))
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return nil, fmt.Errorf("invalid S3 endpoint")
	}
	client, err := minio.New(u.Host, &minio.Options{
		Creds:  credentials.NewStaticV4(accessKey, secretKey, ""),
		Secure: u.Scheme == "https",
	})
	if err != nil {
		return nil, err
	}
	return client, nil
}

func newMediaArchive(queue auditcontracts.ArtifactQueue, store, publicStore objectStore, opts MediaArchiveOptions) *MediaArchive {
	if opts.CleanupInterval <= 0 {
		opts.CleanupInterval = time.Hour
	}
	return &MediaArchive{
		queue: queue, store: store, publicStore: publicStore, bucket: opts.Bucket,
		prefix: strings.Trim(strings.TrimSpace(opts.Prefix), "/"), retention: opts.Retention,
		cleanupInterval: opts.CleanupInterval, logger: opts.Logger, hashLocks: map[string]*contentHashLock{},
	}
}

// Archive uploads media bytes and records the returned object reference. A run
// that suppressed content export stores only a redacted fact.
// Archive 上传媒体字节并记录返回的对象引用。运行级正文抑制时只记录脱敏事实。
func (a *MediaArchive) Archive(ctx context.Context, request media.ArchiveRequest) (media.ArchiveReference, error) {
	if a == nil || len(request.Data) == 0 || strings.TrimSpace(request.Kind) == "" || strings.TrimSpace(request.ContentType) == "" {
		return media.ArchiveReference{}, nil
	}
	if auditcontracts.RunID(ctx) == "" {
		return media.ArchiveReference{}, nil
	}
	sum := sha256.Sum256(request.Data)
	contentHash := fmt.Sprintf("%x", sum)
	artifact := auditdomain.AIArtifact{
		ID: uuid.NewString(), EventID: request.EventID, Kind: request.Kind,
		SHA256: contentHash, ContentType: request.ContentType, SizeBytes: int64(len(request.Data)),
	}
	if observability.ContentSuppressed(ctx) {
		artifact.Status, artifact.FailureCode = "redacted", "content_export_disabled"
		recorded, err := auditcontracts.RecordArtifact(ctx, artifact)
		if err != nil {
			return media.ArchiveReference{}, err
		}
		return media.ArchiveReference{ID: recorded.ID, Status: recorded.Status}, nil
	}
	unlock := a.lockContentHash(contentHash)
	defer unlock()
	expiresAt := time.Now().UTC().Add(a.retention)
	reusable, findErr := a.queue.FindUploadedArtifactBySHA256(ctx, contentHash)
	var uploadedNow bool
	var uploadedBucket, uploadedKey string
	switch {
	case findErr == nil:
		// The same bytes are already retained; add only a per-run reference.
		// 相同字节仍在保留期内，仅追加一条按运行归属的引用。
		artifact.Bucket, artifact.ObjectKey, artifact.Status = reusable.Bucket, reusable.ObjectKey, "uploaded"
		if artifact.Bucket == "" {
			artifact.Bucket = a.bucket
		}
		artifact.ExpiresAt = &expiresAt
	case errors.Is(findErr, auditdomain.ErrNotFound):
		objectKey := a.objectKey(contentHash, request.ContentType)
		if _, err := a.store.PutObject(ctx, a.bucket, objectKey, bytes.NewReader(request.Data), int64(len(request.Data)),
			minio.PutObjectOptions{ContentType: request.ContentType}); err != nil {
			return media.ArchiveReference{}, fmt.Errorf("ai audit media: upload object: %w", err)
		}
		uploadedNow, uploadedBucket, uploadedKey = true, a.bucket, objectKey
		artifact.Bucket, artifact.ObjectKey, artifact.Status, artifact.ExpiresAt = a.bucket, objectKey, "uploaded", &expiresAt
	default:
		return media.ArchiveReference{}, fmt.Errorf("ai audit media: find reusable artifact: %w", findErr)
	}
	recorded, err := auditcontracts.RecordArtifact(ctx, artifact)
	if err != nil {
		if uploadedNow {
			// Delete only when the lookup proves that no retained row owns this object.
			// 仅在查询确认没有保留记录引用该对象时才做补偿删除。
			existing, lookupErr := a.queue.FindUploadedArtifactBySHA256(ctx, contentHash)
			switch {
			case errors.Is(lookupErr, auditdomain.ErrNotFound) || (lookupErr == nil && existing.ObjectKey == ""):
				if removeErr := a.store.RemoveObject(ctx, uploadedBucket, uploadedKey, minio.RemoveObjectOptions{}); removeErr != nil {
					a.logger.Warn("rollback orphan audit media failed", "object_key", uploadedKey, "error", removeErr)
				}
			case lookupErr != nil:
				a.logger.Warn("rollback orphan audit media skipped: reference lookup failed", "object_key", uploadedKey, "error", lookupErr)
			}
		}
		return media.ArchiveReference{}, err
	}
	return media.ArchiveReference{ID: recorded.ID, URI: a.uri(recorded.Bucket, recorded.ObjectKey), Status: recorded.Status}, nil
}

// Run drains retention cleanup until context cancellation.
// Run 持续执行保留期清理，直至上下文取消。
func (a *MediaArchive) Run(ctx context.Context) {
	a.cleanup(ctx)
	cleanupTicker := time.NewTicker(a.cleanupInterval)
	defer cleanupTicker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-cleanupTicker.C:
			a.cleanup(ctx)
		}
	}
}

// Presign returns a short-lived operator download URL only for live objects.
// Presign 仅为仍在保留期内的对象返回短时管理员下载地址。
func (a *MediaArchive) Presign(ctx context.Context, artifact auditdomain.AIArtifact) (string, error) {
	if a == nil || artifact.Status != "uploaded" || artifact.ObjectKey == "" ||
		(artifact.ExpiresAt != nil && !artifact.ExpiresAt.After(time.Now())) {
		return "", nil
	}
	bucket := artifact.Bucket
	if bucket == "" {
		bucket = a.bucket
	}
	u, err := a.publicStore.PresignedGetObject(ctx, bucket, artifact.ObjectKey, 5*time.Minute, nil)
	if err != nil {
		return "", fmt.Errorf("ai audit media: sign artifact: %w", err)
	}
	return u.String(), nil
}

func (a *MediaArchive) cleanup(ctx context.Context) {
	for {
		if ctx.Err() != nil {
			return
		}
		artifacts, err := a.queue.ClaimExpiredArtifacts(ctx, cleanupBatchSize, cleanupLease)
		if err != nil {
			if ctx.Err() == nil {
				a.logger.Warn("claim expired audit media failed", "error", err)
			}
			return
		}
		if len(artifacts) == 0 {
			return
		}
		for _, artifact := range artifacts {
			unlock := a.lockContentHash(artifact.SHA256)
			bucket := artifact.Bucket
			if bucket == "" {
				bucket = a.bucket
			}
			// Re-check retained references under the same per-content lock used by
			// Archive, so a concurrent re-upload cannot be deleted by this cleanup.
			// 在同内容锁下重新检查保留引用，避免并发重新上传的对象被本次清理删除。
			live, findErr := a.queue.FindUploadedArtifactBySHA256(ctx, artifact.SHA256)
			if findErr != nil && !errors.Is(findErr, auditdomain.ErrNotFound) {
				retryErr := a.queue.RetryArtifactExpiry(ctx, artifact.ID, artifact.LeaseToken, time.Now().UTC().Add(retryDelay(artifact.CleanupAttempts+1)), "reference_lookup_failed")
				unlock()
				if retryErr != nil && ctx.Err() == nil {
					a.logger.Error("retry audit media cleanup failed", "artifact_id", artifact.ID, "error", retryErr)
				}
				continue
			}
			if findErr == nil && live.ObjectKey == artifact.ObjectKey && sameBucket(live.Bucket, artifact.Bucket) {
				// A retained row still owns this object; retire only this reference.
				// 仍有保留记录引用该对象，仅将该引用标记为过期。
				if markErr := a.queue.MarkArtifactExpired(ctx, artifact.ID, artifact.LeaseToken); markErr != nil && ctx.Err() == nil {
					a.logger.Error("retire shared audit media reference failed", "artifact_id", artifact.ID, "error", markErr)
				}
				unlock()
				continue
			}
			err := a.store.RemoveObject(ctx, bucket, artifact.ObjectKey, minio.RemoveObjectOptions{})
			if err == nil {
				err = a.queue.MarkArtifactExpired(ctx, artifact.ID, artifact.LeaseToken)
			} else {
				err = a.queue.RetryArtifactExpiry(ctx, artifact.ID, artifact.LeaseToken, time.Now().UTC().Add(retryDelay(artifact.CleanupAttempts+1)), "object_delete_failed")
			}
			unlock()
			if err != nil && ctx.Err() == nil {
				a.logger.Error("expire audit media failed", "artifact_id", artifact.ID, "error", err)
			}
		}
		if len(artifacts) < cleanupBatchSize {
			return
		}
	}
}

func (a *MediaArchive) objectKey(contentHash, contentType string) string {
	ext := map[string]string{
		"image/jpeg": ".jpg",
		"audio/mpeg": ".mp3",
		"audio/mp3":  ".mp3",
	}[contentType]
	if ext == "" {
		ext = ".bin"
		if extensions, err := mime.ExtensionsByType(contentType); err == nil && len(extensions) > 0 {
			ext = extensions[0]
		}
	}
	return path.Join(a.prefix, "artifacts", contentHash+ext)
}

func (a *MediaArchive) uri(bucket, key string) string {
	if bucket == "" {
		bucket = a.bucket
	}
	return "s3://" + bucket + "/" + key
}

type contentHashLock struct {
	mu   sync.Mutex
	refs int
}

func (a *MediaArchive) lockContentHash(contentHash string) func() {
	a.hashMu.Lock()
	lock := a.hashLocks[contentHash]
	if lock == nil {
		lock = &contentHashLock{}
		a.hashLocks[contentHash] = lock
	}
	lock.refs++
	a.hashMu.Unlock()
	lock.mu.Lock()
	return func() {
		lock.mu.Unlock()
		a.hashMu.Lock()
		lock.refs--
		if lock.refs == 0 {
			delete(a.hashLocks, contentHash)
		}
		a.hashMu.Unlock()
	}
}

func sameBucket(left, right string) bool {
	return left == right || left == "" || right == ""
}

func retryDelay(attempt int) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	delay := time.Second << min(attempt-1, 8)
	if delay > 5*time.Minute {
		return 5 * time.Minute
	}
	return delay
}

var _ media.Archiver = (*MediaArchive)(nil)
