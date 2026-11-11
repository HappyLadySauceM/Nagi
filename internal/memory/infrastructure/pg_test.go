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

// TestMemorySearchVisibleMatchesChineseKeywords verifies the minimal Chinese
// keyword search used by reply recall: multi-token queries match stored text,
// ASCII words stay searchable, and another group's memory never leaks.
// TestMemorySearchVisibleMatchesChineseKeywords 验证回复召回使用的最小学中文关键词检索：
// 多词查询能命中存储文本、ASCII 词仍可搜索，且其他群的记忆不会泄漏。
func TestMemorySearchVisibleMatchesChineseKeywords(t *testing.T) {
	pool := dbtest.Open(t)
	repo, err := NewRepo(pool)
	if err != nil {
		t.Fatalf("new repo: %v", err)
	}
	ctx := context.Background()
	const botID = 992002

	if _, err := pool.Exec(ctx,
		`INSERT INTO bot_accounts (bot_id, nickname) VALUES ($1, 'memory-keyword-test') ON CONFLICT (bot_id) DO NOTHING`,
		botID,
	); err != nil {
		t.Fatalf("seed bot account: %v", err)
	}
	if _, err := pool.Exec(ctx, `DELETE FROM memories WHERE bot_id = $1`, botID); err != nil {
		t.Fatalf("clean previous memories: %v", err)
	}

	seed := []domain.Memory{
		{BotID: botID, Visibility: domain.VisibilityGroup, OwnerKey: "700", Content: "群友喜欢晚上聊天", Status: domain.StatusActive, Confidence: 0.8},
		{BotID: botID, Visibility: domain.VisibilityGroup, OwnerKey: "700", Content: "项目使用 Go 后端", Status: domain.StatusActive, Confidence: 0.7},
		{BotID: botID, Visibility: domain.VisibilityGroup, OwnerKey: "701", Content: "其他群的秘密", Status: domain.StatusActive, Confidence: 0.9},
	}
	for _, item := range seed {
		if _, err := repo.Insert(ctx, item); err != nil {
			t.Fatalf("insert memory: %v", err)
		}
	}

	got, err := repo.SearchVisible(ctx, domain.SearchRequest{
		BotID: botID, Kind: "group", TargetID: 700, Query: "晚上 聊天", Limit: 5,
	})
	if err != nil {
		t.Fatalf("search Chinese keywords: %v", err)
	}
	if len(got) != 1 || got[0].Content != "群友喜欢晚上聊天" {
		t.Fatalf("Chinese keyword matches = %+v", got)
	}

	got, err = repo.SearchVisible(ctx, domain.SearchRequest{
		BotID: botID, Kind: "group", TargetID: 700, Query: "Go", Limit: 5,
	})
	if err != nil {
		t.Fatalf("search ASCII keyword: %v", err)
	}
	if len(got) != 1 || got[0].Content != "项目使用 Go 后端" {
		t.Fatalf("ASCII keyword matches = %+v", got)
	}

	other, err := repo.SearchVisible(ctx, domain.SearchRequest{
		BotID: botID, Kind: "group", TargetID: 701, Query: "秘密", Limit: 5,
	})
	if err != nil {
		t.Fatalf("search other group: %v", err)
	}
	if len(other) != 1 || other[0].Content != "其他群的秘密" {
		t.Fatalf("other group matches = %+v", other)
	}

	const otherBotID = 992003
	if _, err := pool.Exec(ctx,
		`INSERT INTO bot_accounts (bot_id, nickname) VALUES ($1, 'memory-other-bot') ON CONFLICT (bot_id) DO NOTHING`,
		otherBotID,
	); err != nil {
		t.Fatalf("seed other bot: %v", err)
	}
	if _, err := pool.Exec(ctx, `DELETE FROM memories WHERE bot_id = $1`, otherBotID); err != nil {
		t.Fatalf("clean other bot memories: %v", err)
	}
	if _, err := repo.Insert(ctx, domain.Memory{
		BotID: otherBotID, Visibility: domain.VisibilityBotShared,
		Content: "另一个账号的共享知识", Status: domain.StatusActive, Confidence: 0.99,
	}); err != nil {
		t.Fatalf("insert other bot memory: %v", err)
	}
	crossBot, err := repo.SearchVisible(ctx, domain.SearchRequest{
		BotID: botID, Kind: "group", TargetID: 700, Query: "共享知识", Limit: 5,
	})
	if err != nil {
		t.Fatalf("search cross bot: %v", err)
	}
	if len(crossBot) != 0 {
		t.Fatalf("cross-bot memory leaked: %+v", crossBot)
	}
}
