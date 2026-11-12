// Integration test for the decision recorder and read model, gated by NAGI_TEST_DSN.
// 决策记录器与读模型的集成测试，以 NAGI_TEST_DSN 为门控。
package infrastructure

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/HappyLadySauceM/Nagi/pkg/db"

	conversationdomain "github.com/HappyLadySauceM/Nagi/internal/conversation/domain"
	conversationinfra "github.com/HappyLadySauceM/Nagi/internal/conversation/infrastructure"
	"github.com/HappyLadySauceM/Nagi/internal/intent/contracts"
	"github.com/HappyLadySauceM/Nagi/internal/intent/domain"
	"github.com/HappyLadySauceM/Nagi/pkg/db/dbtest"
)

// seedJob inserts one inbound message and returns its job id together with the
// account and conversation it belongs to.
// seedJob 插入一条入站消息，返回任务 id 及其所属账号与会话。
func seedJob(t *testing.T, pool *db.Pool) (jobID, botID int64, conversationID string) {
	t.Helper()
	repo, err := conversationinfra.NewRepo(pool)
	if err != nil {
		t.Fatalf("new conversation repo: %v", err)
	}
	base := time.Now().UnixNano()
	const senderID = 992001
	result, err := repo.Ingest(context.Background(), conversationdomain.IngestInput{
		BotID:             992002,
		Kind:              conversationdomain.KindPrivate,
		TargetID:          base % 1_000_000_000,
		PlatformMessageID: base,
		SenderID:          senderID,
		Text:              "决策记录集成测试",
	})
	if err != nil {
		t.Fatalf("ingest: %v", err)
	}
	return result.JobID, 992002, result.ConversationID
}

// TestRecordPersistsStepsAndReadModel verifies that a decision and its steps are
// written together and that the read model joins account, conversation and steps.
// TestRecordPersistsStepsAndReadModel 验证决策与其步骤一起写入，
// 且读模型能联表取出账号、会话与步骤。
func TestRecordPersistsStepsAndReadModel(t *testing.T) {
	pool := dbtest.Open(t)
	jobID, botID, conversationID := seedJob(t, pool)
	repo, err := NewDecisionsRepo(pool)
	if err != nil {
		t.Fatalf("new decisions repo: %v", err)
	}
	ctx := context.Background()

	decision := domain.FinalDecision{
		Decision:       domain.DecisionReply,
		ReasonCode:     "always_reply",
		Provider:       "jev",
		Model:          "jev-latest",
		Scores:         map[string]float64{"directed_to_bot": 0.7},
		LatencyMS:      12,
		RulesetVersion: "builtin-v1",
		Steps: []domain.Step{
			{Index: 0, Phase: domain.StepPolicy, NodeID: domain.NodePolicy, NodeVersion: 1, Status: domain.NodeStatusExecuted, Detail: map[string]any{"ReplyMode": "always_reply"}},
			{Index: 1, Phase: domain.StepRules, NodeID: domain.NodeRules, NodeVersion: 1, Status: domain.NodeStatusExecuted, Detail: map[string]any{"AlwaysReply": true}},
			{Index: 2, Phase: domain.StepFinal, NodeID: domain.NodeDecision, NodeVersion: 1, Status: domain.NodeStatusExecuted, ReasonCode: "always_reply", Detail: map[string]any{"Decision": "reply"}},
		},
		Snapshot: &domain.DecisionSnapshot{
			Version: domain.SnapshotVersion, FlowVersion: domain.FlowVersion, RunID: "run-integration-1",
			Input:          domain.SnapshotInput{Task: domain.Task{JobID: jobID, BotID: botID}, Now: time.Now().UTC()},
			Semantic:       domain.SemanticAnswer{Available: true, IntentCategory: "chat"},
			Prompt:         domain.BuiltinPrompt(domain.PromptProviderJev),
			Rule:           domain.RuleResult{NeedSemantic: true, BuiltinRules: map[string]bool{"closing": true}},
			RulesetVersion: "ruleset-1",
		},
	}
	if err := repo.Record(ctx, jobID, decision, dbtest.Claim(t, pool, jobID)); err != nil {
		t.Fatalf("record: %v", err)
	}

	list, err := repo.List(ctx, contracts.DecisionQuery{BotID: botID, Limit: 10})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list) == 0 {
		t.Fatalf("list returned no decisions")
	}
	latest := list[0]
	if latest.JobID != jobID || latest.ConversationID != conversationID {
		t.Fatalf("list row = %+v, want job %d conversation %s", latest, jobID, conversationID)
	}
	if latest.RulesetVersion != "builtin-v1" || latest.ReasonCode != "always_reply" {
		t.Fatalf("list row metadata = %+v", latest)
	}

	detail, err := repo.Get(ctx, latest.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if len(detail.Steps) != 3 {
		t.Fatalf("steps = %d, want 3", len(detail.Steps))
	}
	if detail.Steps[1].Phase != domain.StepRules || detail.Steps[1].Detail["AlwaysReply"] != true || detail.Steps[1].NodeID != domain.NodeRules {
		t.Fatalf("rules step = %+v", detail.Steps[1])
	}
	if detail.Scores["directed_to_bot"] != 0.7 {
		t.Fatalf("scores = %+v", detail.Scores)
	}
	if detail.SnapshotStatus != domain.SnapshotStatusComplete || detail.RunID != "run-integration-1" || detail.FlowVersion != domain.FlowVersion {
		t.Fatalf("snapshot metadata = %+v", detail)
	}
	snapshot, err := repo.Snapshot(ctx, latest.ID)
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	if snapshot.RunID != "run-integration-1" || snapshot.Semantic.IntentCategory != "chat" {
		t.Fatalf("snapshot = %+v", snapshot)
	}
	if !snapshot.Rule.NeedSemantic || snapshot.RulesetVersion != "ruleset-1" {
		t.Fatalf("snapshot rule = %+v ruleset %q", snapshot.Rule, snapshot.RulesetVersion)
	}
}

// TestListFiltersByConversation verifies the conversation filter and that a
// missing decision reports the not-found error.
// TestListFiltersByConversation 验证会话过滤，以及不存在的决策返回未找到错误。
func TestListFiltersByConversation(t *testing.T) {
	pool := dbtest.Open(t)
	jobID, botID, conversationID := seedJob(t, pool)
	repo, err := NewDecisionsRepo(pool)
	if err != nil {
		t.Fatalf("new decisions repo: %v", err)
	}
	ctx := context.Background()
	if err := repo.Record(ctx, jobID, domain.FinalDecision{
		Decision: domain.DecisionSilent, ReasonCode: "cooldown", Steps: []domain.Step{{Index: 0, Phase: domain.StepFinal}},
	}, dbtest.Claim(t, pool, jobID)); err != nil {
		t.Fatalf("record: %v", err)
	}

	hit, err := repo.List(ctx, contracts.DecisionQuery{BotID: botID, ConversationID: conversationID, Limit: 5})
	if err != nil {
		t.Fatalf("list by conversation: %v", err)
	}
	if len(hit) != 1 || hit[0].ConversationID != conversationID {
		t.Fatalf("conversation filter = %+v", hit)
	}
	miss, err := repo.List(ctx, contracts.DecisionQuery{BotID: botID, ConversationID: "00000000-0000-0000-0000-000000000000", Limit: 5})
	if err != nil {
		t.Fatalf("list miss: %v", err)
	}
	if len(miss) != 0 {
		t.Fatalf("expected no rows for other conversation, got %+v", miss)
	}
	if _, err := repo.Get(ctx, 1<<60); err == nil {
		t.Fatalf("get missing decision should fail")
	}
}

func TestRulesetMultiTargetPublishConflictAndRollback(t *testing.T) {
	pool := dbtest.Open(t)
	repo, err := NewRulesetsRepo(pool)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	const botID = 993077
	defer func() {
		pool.Exec(ctx, "DELETE FROM intent_rule_activations WHERE bot_id=$1", botID)
		pool.Exec(ctx, "DELETE FROM intent_rulesets WHERE bot_id=$1", botID)
	}()
	first, err := repo.CreateDraft(ctx, botID, "group", 901, "first", false)
	if err != nil {
		t.Fatal(err)
	}
	targets := []domain.RuleTarget{{Kind: "group", ID: 901}, {Kind: "private", ID: 902}}
	off := false
	entries := []domain.RuleEntry{{Priority: 0, Enabled: &off, Action: domain.RuleAction{Type: domain.ActionSilent, ReasonCode: "disabled"}}}
	if err := repo.ReplaceEntries(ctx, first.ID, entries, RuleOptions{BuiltinRules: map[string]bool{"closing": false}, Targets: &targets}); err != nil {
		t.Fatal(err)
	}
	if err := repo.Publish(ctx, first.ID); err != nil {
		t.Fatal(err)
	}
	active, found, err := repo.Active(ctx, botID, "private", 902)
	if err != nil || !found || active.ID != first.ID || active.BuiltinRules["closing"] || active.Entries[0].Enabled == nil || *active.Entries[0].Enabled {
		t.Fatalf("multi-target metadata: %+v %v %v", active, found, err)
	}
	second, err := repo.CreateDraft(ctx, botID, "group", 901, "second", true)
	if err != nil {
		t.Fatal(err)
	}
	replacement := []domain.RuleTarget{{Kind: "group", ID: 903}}
	if err := repo.ReplaceEntries(ctx, second.ID, nil, RuleOptions{Targets: &replacement}); err != nil {
		t.Fatal(err)
	}
	if err := repo.Publish(ctx, second.ID); err != nil {
		t.Fatal(err)
	}
	if _, found, err := repo.Active(ctx, botID, "private", 902); err != nil || found {
		t.Fatalf("stale binding: %v %v", found, err)
	}
	conflict, err := repo.CreateDraft(ctx, botID, "group", 904, "conflict", false)
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.ReplaceEntries(ctx, conflict.ID, nil, RuleOptions{Targets: &replacement}); err != nil {
		t.Fatal(err)
	}
	if err := repo.Publish(ctx, conflict.ID); !errors.Is(err, ErrTargetConflict) {
		t.Fatalf("conflict accepted: %v", err)
	}
	if err := repo.Rollback(ctx, botID, "group", 901, first.Version); err != nil {
		t.Fatal(err)
	}
	restored, found, err := repo.Active(ctx, botID, "private", 902)
	if err != nil || !found || restored.ID != first.ID {
		t.Fatalf("rollback: %+v %v %v", restored, found, err)
	}
	if _, found, err := repo.Active(ctx, botID, "group", 903); err != nil || found {
		t.Fatalf("rollback stale binding: %v %v", found, err)
	}
	clone, err := repo.CreateDraft(ctx, botID, "group", 901, "selected archived", true, second.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(clone.Targets) != 1 || clone.Targets[0].ID != 903 {
		t.Fatalf("clone used active version instead of selected archived version: %+v", clone.Targets)
	}

}

func TestRulesetDeletionLifecycle(t *testing.T) {
	pool := dbtest.Open(t)
	repo, err := NewRulesetsRepo(pool)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	botID := time.Now().UnixNano()
	defer pool.Exec(ctx, "DELETE FROM intent_rulesets WHERE bot_id=$1", botID)
	first, err := repo.CreateDraft(ctx, botID, "group", 901, "delete test", false)
	if err != nil {
		t.Fatal(err)
	}
	targets := []domain.RuleTarget{{Kind: "group", ID: 901}, {Kind: "private", ID: 902}}
	if err = repo.ReplaceEntries(ctx, first.ID, nil, RuleOptions{Targets: &targets}); err != nil {
		t.Fatal(err)
	}
	if err = repo.Publish(ctx, first.ID); err != nil {
		t.Fatal(err)
	}
	if err = repo.Delete(ctx, botID+1, first.ID); !errors.Is(err, ErrRulesetNotFound) {
		t.Fatalf("wrong account: %v", err)
	}
	if err = repo.Delete(ctx, botID, first.ID); err != nil {
		t.Fatal(err)
	}
	if err = repo.Delete(ctx, botID, first.ID); err != nil {
		t.Fatalf("repeat: %v", err)
	}
	for _, target := range targets {
		if _, found, err := repo.Active(ctx, botID, target.Kind, target.ID); err != nil || found {
			t.Fatalf("activation remains: %v", err)
		}
	}
	if _, err = repo.Get(ctx, first.ID); !errors.Is(err, ErrRulesetNotFound) {
		t.Fatalf("get deleted: %v", err)
	}
	if err = repo.Publish(ctx, first.ID); !errors.Is(err, ErrRulesetNotFound) {
		t.Fatalf("publish deleted: %v", err)
	}
	if _, err = repo.CreateDraft(ctx, botID, "group", 901, "clone deleted", true, first.ID); !errors.Is(err, ErrRulesetNotFound) {
		t.Fatalf("clone deleted: %v", err)
	}
	next, err := repo.CreateDraft(ctx, botID, "group", 901, "next", false)
	if err != nil {
		t.Fatal(err)
	}
	if next.Version <= first.Version {
		t.Fatal("version reused")
	}
	if err = repo.Delete(ctx, botID, next.ID); err != nil {
		t.Fatal(err)
	}
	rows, err := repo.List(ctx, botID, "", 0)
	if err != nil || len(rows) != 0 {
		t.Fatalf("deleted list: %v %d", err, len(rows))
	}
}

func TestRulesetDeleteSerializesWithPublish(t *testing.T) {
	pool := dbtest.Open(t)
	repo, err := NewRulesetsRepo(pool)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	botID := time.Now().UnixNano()
	defer pool.Exec(ctx, "DELETE FROM intent_rulesets WHERE bot_id=$1", botID)
	old, err := repo.CreateDraft(ctx, botID, "private", 777, "archived", false)
	if err != nil {
		t.Fatal(err)
	}
	if err = repo.Publish(ctx, old.ID); err != nil {
		t.Fatal(err)
	}
	next, err := repo.CreateDraft(ctx, botID, "private", 777, "race", false)
	if err != nil {
		t.Fatal(err)
	}
	if err = repo.Publish(ctx, next.ID); err != nil {
		t.Fatal(err)
	}
	if err = repo.Delete(ctx, botID, old.ID); err != nil {
		t.Fatal(err)
	}
	if err = repo.Rollback(ctx, botID, "private", 777, old.Version); !errors.Is(err, ErrRulesetNotFound) {
		t.Fatalf("rollback deleted: %v", err)
	}
	if err = repo.Delete(ctx, botID, next.ID); err != nil {
		t.Fatal(err)
	}
	race, err := repo.CreateDraft(ctx, botID, "private", 778, "unpublished race", false)
	if err != nil {
		t.Fatal(err)
	}
	start := make(chan struct{})
	published := make(chan error, 1)
	deleted := make(chan error, 1)
	go func() { <-start; published <- repo.Publish(ctx, race.ID) }()
	go func() { <-start; deleted <- repo.Delete(ctx, botID, race.ID) }()
	close(start)
	if err = <-deleted; err != nil {
		t.Fatal(err)
	}
	if err = <-published; err != nil && !errors.Is(err, ErrRulesetNotFound) {
		t.Fatal(err)
	}
	if _, found, err := repo.Active(ctx, botID, "private", 778); err != nil || found {
		t.Fatalf("deleted published active: %v", err)
	}
}

func TestDecisionTransactionRollsBackIfLeaseExpiresDuringWrite(t *testing.T) {
	pool := dbtest.Open(t)
	ctx := context.Background()
	jobID, _, _ := seedJob(t, pool)
	token := dbtest.Claim(t, pool, jobID)
	repo, _ := NewDecisionsRepo(pool)
	_, err := pool.Exec(ctx, `CREATE FUNCTION nagi_test_expire_decision() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN UPDATE message_jobs SET lease_expires_at=clock_timestamp()-interval '1 second' WHERE id=NEW.job_id; RETURN NEW; END $$; CREATE TRIGGER nagi_test_expire_decision AFTER INSERT ON intent_decisions FOR EACH ROW EXECUTE FUNCTION nagi_test_expire_decision()`)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := pool.Exec(ctx, "DROP TRIGGER IF EXISTS nagi_test_expire_decision ON intent_decisions; DROP FUNCTION IF EXISTS nagi_test_expire_decision()"); err != nil {
			t.Error(err)
		}
	})
	err = repo.Record(ctx, jobID, domain.FinalDecision{Decision: domain.DecisionSilent, ReasonCode: "expiry", Steps: []domain.Step{{Index: 0, Phase: domain.StepFinal}}}, token)
	if err == nil {
		t.Fatal("expired decision transaction committed")
	}
	var count int
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM intent_decisions WHERE job_id=$1", jobID).Scan(&count); err != nil || count != 0 {
		t.Fatalf("partial decision persisted: %d %v", count, err)
	}
}

func TestPromptsRepoPublishAndRollback(t *testing.T) {
	pool := dbtest.Open(t)
	repo, err := NewPromptsRepo(pool)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	promptID := domain.BuiltinPromptID + ".test." + time.Now().Format("150405.000000000")
	content := domain.BuiltinPrompt(domain.PromptProviderJev)
	first, err := repo.CreateDraft(ctx, promptID, "本地 Prompt", domain.PromptProviderJev, content, "v1", domain.PromptSourceLocal, "")
	if err != nil {
		t.Fatalf("create draft: %v", err)
	}
	if first.Version != 1 || first.Status != domain.PromptStatusDraft {
		t.Fatalf("first draft = %+v", first)
	}
	if _, found, err := repo.Active(ctx, promptID, domain.PromptProviderJev); err != nil || found {
		t.Fatalf("unexpected active before publish: found=%v err=%v", found, err)
	}
	if err := repo.Publish(ctx, first.ID); err != nil {
		t.Fatalf("publish: %v", err)
	}
	active, found, err := repo.Active(ctx, promptID, domain.PromptProviderJev)
	if err != nil || !found || active.Version != 1 || active.Status != domain.PromptStatusPublished {
		t.Fatalf("active after publish = %+v found=%v err=%v", active, found, err)
	}
	second, err := repo.CreateDraft(ctx, promptID, "本地 Prompt", domain.PromptProviderJev, content, "v2", domain.PromptSourceLocal, "")
	if err != nil || second.Version != 2 {
		t.Fatalf("second draft = %+v err=%v", second, err)
	}
	if err := repo.Publish(ctx, second.ID); err != nil {
		t.Fatalf("publish second: %v", err)
	}
	active, found, err = repo.Active(ctx, promptID, domain.PromptProviderJev)
	if err != nil || !found || active.Version != 2 {
		t.Fatalf("active after second = %+v found=%v err=%v", active, found, err)
	}
	if err := repo.Rollback(ctx, promptID, 1); err != nil {
		t.Fatalf("rollback: %v", err)
	}
	active, found, err = repo.Active(ctx, promptID, domain.PromptProviderJev)
	if err != nil || !found || active.Version != 1 {
		t.Fatalf("active after rollback = %+v found=%v err=%v", active, found, err)
	}
}

// TestStatsReadsLegacySemanticDetailKeys verifies that historical steps written
// with lowercase detail keys still count as successful semantic calls.
// TestStatsReadsLegacySemanticDetailKeys 验证旧版小写 detail 键仍计入语义成功统计。
func TestStatsReadsLegacySemanticDetailKeys(t *testing.T) {
	pool := dbtest.Open(t)
	jobID, botID, _ := seedJob(t, pool)
	repo, err := NewDecisionsRepo(pool)
	if err != nil {
		t.Fatalf("new decisions repo: %v", err)
	}
	before, err := repo.Stats(context.Background(), botID, time.Now().Add(-time.Hour))
	if err != nil {
		t.Fatalf("baseline stats: %v", err)
	}
	decision := domain.FinalDecision{
		Decision:   domain.DecisionSilent,
		ReasonCode: "jev_unavailable",
		Steps: []domain.Step{{
			Index:       0,
			Phase:       domain.StepSemantic,
			NodeID:      domain.NodeSemantic,
			NodeVersion: 1,
			Status:      domain.NodeStatusExecuted,
			Detail:      map[string]any{"called": true, "available": true},
		}},
	}
	if err := repo.Record(context.Background(), jobID, decision, dbtest.Claim(t, pool, jobID)); err != nil {
		t.Fatalf("record: %v", err)
	}
	stats, err := repo.Stats(context.Background(), botID, time.Now().Add(-time.Hour))
	if err != nil {
		t.Fatalf("stats: %v", err)
	}
	if stats.SemanticCalls != before.SemanticCalls+1 || stats.SemanticSuccess != before.SemanticSuccess+1 || stats.Degraded != before.Degraded {
		t.Fatalf("legacy stats = %+v, baseline = %+v", stats, before)
	}
}

func promptFixture(t *testing.T, repo *PromptsRepo) (string, domain.PromptRecord) {
	t.Helper()
	ctx := context.Background()
	promptID := domain.BuiltinPromptID + ".rollback." + time.Now().Format("150405.000000000")
	content := domain.BuiltinPrompt(domain.PromptProviderJev)
	v1, err := repo.CreateDraft(ctx, promptID, "rollback fixture", domain.PromptProviderJev, content, "v1", domain.PromptSourceLocal, "")
	if err != nil {
		t.Fatalf("create v1: %v", err)
	}
	if err := repo.Publish(ctx, v1.ID); err != nil {
		t.Fatalf("publish v1: %v", err)
	}
	return promptID, v1
}

func TestPromptsRepoRollbackRejectsDraftAndKeepsActive(t *testing.T) {
	pool := dbtest.Open(t)
	repo, err := NewPromptsRepo(pool)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	promptID, v1 := promptFixture(t, repo)
	content := domain.BuiltinPrompt(domain.PromptProviderJev)
	v2, err := repo.CreateDraft(ctx, promptID, "rollback fixture", domain.PromptProviderJev, content, "v2", domain.PromptSourceLocal, "")
	if err != nil {
		t.Fatalf("create v2: %v", err)
	}
	if err := repo.Rollback(ctx, promptID, v2.Version); !errors.Is(err, ErrPromptNotArchived) {
		t.Fatalf("draft rollback err = %v, want ErrPromptNotArchived", err)
	}
	active, found, err := repo.Active(ctx, promptID, domain.PromptProviderJev)
	if err != nil || !found || active.Version != v1.Version {
		t.Fatalf("active after rejected rollback = %+v found=%v err=%v", active, found, err)
	}
}

func TestPromptsRepoRollbackArchivedPublishedVersion(t *testing.T) {
	pool := dbtest.Open(t)
	repo, err := NewPromptsRepo(pool)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	promptID, v1 := promptFixture(t, repo)
	content := domain.BuiltinPrompt(domain.PromptProviderJev)
	v2, err := repo.CreateDraft(ctx, promptID, "rollback fixture", domain.PromptProviderJev, content, "v2", domain.PromptSourceLocal, "")
	if err != nil {
		t.Fatalf("create v2: %v", err)
	}
	if err := repo.Publish(ctx, v2.ID); err != nil {
		t.Fatalf("publish v2: %v", err)
	}
	archived, err := repo.Get(ctx, v1.ID)
	if err != nil || archived.Status != domain.PromptStatusArchived || archived.PublishedAt == "" {
		t.Fatalf("v1 before rollback = %+v err=%v", archived, err)
	}
	if err := repo.Rollback(ctx, promptID, v1.Version); err != nil {
		t.Fatalf("rollback archived: %v", err)
	}
	active, found, err := repo.Active(ctx, promptID, domain.PromptProviderJev)
	if err != nil || !found || active.Version != v1.Version {
		t.Fatalf("active after rollback = %+v found=%v err=%v", active, found, err)
	}
}

func TestPromptsRepoRollbackRejectsCrossPromptVersion(t *testing.T) {
	pool := dbtest.Open(t)
	repo, err := NewPromptsRepo(pool)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	promptA, _ := promptFixture(t, repo)
	content := domain.BuiltinPrompt(domain.PromptProviderJev)
	promptB := domain.BuiltinPromptID + ".rollback.other." + time.Now().Format("150405.000000000")
	if _, err := repo.CreateDraft(ctx, promptB, "other", domain.PromptProviderJev, content, "v1", domain.PromptSourceLocal, ""); err != nil {
		t.Fatalf("create other v1: %v", err)
	}
	if err := repo.Rollback(ctx, promptA, 2); !errors.Is(err, ErrPromptNotFound) {
		t.Fatalf("cross-prompt rollback err = %v, want ErrPromptNotFound", err)
	}
}

func TestPromptsRepoPublishRollbackConcurrent(t *testing.T) {
	pool := dbtest.Open(t)
	repo, err := NewPromptsRepo(pool)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	promptID, v1 := promptFixture(t, repo)
	content := domain.BuiltinPrompt(domain.PromptProviderJev)
	v2, err := repo.CreateDraft(ctx, promptID, "rollback fixture", domain.PromptProviderJev, content, "v2", domain.PromptSourceLocal, "")
	if err != nil {
		t.Fatalf("create v2: %v", err)
	}
	if err := repo.Publish(ctx, v2.ID); err != nil {
		t.Fatalf("publish v2: %v", err)
	}
	v3, err := repo.CreateDraft(ctx, promptID, "rollback fixture", domain.PromptProviderJev, content, "v3", domain.PromptSourceLocal, "")
	if err != nil {
		t.Fatalf("create v3: %v", err)
	}
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	wg.Add(2)
	go func() { defer wg.Done(); errs <- repo.Publish(ctx, v3.ID) }()
	go func() { defer wg.Done(); errs <- repo.Rollback(ctx, promptID, v1.Version) }()
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent operation: %v", err)
		}
	}
	versions, err := repo.Versions(ctx, promptID)
	if err != nil {
		t.Fatalf("versions: %v", err)
	}
	published := 0
	for _, version := range versions {
		if version.Status == domain.PromptStatusPublished {
			published++
			if version.Version != v1.Version && version.Version != v3.Version {
				t.Fatalf("unexpected active version: %+v", version)
			}
		}
	}
	if published != 1 {
		t.Fatalf("published versions = %d", published)
	}
}
