// Integration test for the stats repository, gated by QQBOT_TEST_DSN.
// 统计仓库的集成测试，以 QQBOT_TEST_DSN 为门控。
package infrastructure

import (
	"context"
	"testing"

	"github.com/HappyLadySauceM/QQBot/pkg/db/dbtest"
)

// TestStatsReturnsAggregateKeys verifies the stats snapshot keys exist.
// TestStatsReturnsAggregateKeys 验证统计快照包含必需的键。
func TestStatsReturnsAggregateKeys(t *testing.T) {
	pool := dbtest.Open(t)
	repo, err := NewStatsRepo(pool)
	if err != nil {
		t.Fatalf("new stats repo: %v", err)
	}
	stats, err := repo.Stats(context.Background(), 0)
	if err != nil {
		t.Fatalf("stats: %v", err)
	}
	for _, key := range []string{"message_jobs", "intent_decisions_24h", "messages_24h", "memories"} {
		if _, ok := stats[key]; !ok {
			t.Fatalf("stats missing key %q: %+v", key, stats)
		}
	}
}
