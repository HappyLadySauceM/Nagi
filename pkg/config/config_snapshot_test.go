// Golden snapshots for built-in defaults and the shipped example config.
// 内置默认值与示例配置的漂移快照。
package config

import (
	"bytes"
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

var updateConfigSnapshots = flag.Bool("update", false, "更新配置漂移快照")

// TestConfigDefaultsSnapshot pins the built-in defaults loaded without a file
// or environment overrides.
// TestConfigDefaultsSnapshot 固定无文件、无环境变量时的内置默认值。
func TestConfigDefaultsSnapshot(t *testing.T) {
	cfg, err := Load(Options{LookupEnv: func(string) (string, bool) { return "", false }})
	if err != nil {
		t.Fatalf("load defaults: %v", err)
	}
	assertConfigSnapshot(t, "defaults.golden.json", cfg)
}

// TestConfigExampleSnapshot pins the configuration produced by the shipped
// configs/config-example.yaml file.
// TestConfigExampleSnapshot 固定仓库 shipped configs/config-example.yaml 的装载结果。
func TestConfigExampleSnapshot(t *testing.T) {
	cfg, err := Load(Options{
		File:      filepath.Join("..", "..", "configs", "config-example.yaml"),
		LookupEnv: func(string) (string, bool) { return "", false },
	})
	if err != nil {
		t.Fatalf("load example config: %v", err)
	}
	assertConfigSnapshot(t, "config-example.golden.json", cfg)
}

func assertConfigSnapshot(t *testing.T, name string, cfg *Config) {
	t.Helper()
	got, err := json.MarshalIndent(configSnapshot(cfg), "", "  ")
	if err != nil {
		t.Fatalf("marshal %s: %v", name, err)
	}
	got = append(got, '\n')
	path := filepath.Join("testdata", name)
	if *updateConfigSnapshots {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("create snapshot dir: %v", err)
		}
		if err := os.WriteFile(path, got, 0o644); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读取快照 %s 失败: %v\n首次生成或有意更新请运行：go test ./pkg/config -run 'TestConfig.*Snapshot' -update", path, err)
	}
	if !bytes.Equal(want, got) {
		t.Fatalf("默认配置快照发生漂移（%s）：\n默认配置项和阈值会影响线上行为，必须由产品确认，不能当作普通测试更新。\n若为有意变更，请更新快照并在 PR 写明产品确认依据：\n  go test ./pkg/config -run 'TestConfig.*Snapshot' -update\n若为无意变更，请还原默认值。\n--- want ---\n%s\n--- got ---\n%s", name, want, got)
	}
}

// configSnapshot converts Config into a stable, readable map. Duration values
// are rendered as strings so snapshot diffs show "10s" instead of raw nanos.
// configSnapshot 将 Config 转为稳定可读的 map；Duration 渲染为字符串，便于审查 diff。
func configSnapshot(cfg *Config) map[string]any {
	return snapshotValue(reflect.ValueOf(*cfg)).(map[string]any)
}

func snapshotValue(value reflect.Value) any {
	if value.Type() == reflect.TypeOf(Duration(0)) {
		return time.Duration(value.Int()).String()
	}
	switch value.Kind() {
	case reflect.Struct:
		out := make(map[string]any, value.NumField())
		typ := value.Type()
		for i := 0; i < value.NumField(); i++ {
			field := typ.Field(i)
			name := field.Tag.Get("yaml")
			if comma := strings.IndexByte(name, ','); comma >= 0 {
				name = name[:comma]
			}
			if name == "" {
				name = field.Name
			}
			out[name] = snapshotValue(value.Field(i))
		}
		return out
	case reflect.Slice:
		if value.IsNil() {
			return nil
		}
		out := make([]any, value.Len())
		for i := 0; i < value.Len(); i++ {
			out[i] = snapshotValue(value.Index(i))
		}
		return out
	case reflect.Map:
		if value.IsNil() {
			return nil
		}
		out := make(map[string]any, value.Len())
		iter := value.MapRange()
		for iter.Next() {
			out[iter.Key().String()] = snapshotValue(iter.Value())
		}
		return out
	case reflect.Bool:
		return value.Bool()
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return value.Int()
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return value.Uint()
	case reflect.Float32, reflect.Float64:
		return value.Float()
	case reflect.String:
		return value.String()
	default:
		return value.Interface()
	}
}
