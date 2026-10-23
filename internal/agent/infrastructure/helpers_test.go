// Shared helpers for agent infrastructure tests.
// agent 基础设施测试的共享辅助。
package infrastructure

import (
	"encoding/json"
	"net/http"
)

// decodeBody decodes a JSON request body.
// decodeBody 解码 JSON 请求体。
func decodeBody(r *http.Request, v any) error {
	return json.NewDecoder(r.Body).Decode(v)
}
