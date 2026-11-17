// Tests for the ruleset question API round-trip.
// 规则集问题集 API 往返测试。
package intent

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/HappyLadySauceM/Nagi/cmd/app/svc"
	intentdomain "github.com/HappyLadySauceM/Nagi/internal/intent/domain"
	intentinfra "github.com/HappyLadySauceM/Nagi/internal/intent/infrastructure"
	"github.com/HappyLadySauceM/Nagi/pkg/db/dbtest"
)

// rulesetState captures the fields needed to assert PATCH preservation.
// rulesetState 捕获用于断言 PATCH 保留语义的字段。
type rulesetState struct {
	Questions map[string]struct {
		Type     string
		Role     string
		Criteria map[string]string
	}
	Entries []struct {
		Priority int
		Action   struct {
			ReasonCode string `json:"reason_code"`
		}
	}
	BuiltinRules map[string]bool
}

// TestRulesetQuestionsUpdateAndGet verifies PATCH stores a question set that a
// later GET returns, that a questions-only PATCH leaves entries and builtin
// switches untouched, that an invalid set is rejected, and that an explicit
// empty entries array clears the entries.
// TestRulesetQuestionsUpdateAndGet 验证 PATCH 保存问题集后 GET 能返回；仅提交 questions
// 时条目与内置开关保持不变；无效问题集被拒绝；显式提交 entries=[] 才清空条目。
func TestRulesetQuestionsUpdateAndGet(t *testing.T) {
	pool := dbtest.Open(t)
	repo, err := intentinfra.NewRulesetsRepo(pool)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	botID := time.Now().UnixNano()
	defer pool.Exec(ctx, "DELETE FROM intent_rulesets WHERE bot_id=$1", botID)
	set, err := repo.CreateDraft(ctx, botID, "private", 321, "questions api", false)
	if err != nil {
		t.Fatal(err)
	}
	seedEntries := []intentdomain.RuleEntry{{Priority: 0, Action: intentdomain.RuleAction{Type: intentdomain.ActionReply, ReasonCode: "seed"}}}
	seedBuiltin := map[string]bool{"closing": false}
	if err := repo.ReplaceEntries(ctx, set.ID, &seedEntries, intentinfra.RuleOptions{BuiltinRules: &seedBuiltin}); err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	NewHandler(&svc.ServiceContext{Rulesets: repo}).RegisterRules(mux)
	path := fmt.Sprintf("/api/v1/intent/rulesets/%d", set.ID)

	readState := func() rulesetState {
		get := httptest.NewRecorder()
		mux.ServeHTTP(get, httptest.NewRequest(http.MethodGet, path, nil))
		if get.Code != http.StatusOK {
			t.Fatalf("get status = %d: %s", get.Code, get.Body.String())
		}
		var env envelope
		if err := json.Unmarshal(get.Body.Bytes(), &env); err != nil {
			t.Fatal(err)
		}
		var state rulesetState
		if err := json.Unmarshal(env.Data, &state); err != nil {
			t.Fatal(err)
		}
		return state
	}

	body, _ := json.Marshal(map[string]any{"questions": map[string]any{
		"directed_to_bot": map[string]any{"Type": "noul", "Role": "directed_to_bot", "Weight": 1, "Instructions": "是否在对机器人说话？"},
		"intent":          map[string]any{"Type": "choice", "Role": "intent", "Instructions": "当前意图？", "Criteria": map[string]string{"closing": "告别", "chat": "闲聊"}},
	}})
	patch := httptest.NewRecorder()
	mux.ServeHTTP(patch, httptest.NewRequest(http.MethodPatch, path, bytes.NewReader(body)))
	if patch.Code != http.StatusOK {
		t.Fatalf("patch status = %d: %s", patch.Code, patch.Body.String())
	}

	state := readState()
	if state.Questions["intent"].Type != "choice" || state.Questions["intent"].Criteria["closing"] != "告别" || state.Questions["directed_to_bot"].Role != "directed_to_bot" {
		t.Fatalf("questions = %+v", state.Questions)
	}
	if len(state.Entries) != 1 || state.Entries[0].Action.ReasonCode != "seed" {
		t.Fatalf("questions-only patch changed entries: %+v", state.Entries)
	}
	if enabled, ok := state.BuiltinRules["closing"]; !ok || enabled {
		t.Fatalf("questions-only patch changed builtin switches: %+v", state.BuiltinRules)
	}

	bad, _ := json.Marshal(map[string]any{"questions": map[string]any{
		"intent": map[string]any{"Type": "noul", "Role": "intent", "Instructions": "x"},
	}})
	reject := httptest.NewRecorder()
	mux.ServeHTTP(reject, httptest.NewRequest(http.MethodPatch, path, bytes.NewReader(bad)))
	if reject.Code != http.StatusBadRequest {
		t.Fatalf("invalid questions status = %d, want 400: %s", reject.Code, reject.Body.String())
	}

	clearBody, _ := json.Marshal(map[string]any{"entries": []any{}})
	clearRec := httptest.NewRecorder()
	mux.ServeHTTP(clearRec, httptest.NewRequest(http.MethodPatch, path, bytes.NewReader(clearBody)))
	if clearRec.Code != http.StatusOK {
		t.Fatalf("clear entries status = %d: %s", clearRec.Code, clearRec.Body.String())
	}
	cleared := readState()
	if len(cleared.Entries) != 0 {
		t.Fatalf("explicit empty entries did not clear: %+v", cleared.Entries)
	}
	if enabled, ok := cleared.BuiltinRules["closing"]; !ok || enabled {
		t.Fatalf("entries clear changed builtin switches: %+v", cleared.BuiltinRules)
	}
}
