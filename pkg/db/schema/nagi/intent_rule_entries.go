package nagi

import (
	"encoding/json"
	"entgo.io/ent"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

type IntentRuleEntry struct{ ent.Schema }

func (IntentRuleEntry) Fields() []ent.Field {
	return []ent.Field{
		field.Int64("id").SchemaType(map[string]string{"postgres": "bigint"}),
		field.Int64("ruleset_id").SchemaType(map[string]string{"postgres": "bigint"}),
		field.Int("priority").SchemaType(map[string]string{"postgres": "integer"}),
		field.JSON("match", json.RawMessage{}).Annotations(entsql.DefaultExpr("'{}'::jsonb")).Default(json.RawMessage("{}")),
		field.JSON("action", json.RawMessage{}),
		field.Bool("enabled").Annotations(entsql.DefaultExpr("true")).Default(true),
	}
}
func (IntentRuleEntry) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "intent_rule_entries", Checks: map[string]string{}}}
}
func (IntentRuleEntry) Indexes() []ent.Index {
	return []ent.Index{index.Fields("ruleset_id", "priority").Unique().StorageKey("intent_rule_entries_ruleset_id_priority_key")}
}

func (IntentRuleEntry) Edges() []ent.Edge {
	return []ent.Edge{edge.From("ruleset_id_ref", IntentRuleset.Type).Ref("intent_rule_entries_ruleset_id").Field("ruleset_id").Unique().Annotations(entsql.OnDelete("CASCADE")).Required()}
}
