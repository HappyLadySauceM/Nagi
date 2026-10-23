// PostgreSQL-backed runtime statistics for the metrics endpoint.
// 面向指标接口的 PostgreSQL 运行统计。
package infrastructure

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

// StatsRepo queries aggregate counters; it never returns message content.
// StatsRepo 查询聚合计数；绝不返回消息内容。
type StatsRepo struct {
	pool *pgxpool.Pool
}

// NewStatsRepo builds the repository; the pool must not be nil.
// NewStatsRepo 构建仓库；pool 不能为 nil。
func NewStatsRepo(pool *pgxpool.Pool) (*StatsRepo, error) {
	if pool == nil {
		return nil, fmt.Errorf("observability: pool required")
	}
	return &StatsRepo{pool: pool}, nil
}

// Stats returns aggregate counters for decisions, jobs, messages and memories.
// Stats 返回决策、任务、消息与记忆的聚合计数。
func (r *StatsRepo) Stats(ctx context.Context) (map[string]any, error) {
	out := make(map[string]any)

	jobs := map[string]int64{}
	rows, err := r.pool.Query(ctx, `SELECT status, count(*) FROM message_jobs GROUP BY status`)
	if err != nil {
		return nil, fmt.Errorf("observability: job stats: %w", err)
	}
	for rows.Next() {
		var status string
		var count int64
		if err := rows.Scan(&status, &count); err != nil {
			rows.Close()
			return nil, fmt.Errorf("observability: scan job stats: %w", err)
		}
		jobs[status] = count
	}
	rows.Close()
	out["message_jobs"] = jobs

	type reasonCount struct {
		Decision string `json:"decision"`
		Reason   string `json:"reason_code"`
		Count    int64  `json:"count"`
	}
	decisions := []reasonCount{}
	rows, err = r.pool.Query(ctx, `
		SELECT decision, reason_code, count(*) FROM intent_decisions
		WHERE created_at > now() - interval '24 hours'
		GROUP BY decision, reason_code ORDER BY count DESC
	`)
	if err != nil {
		return nil, fmt.Errorf("observability: decision stats: %w", err)
	}
	for rows.Next() {
		var rc reasonCount
		if err := rows.Scan(&rc.Decision, &rc.Reason, &rc.Count); err != nil {
			rows.Close()
			return nil, fmt.Errorf("observability: scan decision stats: %w", err)
		}
		decisions = append(decisions, rc)
	}
	rows.Close()
	out["intent_decisions_24h"] = decisions

	var messages24h int64
	if err := r.pool.QueryRow(ctx,
		`SELECT count(*) FROM messages WHERE created_at > now() - interval '24 hours'`,
	).Scan(&messages24h); err != nil {
		return nil, fmt.Errorf("observability: message stats: %w", err)
	}
	out["messages_24h"] = messages24h

	memories := map[string]int64{}
	rows, err = r.pool.Query(ctx, `SELECT status, count(*) FROM memories GROUP BY status`)
	if err != nil {
		return nil, fmt.Errorf("observability: memory stats: %w", err)
	}
	for rows.Next() {
		var status string
		var count int64
		if err := rows.Scan(&status, &count); err != nil {
			rows.Close()
			return nil, fmt.Errorf("observability: scan memory stats: %w", err)
		}
		memories[status] = count
	}
	rows.Close()
	out["memories"] = memories

	return out, nil
}
