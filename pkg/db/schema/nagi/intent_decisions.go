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

type IntentDecision struct{ ent.Schema }

func (IntentDecision) Fields() []ent.Field {
	return []ent.Field{
		field.Int64("id").SchemaType(map[string]string{"postgres": "bigint"}),
		field.Int64("job_id").SchemaType(map[string]string{"postgres": "bigint"}),
		field.String("provider").SchemaType(map[string]string{"postgres": "text"}),
		field.String("model").SchemaType(map[string]string{"postgres": "text"}).Annotations(entsql.DefaultExpr("''::text")).Default(""),
		field.String("decision").SchemaType(map[string]string{"postgres": "text"}),
		field.String("reason_code").SchemaType(map[string]string{"postgres": "text"}),
		field.JSON("scores", json.RawMessage{}).Annotations(entsql.DefaultExpr("'{}'::jsonb")).Default(json.RawMessage("{}")),
		field.Int("latency_ms").SchemaType(map[string]string{"postgres": "integer"}).Annotations(entsql.DefaultExpr("0")).Default(0),
		field.Time("created_at").Annotations(entsql.DefaultExpr("now()")).Default(time.Now),
		field.String("ruleset_version").SchemaType(map[string]string{"postgres": "text"}).Annotations(entsql.DefaultExpr("''::text")).Default(""),
	}
}
func (IntentDecision) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "intent_decisions", Checks: map[string]string{"intent_decisions_decision_check": "((decision = ANY (ARRAY['reply'::text, 'silent'::text, 'defer'::text])))"}}}
}
func (IntentDecision) Indexes() []ent.Index {
	return []ent.Index{index.Fields("job_id").StorageKey("intent_decisions_job")}
}

func (IntentDecision) Edges() []ent.Edge {
	return []ent.Edge{edge.From("job_id_ref", MessageJob.Type).Ref("intent_decisions_job_id").Field("job_id").Unique().Annotations(entsql.OnDelete("CASCADE")).Required(), edge.To("intent_decision_steps_decision_id", IntentDecisionStep.Type).Annotations(entsql.OnDelete("CASCADE"))}
}
