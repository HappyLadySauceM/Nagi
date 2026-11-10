package infrastructure

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/HappyLadySauceM/Nagi/pkg/db/dbtest"
)

// TestListUnregisteredMessagesSkipsQueuedAndUnauthorized verifies the scan
// skips messages that already have a job and messages with an unauthorized
// authorization event.
// TestListUnregisteredMessagesSkipsQueuedAndUnauthorized 验证扫描跳过已有任务的消息，
// 并排除授权事件为 unauthorized 的消息。
func TestListUnregisteredMessagesSkipsQueuedAndUnauthorized(t *testing.T) {
	pool := dbtest.Open(t)
	repo, err := NewRepo(pool)
	if err != nil {
		t.Fatalf("new repo: %v", err)
	}
	ctx := context.Background()
	const botID = 996001
	const groupID = 996002
	if _, err := pool.Exec(ctx, `INSERT INTO bot_accounts (bot_id, nickname) VALUES ($1, 'scan-test') ON CONFLICT DO NOTHING`, botID); err != nil {
		t.Fatalf("seed bot: %v", err)
	}
	conversationID := uuid.New()
	if _, err := pool.Exec(ctx, `INSERT INTO conversations (id, bot_id, kind, target_id, last_message_at) VALUES ($1,$2,'group',$3,now())`, conversationID, botID, groupID); err != nil {
		t.Fatalf("seed conversation: %v", err)
	}
	var lowEventID, queuedEventID, disabledEventID int64
	for _, event := range []struct {
		target *int64
		auth   bool
	}{{&lowEventID, true}, {&queuedEventID, true}, {&disabledEventID, false}} {
		if err := pool.QueryRow(ctx, `INSERT INTO scope_memory_authorization_events (bot_id, scope_type, scope_id, event_kind, authorized) VALUES ($1,'group',$2,'bootstrap',$3) RETURNING id`, botID, groupID, event.auth).Scan(event.target); err != nil {
			t.Fatalf("seed event: %v", err)
		}
	}
	var lowID, queuedID, disabledID int64
	if err := pool.QueryRow(ctx, `INSERT INTO messages (conversation_id, bot_id, direction, platform_message_id, sender_id, text_content, segments, memory_authorization_event_id) VALUES ($1,$2,'incoming',9961001,996003,'low','[]'::jsonb,$3) RETURNING id`, conversationID, botID, lowEventID).Scan(&lowID); err != nil {
		t.Fatalf("seed low message: %v", err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO messages (conversation_id, bot_id, direction, platform_message_id, sender_id, text_content, segments, memory_authorization_event_id) VALUES ($1,$2,'incoming',9961002,996003,'queued','[]'::jsonb,$3) RETURNING id`, conversationID, botID, queuedEventID).Scan(&queuedID); err != nil {
		t.Fatalf("seed queued message: %v", err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO messages (conversation_id, bot_id, direction, platform_message_id, sender_id, text_content, segments, memory_authorization_event_id) VALUES ($1,$2,'incoming',9961003,996003,'disabled','[]'::jsonb,$3) RETURNING id`, conversationID, botID, disabledEventID).Scan(&disabledID); err != nil {
		t.Fatalf("seed disabled message: %v", err)
	}
	_ = disabledID
	if _, err := pool.Exec(ctx, `INSERT INTO memory_extraction_jobs (id, bot_id, conversation_id, scope_type, scope_id, message_id, authorization_event_id, status) VALUES ($1,$2,$3,'group',$4,$5,$6,'pending')`, uuid.New(), botID, conversationID, groupID, queuedID, queuedEventID); err != nil {
		t.Fatalf("seed job: %v", err)
	}
	defer func() {
		_, _ = pool.Exec(ctx, `DELETE FROM memory_extraction_jobs WHERE bot_id=$1`, botID)
		_, _ = pool.Exec(ctx, `DELETE FROM messages WHERE bot_id=$1`, botID)
		_, _ = pool.Exec(ctx, `DELETE FROM conversations WHERE bot_id=$1`, botID)
		_, _ = pool.Exec(ctx, `DELETE FROM scope_memory_authorization_events WHERE bot_id=$1`, botID)
	}()

	items, err := repo.ListUnregisteredMessages(ctx, botID, conversationID.String(), 10)
	if err != nil {
		t.Fatalf("list unregistered: %v", err)
	}
	if len(items) != 1 || items[0].ID != lowID || items[0].AuthorizationEventID != lowEventID {
		t.Fatalf("unregistered items = %+v, want low id %d event %d", items, lowID, lowEventID)
	}
}

// TestListUnregisteredMessagesIncludesTemplateAuthorization verifies that a
// message stamped with a template authorization event (scope_id=0) is found
// when the conversation has no independent scope policy.
// TestListUnregisteredMessagesIncludesTemplateAuthorization 验证仅继承模板策略、
// 没有独立策略的会话中，带模板授权事件（scope_id=0）的消息能被补扫发现。
func TestListUnregisteredMessagesIncludesTemplateAuthorization(t *testing.T) {
	pool := dbtest.Open(t)
	repo, err := NewRepo(pool)
	if err != nil {
		t.Fatalf("new repo: %v", err)
	}
	ctx := context.Background()
	const botID = 996101
	const groupID = 996102
	if _, err := pool.Exec(ctx, `INSERT INTO bot_accounts (bot_id, nickname) VALUES ($1, 'scan-template-test') ON CONFLICT DO NOTHING`, botID); err != nil {
		t.Fatalf("seed bot: %v", err)
	}
	conversationID := uuid.New()
	if _, err := pool.Exec(ctx, `INSERT INTO conversations (id, bot_id, kind, target_id, last_message_at) VALUES ($1,$2,'group',$3,now())`, conversationID, botID, groupID); err != nil {
		t.Fatalf("seed conversation: %v", err)
	}
	var eventID int64
	if err := pool.QueryRow(ctx, `INSERT INTO scope_memory_authorization_events (bot_id, scope_type, scope_id, event_kind, authorized) VALUES ($1,'group',0,'created',true) RETURNING id`, botID).Scan(&eventID); err != nil {
		t.Fatalf("seed template event: %v", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO scope_policies (bot_id, scope_type, scope_id, enabled, store_messages, allow_memory_write, memory_authorization_event_id) VALUES ($1,'group',0,true,true,true,$2)`, botID, eventID); err != nil {
		t.Fatalf("seed template policy: %v", err)
	}
	var independentCount int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM scope_policies WHERE bot_id=$1 AND scope_type='group' AND scope_id=$2`, botID, groupID).Scan(&independentCount); err != nil {
		t.Fatalf("count independent policies: %v", err)
	}
	if independentCount != 0 {
		t.Fatalf("independent policy count = %d, want 0", independentCount)
	}
	var messageID int64
	if err := pool.QueryRow(ctx, `INSERT INTO messages (conversation_id, bot_id, direction, platform_message_id, sender_id, text_content, segments, memory_authorization_event_id) VALUES ($1,$2,'incoming',9961101,996103,'template','[]'::jsonb,$3) RETURNING id`, conversationID, botID, eventID).Scan(&messageID); err != nil {
		t.Fatalf("seed template message: %v", err)
	}
	defer func() {
		_, _ = pool.Exec(ctx, `DELETE FROM memory_extraction_jobs WHERE bot_id=$1`, botID)
		_, _ = pool.Exec(ctx, `DELETE FROM messages WHERE bot_id=$1`, botID)
		_, _ = pool.Exec(ctx, `DELETE FROM conversations WHERE bot_id=$1`, botID)
		_, _ = pool.Exec(ctx, `DELETE FROM scope_policies WHERE bot_id=$1`, botID)
		_, _ = pool.Exec(ctx, `DELETE FROM scope_memory_authorization_events WHERE bot_id=$1`, botID)
	}()

	items, err := repo.ListUnregisteredMessages(ctx, botID, conversationID.String(), 10)
	if err != nil {
		t.Fatalf("list unregistered: %v", err)
	}
	if len(items) != 1 || items[0].ID != messageID || items[0].AuthorizationEventID != eventID {
		t.Fatalf("unregistered items = %+v, want template message %d event %d", items, messageID, eventID)
	}
}

// TestListUnregisteredMessagesFindsLateCommitWithoutWatermark verifies a
// message that commits after a prior scan, with a lower id and an old
// created_at, remains visible to a later scan because NOT EXISTS has no time
// or id lower bound.
// TestListUnregisteredMessagesFindsLateCommitWithoutWatermark 验证低 id、旧 created_at
// 的消息在先前扫描后才提交时，因 NOT EXISTS 不设时间或 id 下界仍能被后续扫描发现。
func TestListUnregisteredMessagesFindsLateCommitWithoutWatermark(t *testing.T) {
	pool := dbtest.Open(t)
	repo, err := NewRepo(pool)
	if err != nil {
		t.Fatalf("new repo: %v", err)
	}
	ctx := context.Background()
	const botID = 996201
	const groupID = 996202
	if _, err := pool.Exec(ctx, `INSERT INTO bot_accounts (bot_id, nickname) VALUES ($1, 'scan-late-commit-test') ON CONFLICT DO NOTHING`, botID); err != nil {
		t.Fatalf("seed bot: %v", err)
	}
	conversationID := uuid.New()
	if _, err := pool.Exec(ctx, `INSERT INTO conversations (id, bot_id, kind, target_id, last_message_at) VALUES ($1,$2,'group',$3,now())`, conversationID, botID, groupID); err != nil {
		t.Fatalf("seed conversation: %v", err)
	}
	var eventID int64
	if err := pool.QueryRow(ctx, `INSERT INTO scope_memory_authorization_events (bot_id, scope_type, scope_id, event_kind, authorized) VALUES ($1,'group',$2,'bootstrap',true) RETURNING id`, botID, groupID).Scan(&eventID); err != nil {
		t.Fatalf("seed event: %v", err)
	}
	defer func() {
		_, _ = pool.Exec(ctx, `DELETE FROM memory_extraction_jobs WHERE bot_id=$1`, botID)
		_, _ = pool.Exec(ctx, `DELETE FROM messages WHERE bot_id=$1`, botID)
		_, _ = pool.Exec(ctx, `DELETE FROM conversations WHERE bot_id=$1`, botID)
		_, _ = pool.Exec(ctx, `DELETE FROM scope_memory_authorization_events WHERE bot_id=$1`, botID)
	}()

	lateTx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin late transaction: %v", err)
	}
	defer lateTx.Rollback(ctx)
	var lateID int64
	if err := lateTx.QueryRow(ctx, `INSERT INTO messages (conversation_id, bot_id, direction, platform_message_id, sender_id, text_content, segments, memory_authorization_event_id, created_at) VALUES ($1,$2,'incoming',9962101,996203,'late','[]'::jsonb,$3, now() - interval '2 hours') RETURNING id`, conversationID, botID, eventID).Scan(&lateID); err != nil {
		t.Fatalf("seed late message: %v", err)
	}
	var earlyID int64
	if err := pool.QueryRow(ctx, `INSERT INTO messages (conversation_id, bot_id, direction, platform_message_id, sender_id, text_content, segments, memory_authorization_event_id) VALUES ($1,$2,'incoming',9962102,996203,'early','[]'::jsonb,$3) RETURNING id`, conversationID, botID, eventID).Scan(&earlyID); err != nil {
		t.Fatalf("seed early message: %v", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO memory_extraction_jobs (id, bot_id, conversation_id, scope_type, scope_id, message_id, authorization_event_id, status) VALUES ($1,$2,$3,'group',$4,$5,$6,'pending')`, uuid.New(), botID, conversationID, groupID, earlyID, eventID); err != nil {
		t.Fatalf("seed early job: %v", err)
	}

	items, err := repo.ListUnregisteredMessages(ctx, botID, conversationID.String(), 10)
	if err != nil {
		t.Fatalf("scan before late commit: %v", err)
	}
	if len(items) != 0 {
		t.Fatalf("scan before late commit returned %+v, want no unregistered items", items)
	}
	if err := lateTx.Commit(ctx); err != nil {
		t.Fatalf("commit late message: %v", err)
	}
	items, err = repo.ListUnregisteredMessages(ctx, botID, conversationID.String(), 10)
	if err != nil {
		t.Fatalf("scan after late commit: %v", err)
	}
	if len(items) != 1 || items[0].ID != lateID || items[0].AuthorizationEventID != eventID {
		t.Fatalf("scan after late commit = %+v, want late message %d event %d", items, lateID, eventID)
	}
}
