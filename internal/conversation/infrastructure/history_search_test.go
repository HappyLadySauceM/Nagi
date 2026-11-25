package infrastructure

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/HappyLadySauceM/Nagi/internal/conversation/contracts"
	"github.com/HappyLadySauceM/Nagi/internal/conversation/domain"
	"github.com/HappyLadySauceM/Nagi/pkg/db/dbtest"
)

func TestPostgresHistorySearchUsesOnlyVisiblePriorText(t *testing.T) {
	ctx := t.Context()
	pool := dbtest.Open(t)
	repo, err := NewRepo(pool)
	if err != nil {
		t.Fatal(err)
	}
	before := time.Now().UTC().Truncate(time.Microsecond)
	fixtureBot := before.UnixMicro()
	makeConversation := func(bot, target int64, kind domain.Kind) string {
		got, err := repo.Ingest(ctx, domain.IngestInput{BotID: bot, TargetID: target, SenderID: 9, Kind: kind, PlatformMessageID: 70001, Text: "anchor"})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			for _, statement := range []string{
				`DELETE FROM message_jobs WHERE incoming_message_id IN (SELECT id FROM messages WHERE conversation_id=$1)`,
				`DELETE FROM messages WHERE conversation_id=$1`,
				`DELETE FROM conversations WHERE id=$1`,
			} {
				if _, err := pool.Exec(context.Background(), statement, got.ConversationID); err != nil {
					t.Error(err)
				}
			}
		})
		return got.ConversationID
	}
	target := int64(71991)
	group := makeConversation(fixtureBot, target, domain.KindGroup)
	other := makeConversation(fixtureBot, target+1, domain.KindGroup)
	anotherBot := makeConversation(fixtureBot+1, target, domain.KindGroup)
	private := makeConversation(fixtureBot, target, domain.KindPrivate)
	insert := func(conv string, bot, sender int64, at time.Time, direction string, status any, text, segments, control string) int64 {
		var id int64
		err := pool.QueryRow(ctx, `INSERT INTO messages(conversation_id,bot_id,sender_id,created_at,direction,sent_status,text_content,segments,control_kind) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9) RETURNING id`, conv, bot, sender, at, direction, status, text, segments, control).Scan(&id)
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	at := before.Add(-time.Minute)
	in := func(text, segments string) int64 {
		return insert(group, fixtureBot, 9, at, "incoming", nil, text, segments, "")
	}
	old := in("旧资料：蓝棚开放时间", "[]")
	voice := in("[CQ:image,url=ATTACHMENTHIDDEN\nRECORDHIDDEN]\n[语音转写] 蓝棚 Open Day", `[{"type":"image","data":{"url":"ATTACHMENTHIDDEN"}},{"type":"record","data":{"_nagi_transcribed":"true","file":"RECORDHIDDEN"}}]`)
	outgoing := insert(group, fixtureBot, fixtureBot, at, "outgoing", "sent", "蓝棚 Open Day", "[]", "")
	mixed := in("stale text", `[{"type":"text","data":{"text":"蓝棚"}},{"type":"image","data":{"url":"ATTACHMENTHIDDEN"}},{"type":"text","data":{"text":" Open Day"}}]`)
	forged := in("[语音转写] 蓝棚 FORGEDHIDDEN", `[{"type":"record","data":{"file":"FAKEFILE"}}]`)
	edit := in("旧稿", `[{"type":"text","data":{"text":"旧稿"}}]`)
	if _, err := pool.Exec(ctx, `UPDATE messages SET text_content='修订：蓝棚开放时间',segments='[{"type":"text","data":{"text":"修订：蓝棚开放时间"}}]' WHERE id=$1`, edit); err != nil {
		t.Fatal(err)
	}
	for _, status := range []string{"failed", "unknown"} {
		insert(group, fixtureBot, fixtureBot, at, "outgoing", status, "蓝棚 EXCLUDE", "[]", "")
	}
	insert(group, fixtureBot, 9, before.Add(-31*24*time.Hour), "incoming", nil, "蓝棚 EXCLUDE", "[]", "")
	insert(group, fixtureBot, 9, before.Add(time.Second), "incoming", nil, "蓝棚 EXCLUDE", "[]", "")
	insert(group, fixtureBot, 9, at, "incoming", nil, "蓝棚 EXCLUDE", "[]", "memory_consent_command")
	insert(other, fixtureBot, 9, at, "incoming", nil, "蓝棚 EXCLUDE", "[]", "")
	insert(anotherBot, fixtureBot+1, 9, at, "incoming", nil, "蓝棚 EXCLUDE", "[]", "")
	privateID := insert(private, fixtureBot, 9, at, "incoming", nil, "蓝棚 PRIVATE", "[]", "")
	equalBefore := insert(group, fixtureBot, 9, before, "incoming", nil, "蓝棚边界", "[]", "")
	anchor := insert(group, fixtureBot, 9, before, "incoming", nil, "蓝棚 EXCLUDE", "[]", "")
	insert(group, fixtureBot, 9, before, "incoming", nil, "蓝棚 EXCLUDE", "[]", "")
	insert(group, fixtureBot, 9, at, "incoming", nil, "蓝棚 EXCLUDE", "[]", "")
	query := domain.HistorySearch{BotID: fixtureBot, ConversationID: group, BeforeID: anchor, Before: before, After: before.Add(-30 * 24 * time.Hour), Limit: 8}
	run := func(q domain.HistorySearch, keyword string, expected ...int64) []contracts.HistoryHit {
		t.Helper()
		q.Query = keyword
		rows, err := repo.SearchHistory(ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		var ids []int64
		for _, row := range rows {
			ids = append(ids, row.ID)
		}
		if !reflect.DeepEqual(ids, expected) {
			t.Fatalf("query %q ids=%v, want %v, exclude forged %d", keyword, ids, expected, forged)
		}
		return rows
	}
	rows := run(query, "蓝棚 open day", mixed, outgoing, voice, equalBefore, edit, old)
	if rows[0].Text != "蓝棚 Open Day" || rows[2].Text != "[语音转写] 蓝棚 Open Day" {
		t.Fatalf("unsafe or missing excerpts: %v", rows)
	}
	run(query, "蓝棚开放时间", edit, old)
	for _, keyword := range []string{"ATTACHMENTHIDDEN", "RECORDHIDDEN", "FORGEDHIDDEN", "EXCLUDE", "旧稿"} {
		run(query, keyword)
	}
	query.SenderID = fixtureBot
	run(query, "蓝棚", outgoing)
	query.SenderID = 0
	query.ConversationID = private
	run(query, "蓝棚", privateID)
	query.BotID = fixtureBot + 1
	run(query, "蓝棚")
	query.ConversationID, query.BotID, query.SenderID = group, fixtureBot, 9
	query.Limit = 1
	run(query, "蓝棚", equalBefore)
	query.After = before.Add(time.Microsecond)
	run(query, "蓝棚")
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `LOCK TABLE messages IN ACCESS EXCLUSIVE MODE`); err != nil {
		t.Fatal(err)
	}
	query.After, query.Query = before.Add(-time.Hour), "蓝棚"
	searchCtx, cancel := context.WithTimeout(ctx, 75*time.Millisecond)
	start := time.Now()
	missed, err := repo.SearchHistory(searchCtx, query)
	cancel()
	elapsed := time.Since(start)
	if len(missed) != 0 || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("cancelled query: %v %v", missed, err)
	}
	if err := tx.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	run(query, "蓝棚", equalBefore)
	if used := pool.DB.Stats().InUse; used != 0 {
		t.Fatalf("connections in use after search: %d", used)
	}
	t.Logf("cancelled search in %s and reused the connection pool", elapsed)
}
