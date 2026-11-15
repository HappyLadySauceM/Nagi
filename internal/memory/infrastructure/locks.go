package infrastructure

import (
	"context"
	"database/sql"
	"fmt"
)

// sqlExecutor is the transaction executor shape used by memory locks.
// sqlExecutor 是记忆锁使用的 SQL 事务执行器形状。
type sqlExecutor interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

// LockMemoryMutation serializes every memory row mutation for one bot. Memory
// mutation paths take this lock before memory-authorization locks and then
// lock memory rows in UUID order; ingest/scan paths keep their existing
// conversation -> authorization order and do not take this lock.
// LockMemoryMutation 按 bot 串行化所有记忆行变更。记忆变更路径先取该锁，再取
// 记忆授权锁并按下 UUID 顺序锁行；消息入库/扫描路径保持既有
// conversation -> authorization 顺序，不取该锁。
func LockMemoryMutation(ctx context.Context, executor sqlExecutor, botID int64) error {
	if botID <= 0 {
		return fmt.Errorf("memory: mutation bot id must be positive")
	}
	if executor == nil {
		return fmt.Errorf("memory: mutation executor required")
	}
	key := fmt.Sprintf("memory:mutate:%d", botID)
	_, err := executor.ExecContext(ctx, "SELECT pg_advisory_xact_lock(hashtextextended($1,0))", key)
	if err != nil {
		return fmt.Errorf("memory: lock mutation: %w", err)
	}
	return nil
}
