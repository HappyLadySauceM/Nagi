// Package contracts defines media inputs without framework dependencies.
// Package contracts 定义不依赖模型框架的媒体输入。
package contracts

import (
	"context"
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
