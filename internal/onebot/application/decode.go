package application

import (
	"encoding/json"
	"fmt"

	"github.com/HappyLadySauceM/Nagi/internal/onebot/domain"
)

// decodeJSON decodes the action response data; null or empty yields a zero value.
// decodeJSON 解析动作响应 data；null 或空得到零值（兼容 retcode=1 异步提交）。
func decodeJSON(payload json.RawMessage, target any) error {
	if len(payload) == 0 || string(payload) == "null" {
		return nil
	}
	if err := json.Unmarshal(payload, target); err != nil {
		return fmt.Errorf("onebot: decode response: %w", err)
	}
	return nil
}

// decodeMessagePayload parses a message field that may be a wire array or a CQ string.
// decodeMessagePayload 解析可能为线上数组或 CQ 字符串的 message 字段。
func decodeMessagePayload(raw json.RawMessage) ([]domain.Segment, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil
	}
	if segments, err := domain.SegmentsFromWire(raw); err == nil {
		return segments, nil
	}
	var text string
	if err := json.Unmarshal(raw, &text); err != nil {
		return nil, fmt.Errorf("onebot: decode message payload: %w", err)
	}
	return domain.ParseCQ(text), nil
}

// decodeExtra keeps the unparsed remainder of a payload; nil when nothing is left.
// decodeExtra 保留载荷中未被解析的其余字段；无剩余时返回 nil。
func decodeExtra(payload json.RawMessage, known ...string) map[string]any {
	var full map[string]any
	if err := json.Unmarshal(payload, &full); err != nil {
		return nil
	}
	for _, key := range known {
		delete(full, key)
	}
	if len(full) == 0 {
		return nil
	}
	return full
}
