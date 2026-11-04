package observability

import (
	"bytes"
	"encoding/json"
	"regexp"
	"strings"
)

const redactedSecret = "[REDACTED]"

var (
	sensitiveKey = regexp.MustCompile(`(?i)(authorization|cookie|token|api[_-]?key|secret|password|signature|credential|session[_-]?key)`)
	urlSecret    = regexp.MustCompile(`(?i)([?&](?:x-amz-(?:signature|credential|security-token)|signature|sig|token|api[_-]?key|key|access[_-]?token|session[_-]?token)=)[^&#\s]+`)
	authSecret   = regexp.MustCompile(`(?i)\b(bearer|basic)\s+[^\s,;]+`)
	headerSecret = regexp.MustCompile(`(?i)\b(authorization|cookie)\s*[:=]\s*[^\r\n,;]+`)
	keySecret    = regexp.MustCompile(`\b(?:sk|pk|rk)-[A-Za-z0-9_-]{8,}\b`)
)

// Redact converts a value to JSON-compatible data after removing credentials
// from nested fields and strings. It is used for both local audit facts and
// telemetry so the two stores have the same privacy boundary.
// Redact 将值转为 JSON 兼容数据并移除嵌套字段和字符串中的凭证。本地审计事实与
// 遥测都使用它，以保持相同的隐私边界。
func Redact(value any) any {
	b, err := json.Marshal(value)
	if err != nil {
		return "[unserializable]"
	}
	decoder := json.NewDecoder(bytes.NewReader(b))
	decoder.UseNumber()
	var decoded any
	if err := decoder.Decode(&decoded); err != nil {
		return "[unserializable]"
	}
	return redactValue(decoded)
}

// RedactString removes common inline credential forms while preserving normal
// application content. It is safe to apply to every string leaving the process.
// RedactString 移除常见的内联凭证格式，同时保留普通业务内容；可安全用于所有离开
// 进程的字符串。
func RedactString(value string) string {
	var decoded any
	if json.Unmarshal([]byte(value), &decoded) == nil {
		if sanitized, err := json.Marshal(redactValue(decoded)); err == nil {
			return string(sanitized)
		}
	}
	value = urlSecret.ReplaceAllString(value, "${1}"+redactedSecret)
	value = authSecret.ReplaceAllString(value, "${1} "+redactedSecret)
	value = headerSecret.ReplaceAllString(value, "${1}: "+redactedSecret)
	return keySecret.ReplaceAllString(value, redactedSecret)
}

func redactValue(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		out := make(map[string]any, len(typed))
		for key, child := range typed {
			if sensitiveKey.MatchString(strings.TrimSpace(key)) {
				out[key] = redactedSecret
				continue
			}
			out[key] = redactValue(child)
		}
		return out
	case []any:
		out := make([]any, len(typed))
		for i, child := range typed {
			out[i] = redactValue(child)
		}
		return out
	case string:
		return RedactString(typed)
	default:
		return value
	}
}
