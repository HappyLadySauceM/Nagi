// Integration test for the migration runner, gated by QQBOT_TEST_DSN.
// 迁移执行器的集成测试，以 QQBOT_TEST_DSN 为门控。
package db_test

import (
	"context"
	"io/fs"
	"testing"

	"github.com/HappyLadySauceM/QQBot/migrations"
	"github.com/HappyLadySauceM/QQBot/pkg/db"
	"github.com/HappyLadySauceM/QQBot/pkg/db/dbtest"
)

// TestMigrateAppliesAndReverses verifies that Migrate creates the core and
// pipeline tables, Down removes them again and re-running Migrate is idempotent.
// TestMigrateAppliesAndReverses 验证 Migrate 创建核心与流水线表、Down 再次移除、
// 重复执行 Migrate 幂等。
func TestMigrateAppliesAndReverses(t *testing.T) {
	pool := dbtest.Open(t)
	ctx := context.Background()

	// Idempotent: a second Migrate on an already-migrated database must succeed.
	// 幂等：对已迁移数据库再次执行 Migrate 必须成功。
	if err := db.Migrate(ctx, pool, migrations.FS); err != nil {
		t.Fatalf("re-migrate should be idempotent: %v", err)
	}
	for _, table := range []string{
		"bot_accounts", "contacts", "qq_groups", "group_members", "scope_policies", "admin_audit_logs",
		"conversations", "messages", "message_jobs", "active_threads", "intent_decisions", "memories",
	} {
		var exists bool
		if err := pool.QueryRow(ctx,
			`SELECT to_regclass($1) IS NOT NULL`, "public."+table,
		).Scan(&exists); err != nil {
			t.Fatalf("check table %s: %v", table, err)
		}
		if !exists {
			t.Fatalf("table %s should exist after Migrate", table)
		}
	}

	if err := db.Down(ctx, pool, migrations.FS); err != nil {
		t.Fatalf("down: %v", err)
	}
	for _, table := range []string{"messages", "conversations", "scope_policies", "bot_accounts"} {
		var exists bool
		if err := pool.QueryRow(ctx,
			`SELECT to_regclass($1) IS NOT NULL`, "public."+table,
		).Scan(&exists); err != nil {
			t.Fatalf("check table %s after down: %v", table, err)
		}
		if exists {
			t.Fatalf("table %s should not exist after Down", table)
		}
	}

	if err := db.Migrate(ctx, pool, migrations.FS); err != nil {
		t.Fatalf("migrate again after down: %v", err)
	}
	var count int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM schema_migrations`).Scan(&count); err != nil {
		t.Fatalf("count schema_migrations: %v", err)
	}
	// Derive the expected version count from the embedded files so adding a
	// migration never requires editing this test: each version pairs one
	// up and one down file.
	// 从内嵌文件推导期望版本数，新增迁移无需修改本测试：每个版本包含一对 up/down 文件。
	entries, err := fs.ReadDir(migrations.FS, ".")
	if err != nil {
		t.Fatalf("read embedded migrations: %v", err)
	}
	if want := len(entries) / 2; count != want {
		t.Fatalf("schema_migrations should record %d versions, got %d", want, count)
	}
}
