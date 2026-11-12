// Intent decision snapshots preserve the exact input and model answer used for replay.
// 意图决策快照保存回放所需的原始输入与模型回答。
package nagi

import (
	"encoding/json"
	"time"

	"entgo.io/ent"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

type IntentDecisionSnapshot struct{ ent.Schema }

func (IntentDecisionSnapshot) Fields() []ent.Field {
	return []ent.Field{
		field.Int64("id").SchemaType(map[string]string{"postgres": "bigint"}),
		field.Int64("decision_id").SchemaType(map[string]string{"postgres": "bigint"}),
		field.String("run_id").SchemaType(map[string]string{"postgres": "text"}).Annotations(entsql.DefaultExpr("''::text")).Default(""),
		field.String("flow_version").SchemaType(map[string]string{"postgres": "text"}).Annotations(entsql.DefaultExpr("''::text")).Default(""),
		field.String("snapshot_version").SchemaType(map[string]string{"postgres": "text"}).Annotations(entsql.DefaultExpr("''::text")).Default(""),
		field.JSON("input", json.RawMessage{}).Annotations(entsql.DefaultExpr("'{}'::jsonb")).Default(json.RawMessage("{}")),
		field.JSON("semantic", json.RawMessage{}).Annotations(entsql.DefaultExpr("'{}'::jsonb")).Default(json.RawMessage("{}")),
		field.JSON("prompt", json.RawMessage{}).Annotations(entsql.DefaultExpr("'{}'::jsonb")).Default(json.RawMessage("{}")),
		field.JSON("rule", json.RawMessage{}).Annotations(entsql.DefaultExpr("'{}'::jsonb")).Default(json.RawMessage("{}")),
		field.String("ruleset_version").SchemaType(map[string]string{"postgres": "text"}).Annotations(entsql.DefaultExpr("''::text")).Default(""),
		field.JSON("missing_fields", json.RawMessage{}).Annotations(entsql.DefaultExpr("'[]'::jsonb")).Default(json.RawMessage("[]")),
		field.Time("created_at").Annotations(entsql.DefaultExpr("now()")).Default(time.Now),
	}
}

func (IntentDecisionSnapshot) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "intent_decision_snapshots"}}
}

func (IntentDecisionSnapshot) Indexes() []ent.Index {
	return []ent.Index{index.Fields("decision_id").Unique().StorageKey("intent_decision_snapshots_decision_id_key")}
}

func (IntentDecisionSnapshot) Edges() []ent.Edge {
	return []ent.Edge{
		edge.From("decision_id_ref", IntentDecision.Type).Ref("intent_decision_snapshots_decision_id").Field("decision_id").Unique().Annotations(entsql.OnDelete("CASCADE")).Required(),
	}
}
