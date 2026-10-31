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

type IntentDecisionStep struct{ ent.Schema }

func (IntentDecisionStep) Fields() []ent.Field {
	return []ent.Field{
		field.Int64("id").SchemaType(map[string]string{"postgres": "bigint"}),
		field.Int64("decision_id").SchemaType(map[string]string{"postgres": "bigint"}),
		field.Int("step_index").SchemaType(map[string]string{"postgres": "integer"}),
		field.String("phase").SchemaType(map[string]string{"postgres": "text"}),
		field.String("reason_code").SchemaType(map[string]string{"postgres": "text"}).Annotations(entsql.DefaultExpr("''::text")).Default(""),
		field.JSON("detail", json.RawMessage{}).Annotations(entsql.DefaultExpr("'{}'::jsonb")).Default(json.RawMessage("{}")),
		field.Time("created_at").Annotations(entsql.DefaultExpr("now()")).Default(time.Now),
	}
}
func (IntentDecisionStep) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "intent_decision_steps", Checks: map[string]string{"intent_decision_steps_step_index_check": "((step_index >= 0))"}}}
}
func (IntentDecisionStep) Indexes() []ent.Index {
	return []ent.Index{index.Fields("decision_id", "step_index").Unique().StorageKey("intent_decision_steps_decision_id_step_index_key"), index.Fields("decision_id", "step_index").StorageKey("intent_decision_steps_decision")}
}

func (IntentDecisionStep) Edges() []ent.Edge {
	return []ent.Edge{edge.From("decision_id_ref", IntentDecision.Type).Ref("intent_decision_steps_decision_id").Field("decision_id").Unique().Annotations(entsql.OnDelete("CASCADE")).Required()}
}
