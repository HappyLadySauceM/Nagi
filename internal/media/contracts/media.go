// Package contracts defines media inputs without framework dependencies.
// Package contracts 定义不依赖模型框架的媒体输入。
package contracts

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"time"

	ob "github.com/HappyLadySauceM/Nagi/internal/onebot/domain"
)

// TranscriptMarker is storage-owned metadata; ingress must strip it from received segments.
// TranscriptMarker 是存储端专有元数据，入站时必须移除外部提供的同名字段。
const TranscriptMarker = "_nagi_transcribed"

type Part struct {
	PictureSegment *int `json:"-"`
	SourceSegment  *int `json:"-"`
	Text           string
	MIME           string
	Base64         string
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

type InboundAttachment struct {
	ID, Kind, Status, FailureCode, Filename, ContentType, SHA256 string
	SegmentIndex                                                 int
	SizeBytes                                                    int64
	ExpiresAt                                                    time.Time
}

type InboundArchiveReader interface {
	List(context.Context, int64, int64) ([]InboundAttachment, error)
	Open(context.Context, int64, int64, string) (io.ReadCloser, InboundAttachment, error)
}

var ErrAttachmentNotFound = errors.New("inbound attachment not found")

// PictureSummary describes the attachment state of each original image segment.
// PictureSummary 记录每个原始图片段的附件状态。
type PictureSummary struct {
	Version    int
	ImageCount int
	Images     []PictureState
}

type PictureState struct {
	SegmentIndex int
	Status       string
}

const (
	PictureUnknown      = "unknown"
	PictureArchiveOff   = "archive_disabled"
	PictureUnregistered = "unregistered"
	PicturePending      = "pending"
	PictureUnavailable  = "unavailable"
	PictureNoSource     = "no_download_source"
	PictureUploaded     = "uploaded"
	PictureExpired      = "expired"
)

// InboundPictures captures configuration and registration evidence at ingestion.
// InboundPictures 在入站时记录归档配置与注册证据。
func InboundPictures(segments []ob.Segment, archiveEnabled bool) PictureSummary {
	summary := PictureSummary{Version: 1, Images: []PictureState{}}
	for index, segment := range segments {
		if segment.Type != ob.SegImage {
			continue
		}
		status := PicturePending
		if !archiveEnabled {
			status = PictureArchiveOff
		} else if !HasDownloadSource(segment) {
			status = PictureNoSource
		}
		summary.Images = append(summary.Images, PictureState{SegmentIndex: index, Status: status})
	}
	summary.ImageCount = len(summary.Images)
	return summary
}

func HasDownloadSource(segment ob.Segment) bool {
	return segment.Data["url"] != "" || segment.Data["file"] != "" || segment.Data["file_id"] != ""
}

// CurrentPictures projects only recognized fields and matches rows by segment index.
// CurrentPictures 仅投影已知字段，按段序号匹配附件行。
func CurrentPictures(types []string, inbound *PictureSummary, rows []InboundAttachment) PictureSummary {
	current := PictureSummary{Version: 1, Images: []PictureState{}}
	for index, kind := range types {
		if kind != string(ob.SegImage) {
			continue
		}
		status := PictureUnknown
		if inbound != nil && inbound.Version == 1 {
			for _, image := range inbound.Images {
				if image.SegmentIndex != index {
					continue
				}
				status = PictureUnregistered
				if image.Status == PictureArchiveOff {
					status = PictureArchiveOff
				}
				break
			}
		}
		for _, row := range rows {
			if row.SegmentIndex != index || row.Kind != string(ob.SegImage) {
				continue
			}
			switch row.Status {
			case "processing", PicturePending:
				status = PicturePending
			case PictureUploaded, PictureExpired:
				status = row.Status
			case PictureUnavailable, "failed":
				status = PictureUnavailable
				if row.FailureCode == PictureNoSource {
					status = PictureNoSource
				}
			default:
				status = PictureUnknown
			}
			break
		}
		current.Images = append(current.Images, PictureState{SegmentIndex: index, Status: status})
	}
	current.ImageCount = len(current.Images)
	return current
}

// ReadInboundPictures decodes the versioned whitelist stored at ingress.
// ReadInboundPictures 读取入站时保存的版本化白名单。
func ReadInboundPictures(raw []byte) *PictureSummary {
	var summary PictureSummary
	if len(raw) == 0 || json.Unmarshal(raw, &summary) != nil || summary.Version != 1 || summary.ImageCount != len(summary.Images) {
		return nil
	}
	for i, image := range summary.Images {
		if image.SegmentIndex < 0 || i > 0 && summary.Images[i-1].SegmentIndex >= image.SegmentIndex {
			return nil
		}
		switch image.Status {
		case PictureArchiveOff, PicturePending, PictureNoSource:
		default:
			return nil
		}
	}
	return &summary
}

type ModelMediaAttempt struct {
	Shape        string
	State        string
	Images       []int  `json:",omitempty"`
	Hints        []int  `json:",omitempty"`
	HTTPStatus   int    `json:",omitempty"`
	ResponseBody string `json:",omitempty"`
}

const (
	ModelMediaVersion      = 1
	MediaUnknown           = "unknown"
	MediaNotCalled         = "not_called"
	MediaTextOnly          = "text_only"
	MediaHint              = "presence_hint"
	MediaImage             = "image_submitted"
	MediaStarted           = "started"
	MediaWritten           = "written"
	MediaFailedBeforeWrite = "failed_before_write"
	MediaPartialWrite      = "partial_write"
	MediaCompleted         = "completed"
	MediaSucceeded         = "succeeded"
	MediaFailed            = "failed"
	MediaUnattributed      = "transport_attempt_unattributed"
	MediaBodyReadFailed    = "response_body_read_failed"
	MediaBodyComplete      = "complete"
	MediaBodyClosedUnknown = "closed_unknown"
	MediaBodyClosedEarly   = "closed_early"
)

type ModelMediaCall struct {
	Outcome             string
	Reason              string              `json:",omitempty"`
	AttemptCountUnknown bool                `json:",omitempty"`
	HTTPStatus          int                 `json:",omitempty"`
	ResponseBody        string              `json:",omitempty"`
	Attempts            []ModelMediaAttempt `json:",omitempty"`
}

type ModelMedia struct {
	Version     int
	State       string
	StartFailed bool             `json:",omitempty"`
	Calls       []ModelMediaCall `json:",omitempty"`
}

type DecisionMedia struct {
	DecisionID int64
	AtDecision *PictureSummary `json:",omitempty"`
	Intent     ModelMedia
	Generation ModelMedia
}

type InboundMedia struct {
	AtIngress *PictureSummary `json:",omitempty"`
	Current   PictureSummary
	Decisions []DecisionMedia `json:",omitempty"`
}

// ReadDecisionPictures validates archived state codes before returning the summary.
// ReadDecisionPictures 校验冻结摘要中的受控状态码。
func ReadDecisionPictures(summary *PictureSummary) *PictureSummary {
	if summary == nil || summary.Version != 1 || summary.ImageCount != len(summary.Images) {
		return nil
	}
	for i, picture := range summary.Images {
		if picture.SegmentIndex < 0 || i > 0 && summary.Images[i-1].SegmentIndex >= picture.SegmentIndex {
			return nil
		}
		switch picture.Status {
		case PictureUnknown, PictureArchiveOff, PictureUnregistered, PicturePending,
			PictureNoSource, PictureUnavailable, PictureUploaded, PictureExpired:
		default:
			return nil
		}
	}
	return summary
}

// SafeMediaCall limits stored call evidence to approved fields and codes.
// SafeMediaCall 将存储的调用证据限制在获准字段和状态码内。
func SafeMediaCall(call ModelMediaCall) ModelMediaCall {
	switch call.Outcome {
	case MediaStarted, MediaSucceeded, MediaFailed:
	default:
		call.Outcome = MediaUnknown
	}
	switch call.Reason {
	case MediaUnattributed, MediaBodyReadFailed, MediaFailedBeforeWrite:
	default:
		call.Reason = ""
	}
	if call.HTTPStatus < 100 || call.HTTPStatus > 599 {
		call.HTTPStatus = 0
	}
	call.ResponseBody = safeResponseBody(call.ResponseBody)
	for i, attempt := range call.Attempts {
		attempt.ResponseBody = safeResponseBody(attempt.ResponseBody)
		if attempt.HTTPStatus < 100 || attempt.HTTPStatus > 599 {
			attempt.HTTPStatus = 0
		}
		if attempt.State != MediaWritten {
			if attempt.State != MediaPartialWrite {
				attempt.State = MediaUnknown
			}
			attempt.Shape, attempt.Images, attempt.Hints = MediaUnknown, nil, nil
		} else {
			switch attempt.Shape {
			case MediaTextOnly, MediaHint, MediaImage:
			default:
				attempt.Shape, attempt.Images, attempt.Hints = MediaUnknown, nil, nil
			}
		}
		call.Attempts[i] = attempt
	}
	return call
}

func safeResponseBody(state string) string {
	switch state {
	case MediaUnknown, MediaBodyComplete, MediaBodyReadFailed, MediaBodyClosedUnknown, MediaBodyClosedEarly:
		return state
	default:
		return ""
	}
}
