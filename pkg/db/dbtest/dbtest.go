// Package dbtest opens a migration-ready database for integration tests.
// dbtest 包为集成测试打开已完成迁移的数据库。
//
// The integration suite drops and recreates every table, so the DSN is always
// forced onto a "_test" database: the base DSN comes from QQBOT_TEST_DSN or,
// when that is empty, from `database.dsn` in the user config file. Even when the
// base points at the live database (…/qqbot), the tests run against …/qqbot_test.
// 集成测试会删除并重建全部表，因此 DSN 始终落到 "_test" 库：基准 DSN 取自
// QQBOT_TEST_DSN，未设置时取用户配置中的 database.dsn。即使基准指向正式库
// （…/qqbot），实际执行的也是 …/qqbot_test。
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

	"github.com/jackc/pgx/v5/pgxpool"
	"gopkg.in/yaml.v3"

	"github.com/HappyLadySauceM/QQBot/migrations"
	"github.com/HappyLadySauceM/QQBot/pkg/db"
)

// defaultConfigPath is the config location relative to the repository root.
// defaultConfigPath 是相对仓库根目录的默认配置路径。
const defaultConfigPath = "configs/config.yaml"

// Open connects to the derived test database, applies pending migrations and
// registers cleanup. Tests skip when no base DSN can be resolved.
// Open 连接派生出的测试库，应用未执行的迁移并注册清理；无法解析基准 DSN 时跳过。
func Open(t *testing.T) *pgxpool.Pool {
	t.Helper()
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

// testDSN resolves the integration-test DSN. An empty DSN with a skip reason
// means no base configuration is available; a non-nil error is fatal.
// testDSN 解析集成测试 DSN：返回空 DSN 表示没有基准配置（跳过），返回错误表示致命问题。
func testDSN() (dsn string, skipReason string, err error) {
	if raw := strings.TrimSpace(os.Getenv("QQBOT_TEST_DSN")); raw != "" {
		derived, deriveErr := deriveTestDSN(raw)
		if deriveErr != nil {
			return "", "", deriveErr
		}
		return derived, "", nil
	}
	path, found := findConfigFile()
	if !found {
		return "", "QQBOT_TEST_DSN not set and " + defaultConfigPath +
			" not found; skipping database integration test", nil
	}
	base, readErr := readConfigDSN(path)
	if readErr != nil {
		return "", "", readErr
	}
	if base == "" {
		return "", path + " has no database.dsn; skipping database integration test", nil
	}
	derived, deriveErr := deriveTestDSN(base)
	if deriveErr != nil {
		return "", "", deriveErr
	}
	return derived, "", nil
}

// findConfigFile walks up from the working directory looking for the config file,
// because `go test` runs each package with its own directory as the cwd.
// QQBOT_CONFIG overrides the location.
// findConfigFile 从工作目录向上查找配置文件（go test 以包目录为 cwd）；
// QQBOT_CONFIG 可覆盖位置。
func findConfigFile() (string, bool) {
	if custom := strings.TrimSpace(os.Getenv("QQBOT_CONFIG")); custom != "" {
		if _, err := os.Stat(custom); err == nil {
			return custom, true
		}
		return "", false
	}
	dir, err := os.Getwd()
	if err != nil {
		return "", false
	}
	for depth := 0; depth < 6; depth++ {
		candidate := filepath.Join(dir, defaultConfigPath)
		if _, statErr := os.Stat(candidate); statErr == nil {
			return candidate, true
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return "", false
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
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return "", fmt.Errorf("parse dsn: %w", err)
	}
	name := cfg.ConnConfig.Database
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
	// Keyword/value form, e.g. "host=... dbname=qqbot".
	// 关键字形式，例如 "host=... dbname=qqbot"。
	return dsn + " dbname=" + target, nil
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
