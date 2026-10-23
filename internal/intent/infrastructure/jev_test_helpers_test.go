// Shared helpers for intent infrastructure tests.
// intent 基础设施测试的共享辅助。
package infrastructure

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
)

// discardLogger drops log output in tests.
// discardLogger 在测试中丢弃日志输出。
func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// readJSON decodes a request body.
// readJSON 解码请求体。
func readJSON(r *http.Request, v any) error {
	return json.NewDecoder(r.Body).Decode(v)
}
