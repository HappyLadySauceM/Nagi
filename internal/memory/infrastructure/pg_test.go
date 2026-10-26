// Integration test for the memory repository, gated by NAGI_TEST_DSN.
// 记忆仓库的集成测试，以 NAGI_TEST_DSN 为门控。
package infrastructure

import (
	"context"
	"testing"
	"time"

	"github.com/HappyLadySauceM/Nagi/internal/memory/domain"
	"github.com/HappyLadySauceM/Nagi/pkg/db/dbtest"
)

// TestMemoryRepositoryRoundTrip verifies insert, dedupe lookup, approve, search
// visibility and deletion against a real PostgreSQL.
// TestMemoryRepositoryRoundTrip 针对真实 PostgreSQL 验证插入、去重查找、审批、
// 检索可见性与删除。
func TestMemoryRepositoryRoundTrip(t *testing.T) {
	pool := dbtest.Open(t)
	repo, err := NewRepo(pool)
	if err != nil {
		t.Fatalf("new repo: %v", err)
	}
	ctx := context.Background()
	const botID = 992001

	// Seed the bot account to satisfy the foreign key.
	// 先写入机器人账号以满足外键。
	if _, err := pool.Exec(ctx,
		`INSERT INTO bot_accounts (bot_id, nickname) VALUES ($1, 'memory-test') ON CONFLICT (bot_id) DO NOTHING`,
		botID,
	); err != nil {
		t.Fatalf("seed bot account: %v", err)
	}

	shared := domain.Memory{BotID: botID, Visibility: domain.VisibilityBotShared,
		Content: "机器人共享知识：测试用", Status: domain.StatusCandidate, Confidence: 0.9}
	id, err := repo.Insert(ctx, shared)
	if err != nil {
		t.Fatalf("insert: %v", err)
	}

	dup, found, err := repo.FindSame(ctx, botID, domain.VisibilityBotShared, "", "机器人共享知识：测试用")
	if err != nil || !found || dup.ID != id {
		t.Fatalf("find same: found=%v id=%s err=%v", found, dup.ID, err)
	}

	shared.ID = id
	shared.Status = domain.StatusActive
	shared.ApprovedBy = "admin"
	if err := repo.Update(ctx, shared); err != nil {
		t.Fatalf("update: %v", err)
	}

	got, err := repo.SearchVisible(ctx, domain.SearchRequest{BotID: botID, Kind: "group", TargetID: 700, Query: "共享", Limit: 5})
	if err != nil || len(got) != 1 || got[0].ID != id {
		t.Fatalf("search visible: got=%+v err=%v", got, err)
	}

	// Deleted memories leave the retrieval set.
	// 已删除记忆离开检索集合。
	if err := repo.SoftDelete(ctx, id); err != nil {
		t.Fatalf("soft delete: %v", err)
	}
	got, err = repo.SearchVisible(ctx, domain.SearchRequest{BotID: botID, Kind: "group", TargetID: 700, Limit: 5})
	if err != nil || len(got) != 0 {
		t.Fatalf("after delete: got=%d err=%v", len(got), err)
	}

	// Expired active memories are invisible too.
	// 已过期的激活记忆同样不可见。
	expired := domain.Memory{BotID: botID, Visibility: domain.VisibilityGroup, OwnerKey: "700",
		Content: "过期记忆", Status: domain.StatusActive, ExpiresAt: time.Now().Add(-time.Hour)}
	if _, err := repo.Insert(ctx, expired); err != nil {
		t.Fatalf("insert expired: %v", err)
	}
	got, err = repo.SearchVisible(ctx, domain.SearchRequest{BotID: botID, Kind: "group", TargetID: 700, Limit: 5})
	if err != nil || len(got) != 0 {
		t.Fatalf("expired memory visible: got=%d err=%v", len(got), err)
	}
}
