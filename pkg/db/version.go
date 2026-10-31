package db

import (
	"context"
	"fmt"
)

const RequiredVersion int64 = 20261031091021

// CheckVersion validates the externally applied schema and SQL-owned metadata without DDL.
// CheckVersion 校验外部迁移版本与 SQL 管理的元数据表，不执行 DDL。
func CheckVersion(ctx context.Context, p *Pool) error {
	var valid bool
	err := p.QueryRow(ctx, `SELECT (SELECT count(*) FROM information_schema.columns WHERE table_schema='public' AND table_name='schema_migrations')=3 AND (SELECT count(*) FROM information_schema.columns WHERE table_schema='public' AND table_name='schema_migrations' AND is_nullable='NO' AND ((column_name='version' AND data_type='bigint') OR (column_name='name' AND data_type='text') OR (column_name='applied_at' AND data_type='timestamp with time zone' AND column_default='now()')))=3 AND EXISTS(SELECT 1 FROM pg_constraint WHERE conrelid=to_regclass('public.schema_migrations') AND contype='p' AND pg_get_constraintdef(oid)='PRIMARY KEY (version)')`).Scan(&valid)
	if err != nil {
		return fmt.Errorf("inspect migration metadata: %w", err)
	}
	if !valid {
		return fmt.Errorf("invalid public.schema_migrations structure; execute reviewed migrations independently")
	}
	var version int64
	if err := p.QueryRow(ctx, "SELECT COALESCE(MAX(version),0) FROM public.schema_migrations").Scan(&version); err != nil {
		return fmt.Errorf("read schema version: %w", err)
	}
	if version != RequiredVersion {
		return fmt.Errorf("schema version %d, required %d; execute reviewed migrations independently", version, RequiredVersion)
	}
	return nil
}
