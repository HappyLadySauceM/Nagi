// Integration test for the memory repository, gated by NAGI_TEST_DSN.
// 记忆仓库的集成测试，以 NAGI_TEST_DSN 为门控。
package infrastructure

import (
	"context"
	"errors"
	"testing"
	"time"

	memoryapp "github.com/HappyLadySauceM/Nagi/internal/memory/application"
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
	if _, err := pool.Exec(ctx, `DELETE FROM memories WHERE bot_id=$1`, botID); err != nil {
		t.Fatalf("clean previous memories: %v", err)
	}

	shared := domain.Memory{BotID: botID, Visibility: domain.VisibilityBotShared,
		Content: "机器人共享知识：测试用", Status: domain.StatusActive, ApprovedBy: "admin", Confidence: 0.9}
	id, err := repo.Insert(ctx, shared)
	if err != nil {
		t.Fatalf("insert: %v", err)
	}

	dup, found, err := repo.FindSame(ctx, botID, domain.VisibilityBotShared, "", "机器人共享知识：测试用")
	if err != nil || !found || dup.ID != id {
		t.Fatalf("find same: found=%v id=%s err=%v", found, dup.ID, err)
	}

	got, err := repo.SearchVisible(ctx, domain.SearchRequest{BotID: botID, Kind: "group", TargetID: 700, Query: "共享", Limit: 5})
	if err != nil || len(got) != 0 {
		t.Fatalf("search visible: got=%+v err=%v", got, err)
	}

	// Physical deletion removes the row from direct reads, search and listing.
	// 物理删除会让按 ID 读取、检索和列表都不再命中。
	deleted, err := repo.Delete(ctx, id)
	if err != nil || !deleted {
		t.Fatalf("delete: deleted=%v err=%v", deleted, err)
	}
	if _, found, err := repo.Get(ctx, id); err != nil || found {
		t.Fatalf("get after delete: found=%v err=%v", found, err)
	}
	if missingDeleted, err := repo.Delete(ctx, "11111111-1111-4111-8111-111111111111"); err != nil || missingDeleted {
		t.Fatalf("delete missing: deleted=%v err=%v", missingDeleted, err)
	}
	got, err = repo.SearchVisible(ctx, domain.SearchRequest{BotID: botID, Kind: "group", TargetID: 700, Limit: 5})
	if err != nil || len(got) != 0 {
		t.Fatalf("after delete: got=%d err=%v", len(got), err)
	}
	botIDValue := int64(botID)
	items, total, err := repo.List(ctx, domain.ListFilter{BotID: &botIDValue, Page: 1, Limit: 10})
	if err != nil || total != 0 || len(items) != 0 {
		t.Fatalf("list after delete: total=%d items=%d err=%v", total, len(items), err)
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

func TestHAP52HistoricalSharedCannotBeRebound(t *testing.T) {
	ctx := context.Background()
	pool := dbtest.Open(t)
	repo, err := NewRepo(pool)
	if err != nil {
		t.Fatal(err)
	}
	service, err := memoryapp.NewService(repo)
	if err != nil {
		t.Fatal(err)
	}
	const bot, otherBot, group, friend int64 = 982521, 982522, 982525, 982526
	if _, err := pool.Exec(ctx, `INSERT INTO bot_accounts(bot_id) VALUES($1),($2)`, bot, otherBot); err != nil {
		t.Fatal(err)
	}
	source := func(account int64, kind string, target, sender int64) (string, int64) {
		t.Helper()
		var conversation string
		if err := pool.QueryRow(ctx, `INSERT INTO conversations(id,bot_id,kind,target_id) VALUES(gen_random_uuid(),$1,$2,$3) ON CONFLICT(bot_id,kind,target_id) DO UPDATE SET target_id=excluded.target_id RETURNING id::text`, account, kind, target).Scan(&conversation); err != nil {
			t.Fatal(err)
		}
		var msg int64
		if err := pool.QueryRow(ctx, `INSERT INTO messages(conversation_id,bot_id,direction,sender_id) VALUES($1,$2,'incoming',$3) RETURNING id`, conversation, account, sender).Scan(&msg); err != nil {
			t.Fatal(err)
		}
		return conversation, msg
	}
	original, groupSource := source(bot, "group", group, friend)
	private, privateSource := source(bot, "private", friend, friend)
	_, wrongSender := source(bot, "private", friend, friend+1)
	_, wrongAccount := source(otherBot, "group", group, friend)
	for _, tc := range []struct {
		name, kind, conversation string
		target, source, sender   int64
		visible                  bool
	}{
		{"original private", "private", private, friend, privateSource, friend, true},
		{"mismatched private sender", "private", private, friend, wrongSender, friend + 1, false},
		{"mismatched stored sender", "group", original, group, groupSource, friend + 1, false},
		{"wrong account", "group", original, group, wrongAccount, friend, false},
		{"unknown source", "group", original, group, 0, 0, false},
		{"original group", "group", original, group, groupSource, friend, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			current := domain.Memory{BotID: bot, Visibility: domain.VisibilityBotShared, Content: "synthetic legacy scope", Status: domain.StatusActive, ApprovedBy: "fixture", SourceMessageID: tc.source, SourceSenderID: tc.sender}
			id, err := repo.Insert(ctx, current)
			if err != nil {
				t.Fatal(err)
			}
			scope := domain.SearchRequest{BotID: bot, Kind: tc.kind, TargetID: tc.target, ConversationID: tc.conversation}
			if _, found, err := repo.ReadVisible(ctx, scope, id); err != nil || found != tc.visible {
				t.Errorf("original visibility=%v, err=%v, want=%v", found, err, tc.visible)
			}
			for _, patch := range []struct {
				visibility domain.Visibility
				owner      string
			}{
				{domain.VisibilityGroup, "982527"}, {domain.VisibilityPrivateUser, "982526"}, {domain.VisibilityConversation, tc.conversation},
			} {
				if err := service.Revise(ctx, id, "modified", patch.visibility, &patch.owner); !errors.Is(err, memoryapp.ErrMemoryInvalid) {
					t.Errorf("revise %s: %v", patch.visibility, err)
				}
			}
			proposed := current
			proposed.ID, proposed.Visibility, proposed.OwnerKey = id, domain.VisibilityConversation, tc.conversation
			proposed.SourceMessageID, proposed.SourceSenderID = groupSource, friend
			proposed.SubjectType, proposed.SubjectID = domain.SubjectUser, "999"
			if err := repo.Update(ctx, proposed); !errors.Is(err, memoryapp.ErrMemoryInvalid) {
				t.Errorf("direct update with changed source: %v", err)
			}
			if err := repo.UpdateMemoryLocked(ctx, proposed); !errors.Is(err, memoryapp.ErrMemoryInvalid) {
				t.Errorf("stale edit with changed source: %v", err)
			}
			if row, _, found, err := service.Management(ctx, id, bot); err != nil || !found || row.Visibility != current.Visibility || row.SourceMessageID != current.SourceMessageID || row.SourceSenderID != current.SourceSenderID || row.Content != current.Content {
				t.Fatalf("management row changed: found=%v err=%v row=%+v", found, err, row)
			}
			current.Status, current.ApprovedBy = domain.StatusCandidate, ""
			candidate, err := repo.Insert(ctx, current)
			if err != nil {
				t.Fatal(err)
			}
			if err := service.Approve(ctx, candidate, "fixture"); !errors.Is(err, memoryapp.ErrMemoryInvalid) {
				t.Errorf("approve historical candidate: %v", err)
			}
			if err := service.ApproveWithEdit(ctx, candidate, "fixture", "modified", domain.VisibilityConversation, &tc.conversation); !errors.Is(err, memoryapp.ErrMemoryInvalid) {
				t.Errorf("approve and rebind: %v", err)
			}
			if row, found, err := repo.Get(ctx, candidate); err != nil || !found || row.Status != domain.StatusCandidate || row.Visibility != domain.VisibilityBotShared {
				t.Fatalf("candidate changed: found=%v err=%v row=%+v", found, err, row)
			}
			if tc.visible {
				if _, err := pool.Exec(ctx, `DELETE FROM messages WHERE id=$1`, tc.source); err != nil {
					t.Fatal(err)
				}
				if _, found, err := repo.ReadVisible(ctx, scope, id); err != nil || found {
					t.Errorf("deleted source still readable: found=%v err=%v", found, err)
				}
			}
		})
	}
}
