// Package dbtest helpers for Repository-tier PostgreSQL tests behind
// `//go:build integration`, run via just verify-repo with NAGI_TEST_DSN.
// Default `go test ./...` does not connect; application tests use testsupport fakes.
// dbtest 包仅为带 `//go:build integration` 的旧式 PostgreSQL 集成测试提供辅助；
// 默认 `go test ./...` 使用 internal/testsupport 的仓库 mock，不连接 DSN、
// 配置或嵌入式数据库。
package dbtest

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"gopkg.in/yaml.v3"

	"github.com/HappyLadySauceM/Nagi/pkg/db"
	"github.com/HappyLadySauceM/Nagi/pkg/db/migrations"
)

// Open connects to the derived test database, applies pending migrations and
// registers cleanup. Default builds skip: repository behavior uses testsupport mocks.
// Open 连接派生测试库并应用迁移。默认构建会跳过，仓储行为由 testsupport mock 覆盖。
func Open(t *testing.T) *db.Pool {
	t.Helper()
	if os.Getenv("NAGI_TEST_USE_DB") == "" {
		t.Skip("dbtest.Open disabled; use internal/testsupport repository mocks (set NAGI_TEST_USE_DB=1 only for legacy integration)")
	}
	dsn, skip, err := testDSN()
	if err != nil {
		t.Fatalf("dbtest: %v", err)
	}
	if dsn == "" {
		t.Skip(skip)
	}
	// Cross-process lock: `go test` runs packages in parallel and some tests
	// (the migration round-trip) drop tables, so DB integration tests serialize.
	// 跨进程锁：go test 并行运行测试包，而迁移往返测试会删表，
	// 因此数据库集成测试必须串行执行。
	t.Cleanup(lockDatabase(t))

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	pool, err := db.Connect(ctx, dsn, 4)
	if err != nil {
		t.Fatalf("dbtest: connect: %v", err)
	}
	t.Cleanup(pool.Close)
	if _, err := db.Migrate(ctx, pool, migrations.FS); err != nil {
		t.Fatalf("dbtest: migrate: %v", err)
	}
	return pool
}

// DSN returns the same derived integration DSN that Open would use.
// DSN 返回与 Open 相同的派生集成测试 DSN。
func DSN(t *testing.T) string {
	t.Helper()
	if os.Getenv("NAGI_TEST_USE_DB") == "" {
		t.Skip("dbtest.DSN disabled; use testsupport mocks")
	}
	dsn, skip, err := testDSN()
	if err != nil {
		t.Fatalf("dbtest: %v", err)
	}
	if dsn == "" {
		t.Skip(skip)
	}
	return dsn
}

// testDSN resolves the integration-test DSN. An empty DSN with a skip reason
// means no base configuration is available; a non-nil error is fatal.
// testDSN 解析集成测试 DSN：返回空 DSN 表示没有基准配置（跳过），返回错误表示致命问题。
func testDSN() (dsn string, skipReason string, err error) {
	raw := strings.TrimSpace(os.Getenv("NAGI_TEST_DSN"))
	if raw == "" {
		return "", "NAGI_TEST_DSN required when NAGI_TEST_USE_DB=1", nil
	}
	derived, deriveErr := deriveTestDSN(raw)
	if deriveErr != nil {
		return "", "", deriveErr
	}
	return derived, "", nil
}

// readConfigDSN reads only `database.dsn`, so unrelated config validation never
// blocks tests.
// readConfigDSN 只读取 database.dsn，避免其它配置校验影响测试。
func readConfigDSN(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read config %s: %w", path, err)
	}
	var parsed struct {
		Database struct {
			DSN string `yaml:"dsn"`
		} `yaml:"database"`
	}
	if err := yaml.Unmarshal(data, &parsed); err != nil {
		return "", fmt.Errorf("parse config %s: %w", path, err)
	}
	return strings.TrimSpace(parsed.Database.DSN), nil
}

// deriveTestDSN appends "_test" to the database name unless it is already there.
// Query parameters and credentials are preserved.
// deriveTestDSN 在库名后追加 "_test"（已存在则不变），保留账号与查询参数。
func deriveTestDSN(dsn string) (string, error) {
	cfg, err := pgx.ParseConfig(dsn)
	if err != nil {
		return "", fmt.Errorf("parse dsn: %w", err)
	}
	name := cfg.Database
	if name == "" {
		return "", fmt.Errorf("dsn has no database name")
	}
	if strings.HasSuffix(strings.ToLower(name), "_test") {
		return dsn, nil
	}
	target := name + "_test"
	if parsed, err := url.Parse(dsn); err == nil && parsed.Scheme != "" {
		parsed.Path = "/" + target
		return parsed.String(), nil
	}
	// Keyword/value form, e.g. "host=... dbname=nagi".
	// 关键字形式，例如 "host=... dbname=nagi"。
	return dsn + " dbname=" + target, nil
}

// lockDatabase takes an exclusive cross-process file lock for the test lifetime.
// lockDatabase 在测试生命周期内持有跨进程独占文件锁。
func lockDatabase(t *testing.T) func() {
	t.Helper()
	path := filepath.Join(os.TempDir(), "nagi-dbtest.lock")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		t.Fatalf("dbtest: open lock file: %v", err)
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		f.Close()
		t.Fatalf("dbtest: lock: %v", err)
	}
	return func() {
		_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		_ = f.Close()
	}
}
