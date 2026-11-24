package infrastructure

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/HappyLadySauceM/Nagi/internal/memory/domain"
	scopedomain "github.com/HappyLadySauceM/Nagi/internal/scope/domain"
	scopeinfra "github.com/HappyLadySauceM/Nagi/internal/scope/infrastructure"
	"github.com/HappyLadySauceM/Nagi/pkg/db/dbtest"
	"github.com/google/uuid"
)

func TestIntentFactsQualifySourceBeforeLimit(t *testing.T) {
	pool := dbtest.Open(t)
	repo, err := NewRepo(pool)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	const bot, otherBot, group, otherGroup = 986531, 986532, 987001, 987002
	for _, id := range []int64{bot, otherBot} {
		if _, err := pool.Exec(ctx, `INSERT INTO bot_accounts(bot_id,nickname) VALUES($1,'intent-fixture')`, id); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() {
		for _, query := range []string{`DELETE FROM memories WHERE bot_id IN ($1,$2)`, `DELETE FROM messages WHERE bot_id IN ($1,$2)`, `DELETE FROM conversations WHERE bot_id IN ($1,$2)`, `DELETE FROM scope_policies WHERE bot_id IN ($1,$2)`, `DELETE FROM scope_memory_authorization_events WHERE bot_id IN ($1,$2)`, `DELETE FROM bot_accounts WHERE bot_id IN ($1,$2)`} {
			if _, err := pool.Exec(ctx, query, bot, otherBot); err != nil {
				t.Error(err)
			}
		}
	})
	source := func(owner int64, kind string, target int64, direction string) (string, int64) {
		conversation := uuid.NewString()
		if _, err := pool.Exec(ctx, `INSERT INTO conversations(id,bot_id,kind,target_id) VALUES($1,$2,$3,$4)`, conversation, owner, kind, target); err != nil {
			t.Fatal(err)
		}
		var message int64
		if err := pool.QueryRow(ctx, `INSERT INTO messages(conversation_id,bot_id,direction,sender_id,text_content) VALUES($1,$2,$3,22,'source-body-must-not-enter-projection') RETURNING id`, conversation, owner, direction).Scan(&message); err != nil {
			t.Fatal(err)
		}
		return conversation, message
	}
	conversation, good := source(bot, "group", group, "incoming")
	_, wrongGroup := source(bot, "group", otherGroup, "incoming")
	_, wrongKind := source(bot, "private", group, "incoming")
	_, wrongAccount := source(otherBot, "group", group, "incoming")
	scopeRepo, err := scopeinfra.NewRepo(pool)
	if err != nil {
		t.Fatal(err)
	}
	policy := scopedomain.Default(scopedomain.ScopeGroup, group)
	policy.Enabled, policy.StoreMessages, policy.AllowMemoryWrite, policy.AllowMemoryRead = true, true, true, true
	policy.ReplyMode = "intent_based"
	if err := scopeRepo.Upsert(ctx, bot, policy); err != nil {
		t.Fatal(err)
	}
	event, allowed, err := scopeRepo.ResolveMemoryAuthorization(ctx, bot, scopedomain.ScopeGroup, group)
	if err != nil || !allowed || event == 0 {
		t.Fatalf("source authorization: %d %v %v", event, allowed, err)
	}
	if _, err := pool.Exec(ctx, `UPDATE messages SET memory_authorization_event_id=$2,segments='[{"type":"text","data":{"text":"我喜欢fixture_fact relevant"}}]'::jsonb,text_content='我喜欢fixture_fact relevant' WHERE id=$1`, good, event); err != nil {
		t.Fatal(err)
	}
	var outgoing int64
	if err := pool.QueryRow(ctx, `INSERT INTO messages(conversation_id,bot_id,direction,sender_id) VALUES($1,$2,'outgoing',22) RETURNING id`, conversation, bot).Scan(&outgoing); err != nil {
		t.Fatal(err)
	}
	base := domain.Memory{BotID: bot, OwnerKey: fmt.Sprint(group), Visibility: domain.VisibilityGroup, Content: "本人自述：我喜欢fixture_fact relevant", SourceMessageID: good, SubjectType: domain.SubjectUser, SubjectID: "22", Status: domain.StatusActive, ApprovedBy: "fixture", Confidence: .1}
	insert := func(item domain.Memory) string {
		fact, ok := domain.FactFromContent(item.Content)
		if !ok {
			t.Fatalf("invalid fixture fact %q", item.Content)
		}
		item.FactKey = fact.Key(item, domain.ExtractionScope{ConversationID: conversation, ScopeType: "group", ScopeID: group, AuthorizationEventID: event})
		id, err := repo.Insert(ctx, item)
		if err != nil {
			t.Fatal(err)
		}
		if item.SourceMessageID != 0 {
			if _, err := pool.Exec(ctx, `UPDATE memories SET fact_key=$2 WHERE memory_id=$1`, id, item.FactKey); err != nil {
				t.Fatal(err)
			}
			if _, err := pool.Exec(ctx, `INSERT INTO memory_fact_sources(id,memory_id,message_id,authorization_event_id) VALUES($1,$2,$3,$4)`, uuid.NewString(), id, item.SourceMessageID, event); err != nil {
				t.Fatal(err)
			}
		}
		return id
	}
	id := insert(base)
	mutations := []func(*domain.Memory){
		func(m *domain.Memory) { m.Visibility = domain.Visibility("bot_shared"); m.SourceMessageID = wrongGroup },
		func(m *domain.Memory) { m.Visibility = domain.Visibility("bot_shared"); m.SourceMessageID = wrongKind },
		func(m *domain.Memory) { m.SourceMessageID = wrongAccount },
		func(m *domain.Memory) { m.SourceMessageID = 0 },
		func(m *domain.Memory) { m.SourceMessageID = outgoing },
		func(m *domain.Memory) { m.ApprovedBy = "" },
		func(m *domain.Memory) { m.ExpiresAt = time.Now().Add(-time.Minute) },
		func(m *domain.Memory) { m.OwnerKey = fmt.Sprint(otherGroup) },
		func(m *domain.Memory) { m.Status = domain.StatusCandidate },
	}
	for i, mutate := range mutations {
		item := base
		item.Confidence = .99
		mutate(&item)
		item.Content = fmt.Sprintf("本人自述：我喜欢fixture_fact relevant invalid_%d", i)
		insert(item)
	}
	for i := range 8 {
		irrelevant := base
		text := fmt.Sprintf("我喜欢unrelated_%d", i)
		irrelevant.Content = "本人自述：" + text
		if err := pool.QueryRow(ctx, `INSERT INTO messages(conversation_id,bot_id,direction,sender_id,text_content,segments,memory_authorization_event_id) VALUES($1,$2,'incoming',22,$3,jsonb_build_array(jsonb_build_object('type','text','data',jsonb_build_object('text',$3::text))),$4) RETURNING id`, conversation, bot, text, event).Scan(&irrelevant.SourceMessageID); err != nil {
			t.Fatal(err)
		}
		irrelevant.Confidence = .99
		insert(irrelevant)
	}
	for i := range 6 {
		wrongPerson := base
		wrongPerson.SubjectID = fmt.Sprint(33 + i)
		wrongPerson.Confidence = .99
		insert(wrongPerson)
	}
	for _, tc := range []struct {
		query, topic string
		subjects     []string
		want         bool
	}{
		{"fixture_fact relevant", "", []string{"22"}, true},
		{"absent_term", "", []string{"22"}, false},
		{"fixture_fact relevant", "", nil, false},
		{"fixture_fact relevant", "", []string{"22", "33"}, false},
		{"alpha beta gamma delta epsilon zeta theta kappa", "fixture_fact relevant", []string{"22"}, true},
	} {
		facts, err := repo.IntentFacts(ctx, bot, group, conversation, tc.query, tc.topic, tc.subjects, nil)
		if err != nil || tc.want && (len(facts) != 1 || facts[0].ID != id || facts[0].Content != base.Content) || !tc.want && len(facts) != 0 {
			t.Fatalf("query %q topic %q subjects %v: facts=%+v err=%v", tc.query, tc.topic, tc.subjects, facts, err)
		}
	}
	if _, err := pool.Exec(ctx, `DELETE FROM messages WHERE id=$1`, good); err != nil {
		t.Fatal(err)
	}
	facts, err := repo.IntentFacts(ctx, bot, group, conversation, "", "", []string{"22"}, []string{id})
	if err != nil || len(facts) != 0 {
		t.Fatalf("deleted source remained visible: %+v %v", facts, err)
	}
}
