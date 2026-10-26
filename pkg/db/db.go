// Package db provides PostgreSQL connectivity for Nagi.
// db 包提供 Nagi 的 PostgreSQL 连接能力。
package db

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Connect opens a connection pool with the configured size limit and pings it once.
// Connect 按配置的连接上限打开连接池，并做一次连通性检查。
//
// The caller owns the pool and must Close it on shutdown.
// 调用方持有连接池，退出时负责 Close。
func Connect(ctx context.Context, dsn string, maxConns int32) (*pgxpool.Pool, error) {
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("parse dsn: %w", err)
	}
	if maxConns > 0 {
		cfg.MaxConns = maxConns
	}
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("create pool: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping database: %w", err)
	}
	return pool, nil
}
