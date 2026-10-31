package infrastructure

import (
	"context"
	"fmt"
	"github.com/HappyLadySauceM/Nagi/pkg/db"
	"github.com/HappyLadySauceM/Nagi/pkg/db/ent"
	d "github.com/HappyLadySauceM/Nagi/pkg/db/ent/intentdecision"
	mem "github.com/HappyLadySauceM/Nagi/pkg/db/ent/memory"
	m "github.com/HappyLadySauceM/Nagi/pkg/db/ent/message"
	j "github.com/HappyLadySauceM/Nagi/pkg/db/ent/messagejob"
	"time"
)

type StatsRepo struct{ pool *db.Pool }

func NewStatsRepo(p *db.Pool) (*StatsRepo, error) {
	if p == nil {
		return nil, fmt.Errorf("observability: pool required")
	}
	return &StatsRepo{p}, nil
}
func (r *StatsRepo) Stats(ctx context.Context, bot int64) (map[string]any, error) {
	out := map[string]any{}
	jobs := r.pool.Client.MessageJob.Query()
	decisions := r.pool.Client.IntentDecision.Query().Where(d.CreatedAtGT(time.Now().Add(-24 * time.Hour)))
	messages := r.pool.Client.Message.Query().Where(m.CreatedAtGT(time.Now().Add(-24 * time.Hour)))
	memories := r.pool.Client.Memory.Query()
	if bot > 0 {
		jobs.Where(j.HasIncomingMessageIDRefWith(m.BotID(bot)))
		decisions.Where(d.HasJobIDRefWith(j.HasIncomingMessageIDRefWith(m.BotID(bot))))
		messages.Where(m.BotID(bot))
		memories.Where(mem.BotID(bot))
	}
	var jobRows []struct {
		Status string
		Count  int64
	}
	if err := jobs.GroupBy(j.FieldStatus).Aggregate(ent.Count()).Scan(ctx, &jobRows); err != nil {
		return nil, err
	}
	jobCounts := map[string]int64{}
	for _, v := range jobRows {
		jobCounts[v.Status] = v.Count
	}
	out["message_jobs"] = jobCounts
	type reasonCount struct {
		Decision string `json:"decision"`
		Reason   string `json:"reason_code"`
		Count    int64  `json:"count"`
	}
	reasonRows := []reasonCount{}
	if err := decisions.GroupBy(d.FieldDecision, d.FieldReasonCode).Aggregate(ent.Count()).Scan(ctx, &reasonRows); err != nil {
		return nil, err
	}
	out["intent_decisions_24h"] = reasonRows
	count, err := messages.Count(ctx)
	if err != nil {
		return nil, err
	}
	out["messages_24h"] = int64(count)
	var memoryRows []struct {
		Status string
		Count  int64
	}
	if err := memories.GroupBy(mem.FieldStatus).Aggregate(ent.Count()).Scan(ctx, &memoryRows); err != nil {
		return nil, err
	}
	memoryCounts := map[string]int64{}
	for _, v := range memoryRows {
		memoryCounts[v.Status] = v.Count
	}
	out["memories"] = memoryCounts
	return out, nil
}
