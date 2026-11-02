package application

import (
	"encoding/json"
	"testing"
)

// TestRecordSizeWireVariants preserves audio when a bridge encodes file_size as a string.
// TestRecordSizeWireVariants 验证桥接端使用字符串文件大小时仍保留转码音频。
func TestRecordSizeWireVariants(t *testing.T) {
	for _, size := range []string{`42`, `"42"`} {
		got, err := decodeFileResult(json.RawMessage(`{"file":"audio.mp3","file_size":` + size + `,"base64":"YXVkaW8=","out_format":"mp3"}`))
		if err != nil || got.Size != 42 || got.Base64 != "YXVkaW8=" || got.OutFormat != "mp3" {
			t.Fatalf("variant %s lost converted audio: %+v err=%v", size, got, err)
		}
	}
}
