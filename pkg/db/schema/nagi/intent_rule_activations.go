package nagi

import (
	"entgo.io/ent"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
	"time"
)

type IntentRuleActivation struct{ ent.Schema }

func (IntentRuleActivation) Fields() []ent.Field {
	return []ent.Field{
		field.Int64("id"),
		field.Int64("bot_id").SchemaType(map[string]string{"postgres": "bigint"}),
		field.String("scope_type").SchemaType(map[string]string{"postgres": "text"}),
		field.Int64("scope_id").SchemaType(map[string]string{"postgres": "bigint"}),
		field.Int64("ruleset_id").SchemaType(map[string]string{"postgres": "bigint"}),
		field.Time("updated_at").Annotations(entsql.DefaultExpr("now()")).Default(time.Now),
	}
}
func (IntentRuleActivation) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "intent_rule_activations", Checks: map[string]string{}}}
}
func (IntentRuleActivation) Indexes() []ent.Index {
	return []ent.Index{index.Fields("bot_id", "scope_type", "scope_id").Unique().StorageKey("intent_rule_activations_business_key")}
}

func (IntentRuleActivation) Edges() []ent.Edge {
	return []ent.Edge{edge.From("ruleset_id_ref", IntentRuleset.Type).Ref("intent_rule_activations_ruleset_id").Field("ruleset_id").Unique().Annotations(entsql.OnDelete("RESTRICT")).Required()}
}
