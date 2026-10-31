// Integration test for the migration runner, gated by NAGI_TEST_DSN.
// 迁移执行器的集成测试，以 NAGI_TEST_DSN 为门控。
package db_test

import (
	"context"
	"io/fs"
	"testing"
	"time"

	"github.com/HappyLadySauceM/Nagi/pkg/db"
	"github.com/HappyLadySauceM/Nagi/pkg/db/dbtest"
	"github.com/HappyLadySauceM/Nagi/pkg/db/migrations"
)

// TestMigrateAppliesAndReverses verifies that Migrate creates the core and
// pipeline tables, Down removes them again and re-running Migrate is idempotent.
// TestMigrateAppliesAndReverses 验证 Migrate 创建核心与流水线表、Down 再次移除、
// 重复执行 Migrate 幂等。
func TestMigrateAppliesAndReverses(t *testing.T) {
	pool := dbtest.Open(t)
	ctx := context.Background()

	// Idempotent: a second Migrate on an already-migrated database must succeed
	// and apply nothing.
	// 幂等：对已迁移数据库再次执行 Migrate 必须成功且不应用任何版本。
	if applied, err := db.Migrate(ctx, pool, migrations.FS); err != nil {
		t.Fatalf("re-migrate should be idempotent: %v", err)
	} else if len(applied) != 0 {
		t.Fatalf("re-migrate should apply nothing, got %v", applied)
	}
	for _, table := range []string{
		"bot_accounts", "contacts", "qq_groups", "group_members", "scope_policies", "admin_audit_logs",
		"conversations", "messages", "message_jobs", "intent_decisions", "memories",
		"agent_outbound_messages", "agent_run_budgets",
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

	applied, err := db.Migrate(ctx, pool, migrations.FS)
	if err != nil {
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
	// After a full Down, the next Migrate must report every version as applied,
	// which is what the startup log relies on.
	// 完整 Down 之后的首次 Migrate 必须报告所有版本均已应用，启动日志依赖该返回值。
	if len(applied) != count {
		t.Fatalf("migrate after down should report %d applied versions, got %v", count, applied)
	}
}

func TestCheckVersionRejectsWrongRevisionAndMetadata(t *testing.T) {
	pool := dbtest.Open(t)
	ctx := context.Background()
	if err := db.CheckVersion(ctx, pool); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, "UPDATE schema_migrations SET version=version+1 WHERE version=$1", db.RequiredVersion); err != nil {
		t.Fatal(err)
	}
	restore := func() {
		_, err := pool.Exec(ctx, "UPDATE schema_migrations SET version=$1 WHERE version=$1::bigint+1", db.RequiredVersion)
		if err != nil {
			t.Error(err)
		}
	}
	t.Cleanup(restore)
	if err := db.CheckVersion(ctx, pool); err == nil {
		t.Fatal("unexpected migration revision accepted")
	}
	restore()
	if _, err := pool.Exec(ctx, "ALTER TABLE schema_migrations RENAME COLUMN applied_at TO wrong_column"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := pool.Exec(ctx, "ALTER TABLE schema_migrations RENAME COLUMN wrong_column TO applied_at"); err != nil {
			t.Error(err)
		}
	})
	if err := db.CheckVersion(ctx, pool); err == nil {
		t.Fatal("SQL-only metadata drift accepted")
	}
}

func TestEntAndSQLUseOneBoundedPool(t *testing.T) {
	pool := dbtest.Open(t)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if limit := pool.DB.Stats().MaxOpenConnections; limit != 4 {
		t.Fatalf("pool maximum=%d", limit)
	}
	// A single available connection makes an accidental second pool or pool call inside a transaction observable.
	// 仅允许一个连接，使额外连接池或事务内误用外层连接池能够被观测。
	pool.DB.SetMaxOpenConns(1)
	tx, err := pool.Client.Tx(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	rows, err := tx.Client().QueryContext(ctx, "SELECT pg_backend_pid()")
	if err != nil {
		t.Fatal(err)
	}
	var pid int
	if !rows.Next() {
		t.Fatal("transaction query returned no row")
	}
	if err := rows.Scan(&pid); err != nil {
		t.Fatal(err)
	}
	rows.Close()
	if _, err := tx.Client().BotAccount.Query().Count(ctx); err != nil {
		t.Fatal(err)
	}
	if open := pool.DB.Stats().OpenConnections; open != 1 {
		t.Fatalf("open connections=%d", open)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	var outside int
	if err := pool.QueryRow(ctx, "SELECT pg_backend_pid()").Scan(&outside); err != nil {
		t.Fatal(err)
	}
	if outside != pid {
		t.Fatalf("Ent and SQL used separate pools: %d/%d", pid, outside)
	}
}
