package domain

import "strings"

// Text-level CQ escaping: & [ ].
// 文本级 CQ 转义：& [ ]。
var cqTextUnescaper = strings.NewReplacer("&#91;", "[", "&#93;", "]", "&amp;", "&")

// CQ-code parameter escaping additionally covers the comma separator.
// CQ 码参数级转义额外覆盖逗号分隔符。
var cqParamUnescaper = strings.NewReplacer("&#44;", ",", "&#91;", "[", "&#93;", "]", "&amp;", "&")

// ParseCQ parses a CQ-code message string into segments.
// ParseCQ 将 CQ 码消息字符串解析为消息段。
// Malformed or unclosed codes degrade to literal text; it never fails.
// 畸形或未闭合的码一律降级为字面文本，永不失败。
func ParseCQ(raw string) []Segment {
	if raw == "" {
		return nil
	}
	var segments []Segment
	var text strings.Builder
	flush := func() {
		if text.Len() > 0 {
			segments = append(segments, Text(cqTextUnescaper.Replace(text.String())))
			text.Reset()
		}
	}
	for i := 0; i < len(raw); {
		if raw[i] == '[' && strings.HasPrefix(raw[i:], "[CQ:") {
			consumed, seg, ok := parseCQCode(raw[i:])
			if !ok {
				text.WriteByte(raw[i])
				i++
				continue
			}
			flush()
			segments = append(segments, seg)
			i += consumed
			continue
		}
		text.WriteByte(raw[i])
		i++
	}
	flush()
	return segments
}

// parseCQCode parses one "[CQ:type,k=v]" code at the start of s.
// parseCQCode 解析 s 开头的一个 "[CQ:type,k=v]" 码。
// It returns the consumed length and the segment; ok is false for any
// structural malformation so the caller can degrade to literal text.
// 返回消耗长度与消息段；任何结构性畸形返回 ok=false，由调用方降级为文本。
func parseCQCode(s string) (int, Segment, bool) {
	end := strings.IndexByte(s, ']')
	if end < 0 {
		return 0, Segment{}, false
	}
	body := s[len("[CQ:"):end]
	parts := strings.Split(body, ",")
	if len(parts) == 0 || parts[0] == "" {
		return 0, Segment{}, false
	}
	segType := cqTextUnescaper.Replace(parts[0])
	if segType == "" {
		return 0, Segment{}, false
	}
	data := make(map[string]string, len(parts)-1)
	for _, pair := range parts[1:] {
		key, value, found := strings.Cut(pair, "=")
		if !found || key == "" {
			return 0, Segment{}, false
		}
		data[cqParamUnescaper.Replace(key)] = cqParamUnescaper.Replace(value)
	}
	return end + 1, Segment{Type: SegmentType(segType), Data: data}, true
}
