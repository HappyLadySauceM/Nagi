package intent

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/HappyLadySauceM/Nagi/cmd/app/svc"
	intentdomain "github.com/HappyLadySauceM/Nagi/internal/intent/domain"
	intentinfra "github.com/HappyLadySauceM/Nagi/internal/intent/infrastructure"
	scopeapp "github.com/HappyLadySauceM/Nagi/internal/scope/application"
	scopedomain "github.com/HappyLadySauceM/Nagi/internal/scope/domain"
	scopeinfra "github.com/HappyLadySauceM/Nagi/internal/scope/infrastructure"
	"github.com/HappyLadySauceM/Nagi/pkg/db/dbtest"
)

type builtinRulePayload struct {
	RuleID     string
	Order      int
	Title      string
	Condition  string
	Action     string
	Editable   bool
	Source     string
	Enabled    bool
	Applicable bool
	Status     string
	Reason     string
}

type fixedConstraintPayload struct {
	ConstraintID string
	Order        int
	Title        string
	Condition    string
	Action       string
	ReasonCode   string
	Editable     bool
	Source       string
	Value        any
	Applicable   bool
	Status       string
	Reason       string
}

func newRulesServer(rulesets *intentinfra.RulesetsRepo, scopes ...*scopeapp.Service) *http.ServeMux {
	mux := http.NewServeMux()
	svcCtx := &svc.ServiceContext{Rulesets: rulesets}
	if len(scopes) > 0 {
		svcCtx.Scope = scopes[0]
	}
	NewHandler(svcCtx).RegisterRules(mux)
	return mux
}

func getRulePayload(t *testing.T, mux *http.ServeMux, url string, target any) {
	t.Helper()
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, url, nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("%s status = %d: %s", url, rec.Code, rec.Body.String())
	}
	var env envelope
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode envelope: %v", err)
	}
	if err := json.Unmarshal(env.Data, target); err != nil {
		t.Fatalf("decode data: %v", err)
	}
}

// TestBuiltinRulesCatalogWithoutRulesets verifies the static catalog is complete
// and machine-readable even without a database or a custom ruleset.
// TestBuiltinRulesCatalogWithoutRulesets 验证无数据库、无自定义规则时仍返回完整可机读目录。
func TestBuiltinRulesCatalogWithoutRulesets(t *testing.T) {
	mux := newRulesServer(nil)
	var data struct {
		Version          string
		Source           string
		Rules            []builtinRulePayload
		FixedConstraints []fixedConstraintPayload
	}
	getRulePayload(t, mux, "/api/v1/intent/builtin-rules", &data)
	if data.Version != intentdomain.BuiltinRulesVersion || data.Source != "builtin" {
		t.Fatalf("catalog header = %q/%q", data.Version, data.Source)
	}
	if len(data.Rules) != 5 {
		t.Fatalf("rules = %d, want 5", len(data.Rules))
	}
	want := []struct {
		id, title, condition, action string
		order                        int
	}{
		{"closing", "告别结束", "intent=closing", "silent", 10},
		{"addressed_to_other", "对他人说话优先抑制", "addressed_to_other >= AddressedOtherMax", "silent", 20},
		{"continuation", "延续机器人话题", "continues_bot_thread >= Continuation", "reply", 30},
		{"interjection", "自然插话", "reply_mode=intent_based && appropriate_to_join >= Interjection", "reply", 40},
		{"directed_to_bot", "指向机器人", "directed_to_bot >= Continuation", "reply", 50},
	}
	for index, item := range want {
		got := data.Rules[index]
		if got.RuleID != item.id || got.Title != item.title || got.Condition != item.condition || got.Action != item.action || got.Order != item.order || !got.Editable || got.Source != "builtin" {
			t.Fatalf("rule %d = %+v, want %+v", index, got, item)
		}
	}
	if len(data.FixedConstraints) != 6 {
		t.Fatalf("fixed constraints = %d, want 6", len(data.FixedConstraints))
	}
	ids := map[string]bool{}
	for _, item := range data.FixedConstraints {
		ids[item.ConstraintID] = true
	}
	for _, id := range []string{"policy_enabled", "observe_only", "cooldown", "hourly_limit", "tool_permissions", "sending_budget"} {
		if !ids[id] {
			t.Fatalf("missing fixed constraint %q", id)
		}
	}
}

// TestEffectiveRulesDefaultsAndPrivateApplicability verifies default switches,
// private-scope applicability and policy values without a custom ruleset.
// TestEffectiveRulesDefaultsAndPrivateApplicability 验证无自定义规则时的默认开关、私聊适用性与策略值。
func TestEffectiveRulesDefaultsAndPrivateApplicability(t *testing.T) {
	mux := newRulesServer(nil)
	var data struct {
		Version          string
		Source           string
		BuiltinRules     []builtinRulePayload
		ActiveRuleset    any
		FixedConstraints []fixedConstraintPayload
		Policy           map[string]any
	}
	getRulePayload(t, mux, "/api/v1/intent/rules/effective?bot_id=1&scope_type=private&scope_id=200", &data)
	if data.Version != intentdomain.BuiltinRulesVersion || data.Source != "builtin" || data.ActiveRuleset != nil {
		t.Fatalf("effective header = %q/%q active=%v", data.Version, data.Source, data.ActiveRuleset)
	}
	if len(data.BuiltinRules) != 5 {
		t.Fatalf("builtin rules = %d, want 5", len(data.BuiltinRules))
	}
	for _, item := range data.BuiltinRules {
		if item.RuleID == "closing" {
			if !item.Enabled || !item.Applicable || item.Status != "active" || item.Reason != "" {
				t.Fatalf("closing = %+v", item)
			}
			continue
		}
		if item.Enabled != true || item.Applicable != false || item.Status != "not_applicable" || item.Reason != "scope" {
			t.Fatalf("private rule %s = %+v", item.RuleID, item)
		}
	}
	if len(data.FixedConstraints) != 6 {
		t.Fatalf("fixed constraints = %d", len(data.FixedConstraints))
	}
	for _, item := range data.FixedConstraints {
		if item.ConstraintID == "cooldown" {
			if value, ok := item.Value.(float64); !ok || value != 10000 {
				t.Fatalf("cooldown value = %#v", item.Value)
			}
		}
	}
	if data.Policy["MinReplyIntervalMS"] != float64(10000) || data.Policy["Enabled"] != false {
		t.Fatalf("policy = %+v", data.Policy)
	}
}

// TestEffectiveRulesDraftAndPublish verifies a builtin switch only takes effect
// after the existing ruleset draft is published, and global switches inherit.
// TestEffectiveRulesDraftAndPublish 验证内置开关仅在现有规则集草稿发布后生效，且全局开关可继承。
func TestEffectiveRulesDraftAndPublish(t *testing.T) {
	pool := dbtest.Open(t)
	repo, err := intentinfra.NewRulesetsRepo(pool)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	botID := time.Now().UnixNano()
	const groupID = 700
	defer func() {
		_, _ = pool.Exec(ctx, `DELETE FROM intent_rule_activations WHERE bot_id=$1`, botID)
		_, _ = pool.Exec(ctx, `DELETE FROM intent_rulesets WHERE bot_id=$1`, botID)
		_, _ = pool.Exec(ctx, `DELETE FROM scope_policies WHERE bot_id=$1`, botID)
		_, _ = pool.Exec(ctx, `DELETE FROM scope_memory_authorization_events WHERE bot_id=$1`, botID)
		_, _ = pool.Exec(ctx, `DELETE FROM bot_accounts WHERE bot_id=$1`, botID)
	}()
	if _, err := pool.Exec(ctx, `INSERT INTO bot_accounts (bot_id, nickname) VALUES ($1, 'builtin-rules-test') ON CONFLICT DO NOTHING`, botID); err != nil {
		t.Fatal(err)
	}

	scopeRepo, err := scopeinfra.NewRepo(pool)
	if err != nil {
		t.Fatal(err)
	}
	scopeSvc, err := scopeapp.NewService(scopeRepo)
	if err != nil {
		t.Fatal(err)
	}
	policy := scopedomain.Default(scopedomain.ScopeGroup, groupID)
	policy.Enabled = true
	if err := scopeRepo.Upsert(ctx, botID, policy); err != nil {
		t.Fatal(err)
	}

	mux := newRulesServer(repo, scopeSvc)
	draft, err := repo.CreateDraft(ctx, botID, "group", groupID, "switch draft", false)
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.ReplaceEntries(ctx, draft.ID, nil, intentinfra.RuleOptions{BuiltinRules: map[string]bool{"closing": false}}); err != nil {
		t.Fatal(err)
	}

	var before struct {
		BuiltinRules []builtinRulePayload
	}
	getRulePayload(t, mux, "/api/v1/intent/rules/effective?bot_id="+strconv.FormatInt(botID, 10)+"&scope_type=group&scope_id=700", &before)
	if !ruleByID(before.BuiltinRules, "closing").Enabled {
		t.Fatal("draft switch must not affect effective rules before publish")
	}

	if err := repo.Publish(ctx, draft.ID); err != nil {
		t.Fatal(err)
	}
	var after struct {
		Version       string
		Source        string
		BuiltinRules  []builtinRulePayload
		ActiveRuleset *rulesetInfo
	}
	getRulePayload(t, mux, "/api/v1/intent/rules/effective?bot_id="+strconv.FormatInt(botID, 10)+"&scope_type=group&scope_id=700", &after)
	closing := ruleByID(after.BuiltinRules, "closing")
	if closing.Enabled || closing.Applicable || closing.Status != "not_applicable" || closing.Reason != "disabled" {
		t.Fatalf("published closing = %+v", closing)
	}
	if after.ActiveRuleset == nil || after.Source != "specific" || after.Version == intentdomain.BuiltinRulesVersion {
		t.Fatalf("active ruleset header = %+v", after)
	}

	global, err := repo.CreateDraft(ctx, botID, "group", 0, "global switch", false)
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.ReplaceEntries(ctx, global.ID, nil, intentinfra.RuleOptions{BuiltinRules: map[string]bool{"continuation": false}}); err != nil {
		t.Fatal(err)
	}
	if err := repo.Publish(ctx, global.ID); err != nil {
		t.Fatal(err)
	}
	var inherited struct {
		BuiltinRules  []builtinRulePayload
		GlobalRuleset *rulesetInfo
	}
	getRulePayload(t, mux, "/api/v1/intent/rules/effective?bot_id="+strconv.FormatInt(botID, 10)+"&scope_type=group&scope_id=700", &inherited)
	if ruleByID(inherited.BuiltinRules, "continuation").Enabled {
		t.Fatal("global switch must be inherited into effective rules")
	}
	if inherited.GlobalRuleset == nil {
		t.Fatal("global ruleset must be returned")
	}
}

func ruleByID(rules []builtinRulePayload, id string) builtinRulePayload {
	for _, rule := range rules {
		if rule.RuleID == id {
			return rule
		}
	}
	return builtinRulePayload{}
}
