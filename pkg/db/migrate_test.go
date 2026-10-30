// Integration test for the migration runner, gated by NAGI_TEST_DSN.
// 迁移执行器的集成测试，以 NAGI_TEST_DSN 为门控。
package db_test

import (
	"context"
	"io/fs"
	"testing"
	"testing/fstest"

	"github.com/HappyLadySauceM/Nagi/migrations"
	"github.com/HappyLadySauceM/Nagi/pkg/db"
	"github.com/HappyLadySauceM/Nagi/pkg/db/dbtest"
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

// TestUnifiedSendingMigrationPreservesLegacyGrants checks absent, explicit and denied grants.
// TestUnifiedSendingMigrationPreservesLegacyGrants 验证迁移保留缺省、显式允许及拒绝权限。
func TestUnifiedSendingMigrationPreservesLegacyGrants(t *testing.T) {
	pool := dbtest.Open(t)
	ctx := context.Background()
	subset := fstest.MapFS{}
	for _, name := range []string{"0015_unified_scope_sending.up.sql", "0015_unified_scope_sending.down.sql"} {
		raw, err := fs.ReadFile(migrations.FS, name)
		if err != nil {
			t.Fatal(err)
		}
		subset[name] = &fstest.MapFile{Data: raw}
	}
	if err := db.Down(ctx, pool, subset); err != nil {
		t.Fatal(err)
	}
	const botID = 999781
	if _, err := pool.Exec(ctx, "INSERT INTO bot_accounts(bot_id,nickname) VALUES($1,'migration-grants') ON CONFLICT DO NOTHING", botID); err != nil {
		t.Fatal(err)
	}
	defer func() {
		pool.Exec(ctx, "DELETE FROM scope_policies WHERE bot_id=$1", botID)
		pool.Exec(ctx, "DELETE FROM agent_sending_policies WHERE bot_id=$1", botID)
	}()
	permissions := []string{"{}", "{\"qq.reply.current\":true}", "{\"qq.reply.current\":false}"}
	for i, permission := range permissions {
		id := i + 501
		if _, err := pool.Exec(ctx, "INSERT INTO scope_policies(bot_id,scope_type,scope_id,enabled,tool_permissions) VALUES($1,'private',$2,true,$3)", botID, id, permission); err != nil {
			t.Fatal(err)
		}
		if _, err := pool.Exec(ctx, "INSERT INTO agent_sending_policies(bot_id,scope_type,scope_id,allow_send_current,max_messages_per_run,min_interval_ms) VALUES($1,'private',$2,$3,4,800)", botID, id, i != 1); err != nil {
			t.Fatal(err)
		}
	}
	verify := func(want []string) {
		for i, expected := range want {
			var grant string
			if err := pool.QueryRow(ctx, "SELECT COALESCE(tool_permissions->>'qq.reply.current','absent') FROM scope_policies WHERE bot_id=$1 AND scope_id=$2", botID, i+501).Scan(&grant); err != nil {
				t.Fatal(err)
			}
			if grant != expected {
				t.Fatalf("grant %d: got %s want %s", i, grant, expected)
			}
		}
	}
	for cycle := 0; cycle < 2; cycle++ {
		if _, err := db.Migrate(ctx, pool, subset); err != nil {
			t.Fatal(err)
		}
		verify([]string{"absent", "false", "false"})
		var count, interval int
		if err := pool.QueryRow(ctx, "SELECT max_messages_per_run,message_interval_ms FROM scope_policies WHERE bot_id=$1 AND scope_id=501", botID).Scan(&count, &interval); err != nil {
			t.Fatal(err)
		}
		if count != 4 || interval != 800 {
			t.Fatalf("budget lost: %d/%d", count, interval)
		}
		if cycle == 0 {
			if err := db.Down(ctx, pool, subset); err != nil {
				t.Fatal(err)
			}
			verify([]string{"absent", "true", "false"})
		}
	}
}
