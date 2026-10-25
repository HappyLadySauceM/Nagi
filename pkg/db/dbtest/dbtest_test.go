// Unit tests for the test-database derivation; no connection is opened.
// 测试库派生逻辑的单元测试；不会真正建立数据库连接。
package dbtest

import (
	"os"
	"path/filepath"
	"testing"
)

// TestDeriveTestDSN mirrors the user config: the live database in `database.dsn`
// is redirected to its "_test" sibling.
// TestDeriveTestDSN 对应实际配置：把 database.dsn 里的正式库重定向到 "_test" 库。
func TestDeriveTestDSN(t *testing.T) {
	cases := []struct {
		name string
		dsn  string
		want string
	}{
		{
			name: "live database gets _test suffix",
			dsn:  "postgres://qqbot:qqbot-local@127.0.0.1:5433/qqbot?sslmode=disable",
			want: "postgres://qqbot:qqbot-local@127.0.0.1:5433/qqbot_test?sslmode=disable",
		},
		{
			name: "already test database is kept",
			dsn:  "postgres://qqbot:qqbot-local@127.0.0.1:5433/qqbot_test?sslmode=disable",
			want: "postgres://qqbot:qqbot-local@127.0.0.1:5433/qqbot_test?sslmode=disable",
		},
		{
			name: "keyword dsn gets dbname override",
			dsn:  "host=127.0.0.1 port=5433 user=qqbot dbname=qqbot",
			want: "host=127.0.0.1 port=5433 user=qqbot dbname=qqbot dbname=qqbot_test",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := deriveTestDSN(tc.dsn)
			if err != nil {
				t.Fatalf("deriveTestDSN(%q) error: %v", tc.dsn, err)
			}
			if got != tc.want {
				t.Fatalf("deriveTestDSN(%q) = %q, want %q", tc.dsn, got, tc.want)
			}
		})
	}

	if _, err := deriveTestDSN("not-a-dsn"); err == nil {
		t.Fatal("deriveTestDSN accepted an unparsable DSN")
	}
}

// TestReadConfigDSN keeps parsing limited to database.dsn.
// TestReadConfigDSN 确认只解析 database.dsn。
func TestReadConfigDSN(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	content := "database:\n  dsn: postgres://qqbot:qqbot-local@127.0.0.1:5433/qqbot?sslmode=disable\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write temp config: %v", err)
	}
	dsn, err := readConfigDSN(path)
	if err != nil {
		t.Fatalf("readConfigDSN: %v", err)
	}
	want := "postgres://qqbot:qqbot-local@127.0.0.1:5433/qqbot?sslmode=disable"
	if dsn != want {
		t.Fatalf("readConfigDSN = %q, want %q", dsn, want)
	}

	empty := filepath.Join(dir, "empty.yaml")
	if err := os.WriteFile(empty, []byte("server:\n  listen: 127.0.0.1:5080\n"), 0o600); err != nil {
		t.Fatalf("write empty config: %v", err)
	}
	if dsn, err := readConfigDSN(empty); err != nil || dsn != "" {
		t.Fatalf("readConfigDSN(empty) = %q, %v; want empty DSN and no error", dsn, err)
	}
}
