package infrastructure

import (
	"context"
	"database/sql"
	"testing"

	"github.com/HappyLadySauceM/Nagi/internal/conversation/domain"
	scopedomain "github.com/HappyLadySauceM/Nagi/internal/scope/domain"
	scopeinfra "github.com/HappyLadySauceM/Nagi/internal/scope/infrastructure"
	"github.com/HappyLadySauceM/Nagi/pkg/db/dbtest"
)

// TestIngestStampsCurrentMemoryAuthorization verifies that message persistence
// records the generation read inside the same transaction, including a fresh
// generation after closing and reopening write authorization.
// TestIngestStampsCurrentMemoryAuthorization 验证消息持久化在同一事务内记录授权代际，
// 包括关闭再开启写授权后产生的新代际。
func TestIngestStampsCurrentMemoryAuthorization(t *testing.T) {
	pool := dbtest.Open(t)
	scopeRepo, err := scopeinfra.NewRepo(pool)
	if err != nil {
		t.Fatalf("new scope repo: %v", err)
	}
	repo, err := NewRepo(pool, WithMemoryAuthorizationStamper(scopeRepo))
	if err != nil {
		t.Fatalf("new conversation repo: %v", err)
	}
	ctx := context.Background()
	const botID = 993001
	const groupID = 993002
	if _, err := pool.Exec(ctx, `INSERT INTO bot_accounts (bot_id, nickname) VALUES ($1, 'ingest-auth') ON CONFLICT DO NOTHING`, botID); err != nil {
		t.Fatalf("seed bot: %v", err)
	}
	defer func() {
		_, _ = pool.Exec(ctx, `DELETE FROM message_jobs WHERE incoming_message_id IN (SELECT id FROM messages WHERE bot_id=$1)`, botID)
		_, _ = pool.Exec(ctx, `DELETE FROM messages WHERE bot_id=$1`, botID)
		_, _ = pool.Exec(ctx, `DELETE FROM conversations WHERE bot_id=$1`, botID)
		_, _ = pool.Exec(ctx, `DELETE FROM scope_policies WHERE bot_id=$1`, botID)
		_, _ = pool.Exec(ctx, `DELETE FROM scope_memory_authorization_events WHERE bot_id=$1`, botID)
	}()
	policy := scopedomain.Default(scopedomain.ScopeGroup, groupID)
	policy.Enabled = true
	policy.StoreMessages = true
	policy.AllowMemoryWrite = true
	if err := scopeRepo.Upsert(ctx, botID, policy); err != nil {
		t.Fatalf("seed policy: %v", err)
	}
	firstEvent, authorized, err := scopeRepo.ResolveMemoryAuthorization(ctx, botID, scopedomain.ScopeGroup, groupID)
	if err != nil || !authorized || firstEvent == 0 {
		t.Fatalf("initial event: event=%d authorized=%v err=%v", firstEvent, authorized, err)
	}

	first, err := repo.Ingest(ctx, domain.IngestInput{BotID: botID, Kind: domain.KindGroup, TargetID: groupID, PlatformMessageID: 9931001, SenderID: 993003, Text: "first"})
	if err != nil {
		t.Fatalf("ingest first: %v", err)
	}
	assertMessageAuthorizationEvent(t, pool, first.MessageID, firstEvent)

	policy.AllowMemoryWrite = false
	if err := scopeRepo.Upsert(ctx, botID, policy); err != nil {
		t.Fatalf("disable policy: %v", err)
	}
	closedEvent, authorized, err := scopeRepo.ResolveMemoryAuthorization(ctx, botID, scopedomain.ScopeGroup, groupID)
	if err != nil || authorized || closedEvent == firstEvent {
		t.Fatalf("closed event: event=%d authorized=%v err=%v first=%d", closedEvent, authorized, err, firstEvent)
	}
	second, err := repo.Ingest(ctx, domain.IngestInput{BotID: botID, Kind: domain.KindGroup, TargetID: groupID, PlatformMessageID: 9931002, SenderID: 993003, Text: "second"})
	if err != nil {
		t.Fatalf("ingest second: %v", err)
	}
	assertMessageAuthorizationEvent(t, pool, second.MessageID, 0)

	policy.AllowMemoryWrite = true
	if err := scopeRepo.Upsert(ctx, botID, policy); err != nil {
		t.Fatalf("reopen policy: %v", err)
	}
	reopenedEvent, authorized, err := scopeRepo.ResolveMemoryAuthorization(ctx, botID, scopedomain.ScopeGroup, groupID)
	if err != nil || !authorized || reopenedEvent == closedEvent || reopenedEvent == firstEvent {
		t.Fatalf("reopened event: event=%d authorized=%v err=%v closed=%d first=%d", reopenedEvent, authorized, err, closedEvent, firstEvent)
	}
	third, err := repo.Ingest(ctx, domain.IngestInput{BotID: botID, Kind: domain.KindGroup, TargetID: groupID, PlatformMessageID: 9931003, SenderID: 993003, Text: "third"})
	if err != nil {
		t.Fatalf("ingest third: %v", err)
	}
	assertMessageAuthorizationEvent(t, pool, third.MessageID, reopenedEvent)
}

func assertMessageAuthorizationEvent(t *testing.T, pool interface {
	QueryRow(context.Context, string, ...any) *sql.Row
}, messageID, want int64) {
	t.Helper()
	var got sql.NullInt64
	if err := pool.QueryRow(context.Background(), `SELECT memory_authorization_event_id FROM messages WHERE id=$1`, messageID).Scan(&got); err != nil {
		t.Fatalf("read message authorization event: %v", err)
	}
	if want == 0 {
		if got.Valid {
			t.Fatalf("message authorization event = %v, want unset", got)
		}
		return
	}
	if !got.Valid || got.Int64 != want {
		t.Fatalf("message authorization event = %v, want %d", got, want)
	}
}
