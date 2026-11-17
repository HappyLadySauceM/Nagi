// Integration tests for ruleset question persistence, gated by NAGI_TEST_DSN.
// 规则集问题集持久化的集成测试，以 NAGI_TEST_DSN 为门控。
package infrastructure

import (
	"context"
	"testing"
	"time"

	"github.com/HappyLadySauceM/Nagi/internal/intent/domain"
	"github.com/HappyLadySauceM/Nagi/pkg/db/dbtest"
)

// TestRulesetQuestionsReplaceAndClone verifies ReplaceEntries stores a question
// set, Get reads it back, CreateDraft clones it, and an empty set clears it.
// TestRulesetQuestionsReplaceAndClone 验证 ReplaceEntries 保存问题集、Get 读回、
// CreateDraft 克隆问题集，空集合可清空。
func TestRulesetQuestionsReplaceAndClone(t *testing.T) {
	pool := dbtest.Open(t)
	repo, err := NewRulesetsRepo(pool)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	botID := time.Now().UnixNano()
	defer pool.Exec(ctx, "DELETE FROM intent_rulesets WHERE bot_id=$1", botID)

	questions := map[string]domain.PromptQuestion{
		"directed_to_bot": {Type: "noul", Role: domain.QuestionRoleDirectedToBot, Weight: 1, Instructions: "是否在对机器人说话？"},
		"intent": {Type: "choice", Role: domain.QuestionRoleIntent, Instructions: "当前意图？",
			Criteria: map[string]string{"closing": "告别", "chat": "闲聊"}},
	}
	first, err := repo.CreateDraft(ctx, botID, "group", 0, "questions", false)
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.ReplaceEntries(ctx, first.ID, nil, RuleOptions{Questions: &questions}); err != nil {
		t.Fatal(err)
	}
	stored, err := repo.Get(ctx, first.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Questions["intent"].Type != "choice" || stored.Questions["directed_to_bot"].Role != domain.QuestionRoleDirectedToBot {
		t.Fatalf("stored questions = %+v", stored.Questions)
	}
	if err := repo.Publish(ctx, first.ID); err != nil {
		t.Fatal(err)
	}
	clone, err := repo.CreateDraft(ctx, botID, "group", 0, "clone", true)
	if err != nil {
		t.Fatal(err)
	}
	if len(clone.Questions) != len(questions) || clone.Questions["intent"].Criteria["closing"] != "告别" {
		t.Fatalf("cloned questions = %+v", clone.Questions)
	}

	empty := map[string]domain.PromptQuestion{}
	if err := repo.ReplaceEntries(ctx, clone.ID, nil, RuleOptions{Questions: &empty}); err != nil {
		t.Fatal(err)
	}
	cleared, err := repo.Get(ctx, clone.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(cleared.Questions) != 0 {
		t.Fatalf("cleared questions = %+v", cleared.Questions)
	}
}
