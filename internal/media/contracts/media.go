// Package contracts defines media inputs without framework dependencies.
// Package contracts 定义不依赖模型框架的媒体输入。
package contracts

import (
	"context"
	"errors"

	ob "github.com/HappyLadySauceM/Nagi/internal/onebot/domain"
)

// TranscriptMarker is storage-owned metadata; ingress must strip it from received segments.
// TranscriptMarker 是存储端专有元数据，入站时必须移除外部提供的同名字段。
const TranscriptMarker = "_nagi_transcribed"

type Part struct {
	Text   string
	MIME   string
	Base64 string
}
type MessageReader interface {
	Parts(context.Context, int64, string, []ob.Segment, string) ([]Part, error)
}
type Transcriber interface {
	Transcribe(context.Context, int64, []ob.Segment, string) (string, error)
}
type Speech interface {
	Synthesize(context.Context, string) ([]byte, error)
}

// ArchiveRequest is one already-authorized media value entering the archive.
// EventID is optional for input media not produced by a named step.
// ArchiveRequest 是一个进入归档的已授权媒体值。没有明确步骤产生的入站媒体
// 可以不填写 EventID。
type ArchiveRequest struct {
	Kind        string
	ContentType string
	Data        []byte
	EventID     string
}

// ArchiveReference points at one stored S3 object and its stable s3:// URI.
// The administrator API issues time-bounded GET URLs separately.
// ArchiveReference 指向一个已存储的 S3 对象及其稳定的 s3:// URI。
// 管理员 API 另行签发限时 GET 地址。
type ArchiveReference struct {
	ID     string
	URI    string
	Status string
}

// Archiver uploads media bytes to object storage synchronously and returns the
// stored object reference. It must not expose credentials in its result.
// Archiver 同步把媒体字节上传到对象存储并返回已存储对象引用；返回值不得暴露凭据。
type Archiver interface {
	Archive(context.Context, ArchiveRequest) (ArchiveReference, error)
}

// ErrArchiveFailed marks a media archive failure that must abort the run instead
// of being downgraded to a transcription or generation failure.
// ErrArchiveFailed 标记必须中止运行的媒体归档失败，不能被降级为转写或生成失败。
var ErrArchiveFailed = errors.New("media archive failed")
