package infrastructure

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"

	media "github.com/HappyLadySauceM/Nagi/internal/media/contracts"
	ob "github.com/HappyLadySauceM/Nagi/internal/onebot/domain"
	scopecontracts "github.com/HappyLadySauceM/Nagi/internal/scope/contracts"
	scopedomain "github.com/HappyLadySauceM/Nagi/internal/scope/domain"
	"github.com/HappyLadySauceM/Nagi/pkg/db"
)

type InboundArchiveOptions struct {
	Endpoint, AccessKey, SecretKey, Bucket, Prefix string
	MaxBytes                                       int64
}

type InboundArchive struct {
	pool   *db.Pool
	store  *minio.Client
	media  *Service
	scopes scopecontracts.Authorizer
	opts   InboundArchiveOptions
	logger *slog.Logger
}

func NewInboundArchive(pool *db.Pool, media *Service, scopes scopecontracts.Authorizer, opts InboundArchiveOptions, logger *slog.Logger) (*InboundArchive, error) {
	u, err := url.Parse(opts.Endpoint)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || (u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.Fragment != "" {
		return nil, fmt.Errorf("invalid inbound archive endpoint")
	}
	store, err := minio.New(u.Host, &minio.Options{
		Creds: credentials.NewStaticV4(opts.AccessKey, opts.SecretKey, ""), Secure: u.Scheme == "https",
		BucketLookup: minio.BucketLookupPath,
	})
	if err != nil {
		return nil, err
	}
	return &InboundArchive{pool: pool, store: store, media: media, scopes: scopes, opts: opts, logger: logger}, nil
}

type archiveTask struct {
	ID, Token, Kind, Bucket, Key, Scope string
	BotID, TargetID                     int64
	Attempts                            int
	Source                              []byte
	Metadata                            archiveMetadata
}

func (a *InboundArchive) Run(ctx context.Context) {
	for ctx.Err() == nil {
		delay := time.Second
		if _, err := a.pool.DB.ExecContext(ctx, `DELETE FROM inbound_attachments WHERE id IN (
		    SELECT a.id FROM inbound_attachments a WHERE a.status='expired'
		      AND NOT EXISTS (SELECT 1 FROM messages m WHERE m.id=a.message_id) LIMIT 100
		)`); err != nil && ctx.Err() == nil {
			a.logger.Warn("inbound attachment tombstone cleanup failed")
		}
		if worked, err := a.step(ctx); err != nil {
			if ctx.Err() == nil {
				a.logger.Warn("inbound attachment archive step failed", "error", err)
			}
			delay = 5 * time.Second
		} else if worked {
			continue
		}
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
		case <-timer.C:
		}
		timer.Stop()
	}
}

func (a *InboundArchive) step(ctx context.Context) (bool, error) {
	purged, err := a.purge(ctx)
	if err != nil || purged {
		return purged, err
	}
	task, err := a.claim(ctx)
	if err != nil || task.ID == "" {
		return false, err
	}
	deadline, cancel := context.WithTimeout(ctx, 5*time.Minute)
	meta, code := a.storeTask(deadline, task)
	cancel()
	status := "uploaded"
	retry := 0
	if code != "" {
		status = "unavailable"
		if code == "scope_revoked" {
			status = "purging"
		}
		if code == "download_failed" || code == "upload_failed" || code == "scope_unavailable" {
			status = "failed"
			if task.Attempts < 5 {
				status = "pending"
				retry = (5 << (task.Attempts - 1)) + int(time.Now().UnixNano()%7)
			}
		}
	}
	_, err = a.pool.DB.ExecContext(ctx, `
		UPDATE inbound_attachments SET status=$3::text, failure_code=$4, filename=COALESCE(NULLIF($5,''),filename), content_type=COALESCE(NULLIF($6,''),content_type),
		    size_bytes=CASE WHEN $7::bigint>0 THEN $7 ELSE size_bytes END, sha256=COALESCE(NULLIF($8,''),sha256), source=CASE WHEN $3::text='pending' THEN source ELSE NULL END,
		    next_run_at=now()+make_interval(secs=>$9), lease_token=NULL, lease_expires_at=NULL
		WHERE id=$1 AND lease_token=$2
	`, task.ID, task.Token, status, code, meta.name, meta.contentType, meta.size, meta.hash, retry)
	return true, err
}

func (a *InboundArchive) claim(ctx context.Context) (archiveTask, error) {
	var task archiveTask
	task.Token = uuid.NewString()
	err := a.pool.DB.QueryRowContext(ctx, `
		WITH candidate AS (
		    SELECT a.id, c.bot_id, c.kind, c.target_id
		    FROM inbound_attachments a
		    JOIN messages m ON m.id=a.message_id
		    JOIN conversations c ON c.id=m.conversation_id
		    WHERE a.next_run_at<=now() AND a.expires_at>now()
		      AND (a.status='pending' OR (a.status='processing' AND a.lease_expires_at<=now()))
		    ORDER BY a.next_run_at,a.id
		    LIMIT 1 FOR UPDATE OF a SKIP LOCKED
		)
		UPDATE inbound_attachments a SET status='processing', attempts=a.attempts+1,
		    lease_token=$1, lease_expires_at=now()+interval '6 minutes',
		    bucket=COALESCE(NULLIF(a.bucket,''),$2),
		    object_key=COALESCE(NULLIF(a.object_key,''),$3||a.id::text)
		FROM candidate c WHERE a.id=c.id
		RETURNING a.id::text,a.kind,a.bucket,a.object_key,c.kind,c.bot_id,c.target_id,a.attempts,a.source,a.filename,a.content_type,a.size_bytes,a.sha256
	`, task.Token, a.opts.Bucket, path.Join(a.opts.Prefix, "inbound")+"/").Scan(
		&task.ID, &task.Kind, &task.Bucket, &task.Key, &task.Scope, &task.BotID, &task.TargetID, &task.Attempts, &task.Source,
		&task.Metadata.name, &task.Metadata.contentType, &task.Metadata.size, &task.Metadata.hash)
	if errors.Is(err, sql.ErrNoRows) {
		return archiveTask{}, nil
	}
	return task, err
}

func (a *InboundArchive) purge(ctx context.Context) (bool, error) {
	var id, bucket, key string
	token := uuid.NewString()
	err := a.pool.DB.QueryRowContext(ctx, `
		WITH candidate AS (
		    SELECT a.id FROM inbound_attachments a
		    WHERE (a.lease_expires_at IS NULL OR a.lease_expires_at<=now())
		      AND a.next_run_at<=now() AND a.status<>'expired'
		      AND (a.expires_at<=now() OR a.status='purging' OR NOT EXISTS (
		          SELECT 1 FROM messages m WHERE m.id=a.message_id
		      ))
		    ORDER BY a.expires_at,a.id LIMIT 1 FOR UPDATE OF a SKIP LOCKED
		)
		UPDATE inbound_attachments a SET status='purging', lease_token=$1,
		    lease_expires_at=now()+interval '6 minutes', source=NULL
		FROM candidate c WHERE c.id=a.id
		RETURNING a.id::text,a.bucket,a.object_key
	`, token).Scan(&id, &bucket, &key)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if key != "" {
		removeCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		err = a.store.RemoveObject(removeCtx, bucket, key, minio.RemoveObjectOptions{})
		cancel()
		if err != nil {
			_, updateErr := a.pool.DB.ExecContext(ctx, `UPDATE inbound_attachments SET
			    next_run_at=now()+interval '1 minute', lease_token=NULL, lease_expires_at=NULL
			    WHERE id=$1 AND lease_token=$2`, id, token)
			return true, errors.Join(err, updateErr)
		}
	}
	_, err = a.pool.DB.ExecContext(ctx, `
		UPDATE inbound_attachments SET status='expired', object_key='', bucket='',
		    lease_token=NULL, lease_expires_at=NULL WHERE id=$1 AND lease_token=$2
	`, id, token)
	return true, err
}

type archiveMetadata struct {
	name, contentType, hash string
	size                    int64
}

func (a *InboundArchive) allowed(ctx context.Context, task archiveTask) (bool, error) {
	policy, err := a.scopes.Resolve(ctx, task.BotID, scopedomain.ScopeType(task.Scope), task.TargetID)
	return policy.Enabled && policy.StoreMessages, err
}

func (a *InboundArchive) storeTask(ctx context.Context, task archiveTask) (archiveMetadata, string) {
	var meta archiveMetadata
	allowed, err := a.allowed(ctx, task)
	if err != nil {
		return meta, "scope_unavailable"
	}
	if !allowed {
		return meta, "scope_revoked"
	}
	if task.Metadata.hash != "" {
		object, err := a.store.StatObject(ctx, task.Bucket, task.Key, minio.StatObjectOptions{})
		if err == nil && object.Size == task.Metadata.size && object.Metadata.Get("X-Amz-Meta-Sha256") == task.Metadata.hash {
			return task.Metadata, ""
		}
		if err != nil {
			code := minio.ToErrorResponse(err).Code
			if code != "NoSuchKey" && code != "NotFound" {
				return meta, "upload_failed"
			}
		}
	}
	var source map[string]string
	if err := json.Unmarshal(task.Source, &source); err != nil || source == nil {
		return meta, "no_download_source"
	}
	filename := source["name"]
	if filename == "" {
		filename = source["file_name"]
	}
	meta.name = path.Base(strings.ReplaceAll(filename, "\\", "/"))
	if meta.name == "." || meta.name == "/" {
		meta.name = ""
	}
	if size, _ := strconv.ParseInt(source["file_size"], 10, 64); size > a.opts.MaxBytes {
		return meta, "too_large"
	}
	body, code := a.source(ctx, task, source)
	if code != "" {
		return meta, code
	}
	defer body.Close()
	tmp, err := os.CreateTemp("", "nagi-inbound-*")
	if err != nil {
		return meta, "upload_failed"
	}
	defer os.Remove(tmp.Name())
	defer tmp.Close()
	sum := sha256.New()
	meta.size, err = io.Copy(io.MultiWriter(tmp, sum), io.LimitReader(body, a.opts.MaxBytes+1))
	if err != nil {
		return meta, "download_failed"
	}
	if meta.size > a.opts.MaxBytes {
		return meta, "too_large"
	}
	if meta.size == 0 {
		return meta, "source_empty"
	}
	head := make([]byte, 512)
	n, _ := tmp.ReadAt(head, 0)
	meta.contentType = http.DetectContentType(head[:n])
	meta.hash = hex.EncodeToString(sum.Sum(nil))
	if _, err := tmp.Seek(0, io.SeekStart); err != nil {
		return meta, "upload_failed"
	}
	allowed, err = a.allowed(ctx, task)
	if err != nil {
		return meta, "scope_unavailable"
	}
	if !allowed {
		return meta, "scope_revoked"
	}
	result, err := a.pool.DB.ExecContext(ctx, `UPDATE inbound_attachments SET filename=$3, content_type=$4, size_bytes=$5, sha256=$6
	    WHERE id=$1 AND lease_token=$2 AND lease_expires_at>now()`,
		task.ID, task.Token, meta.name, meta.contentType, meta.size, meta.hash)
	if err != nil {
		return meta, "upload_failed"
	}
	affected, err := result.RowsAffected()
	if err != nil || affected != 1 {
		return meta, "upload_failed"
	}
	if _, err := a.store.PutObject(ctx, task.Bucket, task.Key, tmp, meta.size, minio.PutObjectOptions{
		ContentType: meta.contentType, UserMetadata: map[string]string{"sha256": meta.hash},
	}); err != nil {
		return meta, "upload_failed"
	}
	allowed, err = a.allowed(ctx, task)
	if err != nil {
		return meta, "scope_unavailable"
	}
	if !allowed {
		return meta, "scope_revoked"
	}
	return meta, ""
}

func (a *InboundArchive) source(ctx context.Context, task archiveTask, source map[string]string) (io.ReadCloser, string) {
	raw, file := source["url"], source["file"]
	if strings.HasPrefix(file, "base64://") {
		return inlineMedia(strings.TrimPrefix(file, "base64://")), ""
	}
	if raw == "" && (strings.HasPrefix(file, "https://") || strings.HasPrefix(file, "http://")) {
		raw, file = file, ""
	}
	body, code := a.download(ctx, raw)
	if code == "" || code == "too_large" {
		return body, code
	}
	id := source["file_id"]
	if id == "" {
		id = file
	}
	if id == "" || (task.Kind != "image" && task.Kind != "record" && task.Kind != "file") {
		return nil, code
	}
	if a.media.Accounts == nil {
		return nil, "download_failed"
	}
	reader, ok := a.media.Accounts.MediaReader(task.BotID)
	if !ok {
		return nil, "download_failed"
	}
	if task.Kind == "file" {
		files, supported := reader.(interface {
			GetAttachmentFileURL(context.Context, bool, int64, string, string, string) (string, error)
		})
		if !supported {
			return nil, code
		}
		resolved, err := files.GetAttachmentFileURL(ctx, task.Scope == "group", task.TargetID, id, source["busid"], source["file_hash"])
		if err != nil {
			return nil, "download_failed"
		}
		if resolved == "" {
			return nil, code
		}
		return a.download(ctx, resolved)
	}
	var resolved ob.FileResult
	var err error
	if task.Kind == "image" {
		resolved, err = reader.GetImage(ctx, id)
	} else {
		original, ok := reader.(interface {
			GetOriginalRecord(context.Context, string) (ob.FileResult, error)
		})
		if !ok {
			return nil, code
		}
		resolved, err = original.GetOriginalRecord(ctx, id)
	}
	if err != nil {
		return nil, "download_failed"
	}
	if resolved.Base64 != "" {
		return inlineMedia(resolved.Base64), ""
	}
	if resolved.URL != "" || strings.HasPrefix(resolved.File, "https://") || strings.HasPrefix(resolved.File, "http://") {
		link := resolved.URL
		if link == "" {
			link = resolved.File
		}
		return a.download(ctx, link)
	}
	return nil, code
}

func inlineMedia(text string) io.ReadCloser {
	return io.NopCloser(base64.NewDecoder(base64.StdEncoding, strings.NewReader(text)))
}

func (a *InboundArchive) download(ctx context.Context, raw string) (io.ReadCloser, string) {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || u.User != nil || (u.Scheme != "https" && u.Scheme != "http") {
		return nil, "no_download_source"
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, raw, nil)
	if err != nil {
		return nil, "no_download_source"
	}
	response, err := a.media.HTTP.Do(request)
	if err != nil {
		return nil, "download_failed"
	}
	if response.StatusCode == http.StatusOK && response.ContentLength <= a.opts.MaxBytes {
		return response.Body, ""
	}
	response.Body.Close()
	if response.StatusCode == http.StatusOK {
		return nil, "too_large"
	}
	if response.StatusCode == http.StatusForbidden || response.StatusCode == http.StatusNotFound || response.StatusCode == http.StatusGone {
		return nil, "source_expired"
	}
	return nil, "download_failed"
}

func (a *InboundArchive) authorizeMessage(ctx context.Context, botID, messageID int64) error {
	var task archiveTask
	task.BotID = botID
	err := a.pool.DB.QueryRowContext(ctx, `
		SELECT c.kind, c.target_id FROM messages m JOIN conversations c ON c.id=m.conversation_id
		WHERE m.bot_id=$1 AND m.id=$2 AND m.direction='incoming'
	`, botID, messageID).Scan(&task.Scope, &task.TargetID)
	if errors.Is(err, sql.ErrNoRows) {
		return media.ErrAttachmentNotFound
	}
	if err != nil {
		return err
	}
	allowed, err := a.allowed(ctx, task)
	if err == nil && !allowed {
		return media.ErrAttachmentNotFound
	}
	return err
}

func (a *InboundArchive) List(ctx context.Context, botID, messageID int64) ([]media.InboundAttachment, error) {
	if err := a.authorizeMessage(ctx, botID, messageID); err != nil {
		return nil, err
	}
	rows, err := a.pool.DB.QueryContext(ctx, `
		SELECT id::text, kind, CASE WHEN expires_at<=now() THEN 'expired' ELSE status END,
		       failure_code, filename, content_type, sha256, segment_index, size_bytes, expires_at
		FROM inbound_attachments WHERE message_id=$1 ORDER BY segment_index
	`, messageID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]media.InboundAttachment, 0)
	for rows.Next() {
		var item media.InboundAttachment
		if err := rows.Scan(&item.ID, &item.Kind, &item.Status, &item.FailureCode, &item.Filename, &item.ContentType,
			&item.SHA256, &item.SegmentIndex, &item.SizeBytes, &item.ExpiresAt); err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

func (a *InboundArchive) Open(ctx context.Context, botID, messageID int64, id string) (io.ReadCloser, media.InboundAttachment, error) {
	var item media.InboundAttachment
	if err := a.authorizeMessage(ctx, botID, messageID); err != nil {
		return nil, item, err
	}
	var bucket, key string
	err := a.pool.DB.QueryRowContext(ctx, `
		SELECT filename, content_type, size_bytes, bucket, object_key FROM inbound_attachments
		WHERE id=$1 AND message_id=$2 AND status='uploaded' AND expires_at>now()
	`, id, messageID).Scan(&item.Filename, &item.ContentType, &item.SizeBytes, &bucket, &key)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, item, media.ErrAttachmentNotFound
	}
	if err != nil {
		return nil, item, err
	}
	object, err := a.store.GetObject(ctx, bucket, key, minio.GetObjectOptions{})
	if err != nil {
		return nil, item, err
	}
	if _, err := object.Stat(); err != nil {
		object.Close()
		if minio.ToErrorResponse(err).Code == "NoSuchKey" {
			return nil, item, media.ErrAttachmentNotFound
		}
		return nil, item, err
	}
	if err := a.authorizeMessage(ctx, botID, messageID); err != nil {
		object.Close()
		return nil, item, err
	}
	return object, item, nil
}
