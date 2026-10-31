package nagi

import (
	"encoding/json"
	"entgo.io/ent"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
	"time"
)

type IntentRuleset struct{ ent.Schema }

func (IntentRuleset) Fields() []ent.Field {
	return []ent.Field{
		field.Int64("id").SchemaType(map[string]string{"postgres": "bigint"}),
		field.Int64("bot_id").SchemaType(map[string]string{"postgres": "bigint"}),
		field.String("scope_type").SchemaType(map[string]string{"postgres": "text"}),
		field.Int64("scope_id").SchemaType(map[string]string{"postgres": "bigint"}),
		field.Int("version").SchemaType(map[string]string{"postgres": "integer"}),
		field.String("status").SchemaType(map[string]string{"postgres": "text"}),
		field.String("note").SchemaType(map[string]string{"postgres": "text"}).Annotations(entsql.DefaultExpr("''::text")).Default(""),
		field.Time("created_at").Annotations(entsql.DefaultExpr("now()")).Default(time.Now),
		field.Time("published_at").Optional().Nillable(),
		field.JSON("builtin_rules", json.RawMessage{}).Annotations(entsql.DefaultExpr("'{}'::jsonb")).Default(json.RawMessage("{}")),
		field.JSON("targets", json.RawMessage{}).Annotations(entsql.DefaultExpr("'[]'::jsonb")).Default(json.RawMessage("[]")),
		field.Time("deleted_at").Optional().Nillable(),
	}
}
func (IntentRuleset) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "intent_rulesets", Checks: map[string]string{"intent_rulesets_scope_type_check": "((scope_type = ANY (ARRAY['group'::text, 'private'::text])))", "intent_rulesets_version_check": "((version > 0))", "intent_rulesets_status_check": "((status = ANY (ARRAY['draft'::text, 'published'::text, 'archived'::text])))"}}}
}
func (IntentRuleset) Indexes() []ent.Index {
	return []ent.Index{index.Fields("bot_id", "scope_type", "scope_id", "version").Unique().StorageKey("intent_rulesets_bot_id_scope_type_scope_id_version_key"), index.Fields("bot_id", "scope_type", "scope_id", "version").StorageKey("intent_rulesets_scope")}
}

func (IntentRuleset) Edges() []ent.Edge {
	return []ent.Edge{edge.To("intent_rule_entries_ruleset_id", IntentRuleEntry.Type).Annotations(entsql.OnDelete("CASCADE")), edge.To("intent_rule_activations_ruleset_id", IntentRuleActivation.Type).Annotations(entsql.OnDelete("RESTRICT"))}
}
