package db

import (
	"encoding/json"
	"fmt"
)

type jsonScanner struct{ value any }

// JSON adapts JSON values for parameterized repository SQL scans.
// JSON 适配参数化仓储 SQL 的 JSON 扫描。
func JSON(value any) interface{ Scan(any) error } { return jsonScanner{value} }
func (s jsonScanner) Scan(value any) error {
	switch v := value.(type) {
	case nil:
		return nil
	case []byte:
		return json.Unmarshal(v, s.value)
	case string:
		return json.Unmarshal([]byte(v), s.value)
	default:
		return fmt.Errorf("unsupported JSON source %T", value)
	}
}
