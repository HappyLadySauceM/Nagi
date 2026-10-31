// Package db owns the shared PostgreSQL connection pool.
// db 包持有共享 PostgreSQL 连接池。
package db

import (
	"context"
	"database/sql"
	"fmt"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	"github.com/HappyLadySauceM/Nagi/pkg/db/ent"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
)

// Pool shares a bounded sql.DB between Ent and atomic repository SQL.
// Pool 在 Ent 与仓储原子 SQL 间共享有界 sql.DB。
type Pool struct {
	DB     *sql.DB
	Client *ent.Client
}

func Connect(ctx context.Context, dsn string, maxConns int32) (*Pool, error) {
	if maxConns <= 0 || maxConns > 12 {
		return nil, fmt.Errorf("database connection limit must be between 1 and 12")
	}
	cfg, err := pgx.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("parse dsn: %w", err)
	}
	database := stdlib.OpenDB(*cfg)
	database.SetMaxOpenConns(int(maxConns))
	database.SetMaxIdleConns(int(maxConns))
	if err = database.PingContext(ctx); err != nil {
		database.Close()
		return nil, fmt.Errorf("ping database: %w", err)
	}
	var version int
	if err := database.QueryRowContext(ctx, "SHOW server_version_num").Scan(&version); err != nil {
		database.Close()
		return nil, err
	}
	if version < 170000 || version >= 180000 {
		database.Close()
		return nil, fmt.Errorf("PostgreSQL 17 required, found server version %d", version)
	}
	return &Pool{DB: database, Client: ent.NewClient(ent.Driver(entsql.OpenDB(dialect.Postgres, database)))}, nil
}
func (p *Pool) Close()                         { _ = p.Client.Close() }
func (p *Pool) Ping(ctx context.Context) error { return p.DB.PingContext(ctx) }
func (p *Pool) Query(ctx context.Context, q string, args ...any) (*sql.Rows, error) {
	return p.DB.QueryContext(ctx, q, args...)
}
func (p *Pool) QueryRow(ctx context.Context, q string, args ...any) *sql.Row {
	return p.DB.QueryRowContext(ctx, q, args...)
}
func (p *Pool) Exec(ctx context.Context, q string, args ...any) (Result, error) {
	r, e := p.DB.ExecContext(ctx, q, args...)
	return result(r, e)
}
func (p *Pool) Begin(ctx context.Context) (*Tx, error) {
	t, e := p.DB.BeginTx(ctx, nil)
	return &Tx{t}, e
}

type Result struct{ affected int64 }

func (r Result) RowsAffected() int64 { return r.affected }
func result(r sql.Result, err error) (Result, error) {
	if err != nil {
		return Result{}, err
	}
	n, err := r.RowsAffected()
	return Result{affected: n}, err
}

type Tx struct{ *sql.Tx }

func (t *Tx) Exec(ctx context.Context, q string, args ...any) (Result, error) {
	r, e := t.ExecContext(ctx, q, args...)
	return result(r, e)
}
func (t *Tx) Query(ctx context.Context, q string, args ...any) (*sql.Rows, error) {
	return t.QueryContext(ctx, q, args...)
}
func (t *Tx) QueryRow(ctx context.Context, q string, args ...any) *sql.Row {
	return t.QueryRowContext(ctx, q, args...)
}
func (t *Tx) Commit(context.Context) error   { return t.Tx.Commit() }
func (t *Tx) Rollback(context.Context) error { return t.Tx.Rollback() }
