// Package infrastructure implements the audit recorder on PostgreSQL.
// audit/infrastructure 在 PostgreSQL 上实现审计记录器。
package infrastructure

import (
	"context"
	"fmt"

	"github.com/HappyLadySauceM/Nagi/pkg/db"

	"github.com/HappyLadySauceM/Nagi/internal/audit/domain"
)

// Recorder is the pgx-backed audit recorder.
// Recorder 是基于 pgx 的审计记录器。
type Recorder struct {
	pool *db.Pool
}

// NewRecorder builds the recorder; the pool must not be nil.
// NewRecorder 构建记录器；pool 不能为 nil。
func NewRecorder(pool *db.Pool) (*Recorder, error) {
	if pool == nil {
		return nil, fmt.Errorf("audit: pool required")
	}
	return &Recorder{pool: pool}, nil
}

// Record inserts one audit entry; the timestamp is set by the database.
// Record 插入一条审计记录；时间戳由数据库生成。
func (r *Recorder) Record(ctx context.Context, entry domain.Entry) error {
	return r.pool.Client.AdminAuditLog.Create().SetActor(entry.Actor).SetAction(entry.Action).SetResource(entry.Resource).SetBefore(entry.Before).SetAfter(entry.After).Exec(ctx)
}
