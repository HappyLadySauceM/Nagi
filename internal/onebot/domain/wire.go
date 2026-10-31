// Package domain — wire.go bridges the published protocol language and its wire shape.
// wire.go 在已发布的协议语言与线上形状之间双向转换，供 infrastructure 与合并转发复用。
package domain

import (
	"encoding/json"
	"fmt"
	"strconv"
)

// WireSegments converts segments into the generic OneBot wire shape.
// WireSegments 将消息段转换为通用的 OneBot 线上形状；输出为深复制，可安全修改。
func WireSegments(segments []Segment) []map[string]any {
	return wireSegments(segments, 0)
}

func wireSegments(segments []Segment, depth int) []map[string]any {
	out := make([]map[string]any, 0, len(segments))
	for _, s := range segments {
		data := make(map[string]any, len(s.Data))
		for k, v := range s.Data {
			data[k] = v
			if s.Type == SegNode && k == "content" && depth < 16 {
				if nested, err := SegmentsFromWire([]byte(v)); err == nil && nested != nil {
					data[k] = wireSegments(nested, depth+1)
				}
			}
		}
		out = append(out, map[string]any{"type": string(s.Type), "data": data})
	}
	return out
}

// SegmentsFromWire parses a JSON-encoded wire segment array.
// SegmentsFromWire 解析 JSON 编码的线上消息段数组。
// Data values are coerced to strings; complex values keep their JSON text.
// data 值统一转换为字符串；复杂值保留其 JSON 文本。
func SegmentsFromWire(data []byte) ([]Segment, error) {
	if len(data) == 0 || string(data) == "null" {
		return nil, nil
	}
	var rows []map[string]any
	if err := json.Unmarshal(data, &rows); err != nil {
		return nil, fmt.Errorf("onebot: decode segments: %w", err)
	}
	out := make([]Segment, 0, len(rows))
	for _, row := range rows {
		rawType, _ := row["type"].(string)
		if rawType == "" {
			return nil, fmt.Errorf("onebot: segment without type")
		}
		seg := Segment{Type: SegmentType(rawType)}
		if raw, ok := row["data"].(map[string]any); ok {
			seg.Data = stringifyWireMap(raw)
		}
		out = append(out, seg)
	}
	return out, nil
}

func stringifyWireMap(in map[string]any) map[string]string {
	if in == nil {
		return nil
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = stringifyWireValue(v)
	}
	return out
}

func stringifyWireValue(v any) string {
	switch value := v.(type) {
	case string:
		return value
	case bool:
		return strconv.FormatBool(value)
	case float64:
		return strconv.FormatFloat(value, 'f', -1, 64)
	case nil:
		return ""
	default:
		if b, err := json.Marshal(value); err == nil {
			return string(b)
		}
		return ""
	}
}
