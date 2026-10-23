// Migrate applies versioned SQL migrations from an embedded filesystem.
// Migrate 从内嵌文件系统应用版本化 SQL 迁移。
package db

import (
	"context"
	"fmt"
	"io/fs"
	"regexp"
	"sort"
	"strconv"

	"github.com/jackc/pgx/v5/pgxpool"
)

// migrationFile matches names like "0002_pipeline.up.sql".
// migrationFile 匹配形如 "0002_pipeline.up.sql" 的文件名。
var migrationFile = regexp.MustCompile(`^(\d+)_([a-z0-9_]+)\.(up|down)\.sql$`)

// migration is one versioned schema step; Down is required by convention.
// migration 是一个版本化的模式步骤；按约定必须提供 Down。
type migration struct {
	Version int
	Name    string
	Up      string
	Down    string
}

// parseMigrations loads and orders all migration files; every version needs up and down.
// parseMigrations 加载并排序全部迁移文件；每个版本都必须有 up 和 down。
func parseMigrations(fsys fs.FS) ([]migration, error) {
	entries, err := fs.Glob(fsys, "*.sql")
	if err != nil {
		return nil, fmt.Errorf("list migrations: %w", err)
	}
	byVersion := make(map[int]*migration)
	for _, name := range entries {
		groups := migrationFile.FindStringSubmatch(name)
		if groups == nil {
			return nil, fmt.Errorf("migration file %s: invalid name (want NNNN_name.up.sql)", name)
		}
		version, err := strconv.Atoi(groups[1])
		if err != nil {
			return nil, fmt.Errorf("migration file %s: bad version: %w", name, err)
		}
		content, err := fs.ReadFile(fsys, name)
		if err != nil {
			return nil, fmt.Errorf("read migration %s: %w", name, err)
		}
		m := byVersion[version]
		if m == nil {
			m = &migration{Version: version, Name: groups[2]}
			byVersion[version] = m
		}
		switch groups[3] {
		case "up":
			m.Up = string(content)
		case "down":
			m.Down = string(content)
		}
	}
	ordered := make([]migration, 0, len(byVersion))
	for _, m := range byVersion {
		if m.Up == "" || m.Down == "" {
			return nil, fmt.Errorf("migration %04d_%s: both up and down are required", m.Version, m.Name)
		}
		ordered = append(ordered, *m)
	}
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].Version < ordered[j].Version })
	return ordered, nil
}

// migrationLockKey serializes migration runs across processes with an advisory lock.
// migrationLockKey 通过 advisory lock 跨进程串行化迁移执行。
const migrationLockKey int64 = 629378877

// Migrate applies every pending migration in ascending order inside one locked
// transaction, together with its schema_migrations bookkeeping rows.
// Migrate 在单个持锁事务内按版本升序应用所有未执行的迁移，
// 并写入 schema_migrations 记录。重复调用是安全的。
func Migrate(ctx context.Context, pool *pgxpool.Pool, fsys fs.FS) error {
	migs, err := parseMigrations(fsys)
	if err != nil {
		return err
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin migrate: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, migrationLockKey); err != nil {
		return fmt.Errorf("acquire migration lock: %w", err)
	}
	if _, err := tx.Exec(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (
		version BIGINT PRIMARY KEY,
		name TEXT NOT NULL,
		applied_at TIMESTAMPTZ NOT NULL DEFAULT now()
	)`); err != nil {
		return fmt.Errorf("ensure schema_migrations: %w", err)
	}
	for _, m := range migs {
		var applied bool
		if err := tx.QueryRow(ctx,
			`SELECT EXISTS(SELECT 1 FROM schema_migrations WHERE version = $1)`, m.Version,
		).Scan(&applied); err != nil {
			return fmt.Errorf("check migration %04d: %w", m.Version, err)
		}
		if applied {
			continue
		}
		if _, err := tx.Exec(ctx, m.Up); err != nil {
			return fmt.Errorf("apply migration %04d_%s: %w", m.Version, m.Name, err)
		}
		if _, err := tx.Exec(ctx,
			`INSERT INTO schema_migrations (version, name) VALUES ($1, $2)`, m.Version, m.Name,
		); err != nil {
			return fmt.Errorf("record migration %04d: %w", m.Version, err)
		}
	}
	return tx.Commit(ctx)
}

// Down reverses every applied migration in descending order, for local dev and test resets.
// Down 按版本降序回滚所有已应用的迁移，用于本地开发与测试重置。
func Down(ctx context.Context, pool *pgxpool.Pool, fsys fs.FS) error {
	var exists bool
	if err := pool.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM information_schema.tables WHERE table_name = 'schema_migrations')`,
	).Scan(&exists); err != nil {
		return fmt.Errorf("check schema_migrations: %w", err)
	}
	if !exists {
		return nil
	}
	migs, err := parseMigrations(fsys)
	if err != nil {
		return err
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin down: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, migrationLockKey); err != nil {
		return fmt.Errorf("acquire migration lock: %w", err)
	}
	for i := len(migs) - 1; i >= 0; i-- {
		m := migs[i]
		var applied bool
		if err := tx.QueryRow(ctx,
			`SELECT EXISTS(SELECT 1 FROM schema_migrations WHERE version = $1)`, m.Version,
		).Scan(&applied); err != nil {
			return fmt.Errorf("check migration %04d: %w", m.Version, err)
		}
		if !applied {
			continue
		}
		if _, err := tx.Exec(ctx, m.Down); err != nil {
			return fmt.Errorf("revert migration %04d_%s: %w", m.Version, m.Name, err)
		}
		if _, err := tx.Exec(ctx, `DELETE FROM schema_migrations WHERE version = $1`, m.Version); err != nil {
			return fmt.Errorf("unrecord migration %04d: %w", m.Version, err)
		}
	}
	return tx.Commit(ctx)
}
