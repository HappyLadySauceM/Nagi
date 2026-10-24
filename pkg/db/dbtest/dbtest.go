// Package dbtest opens a migration-ready database for integration tests.
// dbtest 包为集成测试打开已完成迁移的数据库。
//
// Tests are gated by QQBOT_TEST_DSN: without it they skip instead of failing.
// 测试以 QQBOT_TEST_DSN 为门控：未设置时跳过而非失败。
package dbtest

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/HappyLadySauceM/QQBot/migrations"
	"github.com/HappyLadySauceM/QQBot/pkg/db"
)

// Open connects to QQBOT_TEST_DSN, applies pending migrations and registers cleanup.
// Open 连接 QQBOT_TEST_DSN，应用未执行的迁移并注册清理。
func Open(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := strings.TrimSpace(os.Getenv("QQBOT_TEST_DSN"))
	if dsn == "" {
		t.Skip("QQBOT_TEST_DSN not set; skipping database integration test")
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

// lockDatabase takes an exclusive cross-process file lock for the test lifetime.
// lockDatabase 在测试生命周期内持有跨进程独占文件锁。
func lockDatabase(t *testing.T) func() {
	t.Helper()
	path := filepath.Join(os.TempDir(), "qqbot-dbtest.lock")
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
