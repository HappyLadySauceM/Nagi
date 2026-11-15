package infrastructure

import (
	"context"

	"github.com/HappyLadySauceM/Nagi/internal/memory/domain"
)

// MemorySummary counts the current non-deleted memory rows of one bot by state
// and visibility. Physical deletion has already removed rows, so no historical
// deleted total is fabricated here.
// MemorySummary 按状态与可见性统计单个机器人当前未被物理删除的记忆行；
// 物理删除的行已不存在，这里不伪造历史删除总数。
func (r *Repo) MemorySummary(ctx context.Context, botID int64) (domain.MemorySummary, error) {
	var out domain.MemorySummary
	err := r.pool.QueryRow(ctx, `
		SELECT
			COUNT(*) FILTER (WHERE status IN ('candidate','active','superseded')),
			COUNT(*) FILTER (WHERE status = 'active'),
			COUNT(*) FILTER (WHERE status = 'candidate'),
			COUNT(*) FILTER (WHERE status = 'superseded')
		FROM memories
		WHERE bot_id = $1`, botID).Scan(&out.Total, &out.Active, &out.Candidate, &out.Superseded)
	if err != nil {
		return domain.MemorySummary{}, err
	}
	out.ByVisibility = map[domain.Visibility]int64{
		domain.VisibilityBotShared:    0,
		domain.VisibilityGroup:        0,
		domain.VisibilityPrivateUser:  0,
		domain.VisibilityConversation: 0,
	}
	rows, err := r.pool.Query(ctx, `
		SELECT visibility, COUNT(*)
		FROM memories
		WHERE bot_id = $1 AND status IN ('candidate','active','superseded')
		GROUP BY visibility`, botID)
	if err != nil {
		return domain.MemorySummary{}, err
	}
	defer rows.Close()
	for rows.Next() {
		var visibility string
		var count int64
		if err := rows.Scan(&visibility, &count); err != nil {
			return domain.MemorySummary{}, err
		}
		out.ByVisibility[domain.Visibility(visibility)] = count
	}
	if err := rows.Err(); err != nil {
		return domain.MemorySummary{}, err
	}
	return out, nil
}

// ExtractionSummary counts extraction jobs by active state and returns the
// latest failed job's stable error code for one bot.
// ExtractionSummary 统计单个机器人按状态区分的抽取任务，并返回最近失败任务的稳定错误码。
func (r *Repo) ExtractionSummary(ctx context.Context, botID int64) (domain.ExtractionSummary, error) {
	var out domain.ExtractionSummary
	err := r.pool.QueryRow(ctx, `
		SELECT
			COUNT(*) FILTER (WHERE status = 'pending'),
			COUNT(*) FILTER (WHERE status = 'processing'),
			COUNT(*) FILTER (WHERE status = 'failed'),
			COALESCE((
				SELECT last_error_code
				FROM memory_extraction_jobs
				WHERE bot_id = $1 AND status = 'failed' AND last_error_code IS NOT NULL AND last_error_code <> ''
				ORDER BY updated_at DESC, id DESC
				LIMIT 1
			), '')
		FROM memory_extraction_jobs
		WHERE bot_id = $1`, botID).Scan(&out.PendingJobs, &out.ProcessingJobs, &out.FailedJobs, &out.LastErrorCode)
	if err != nil {
		return domain.ExtractionSummary{}, err
	}
	return out, nil
}
