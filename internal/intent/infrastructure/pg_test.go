// Integration test for the decision recorder and read model, gated by NAGI_TEST_DSN.
// 决策记录器与读模型的集成测试，以 NAGI_TEST_DSN 为门控。
package infrastructure

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	conversationdomain "github.com/HappyLadySauceM/Nagi/internal/conversation/domain"
	conversationinfra "github.com/HappyLadySauceM/Nagi/internal/conversation/infrastructure"
	"github.com/HappyLadySauceM/Nagi/internal/intent/contracts"
	"github.com/HappyLadySauceM/Nagi/internal/intent/domain"
	"github.com/HappyLadySauceM/Nagi/pkg/db/dbtest"
)

// seedJob inserts one inbound message and returns its job id together with the
// account and conversation it belongs to.
// seedJob 插入一条入站消息，返回任务 id 及其所属账号与会话。
func seedJob(t *testing.T, pool *pgxpool.Pool) (jobID, botID int64, conversationID string) {
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
			{Index: 0, Phase: domain.StepPolicy, Detail: map[string]any{"reply_mode": "always_reply"}},
			{Index: 1, Phase: domain.StepRules, Detail: map[string]any{"always_reply": true}},
			{Index: 2, Phase: domain.StepFinal, ReasonCode: "always_reply", Detail: map[string]any{"decision": "reply"}},
		},
	}
	if err := repo.Record(ctx, jobID, decision); err != nil {
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
	if detail.Steps[1].Phase != domain.StepRules || detail.Steps[1].Detail["always_reply"] != true {
		t.Fatalf("rules step = %+v", detail.Steps[1])
	}
	if detail.Scores["directed_to_bot"] != 0.7 {
		t.Fatalf("scores = %+v", detail.Scores)
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
	}); err != nil {
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
