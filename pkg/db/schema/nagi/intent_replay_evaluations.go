// Intent replay evaluations keep independent saved replay results.
// 意图回放评估保存独立的回放结果。
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

type IntentReplayEvaluation struct{ ent.Schema }

func (IntentReplayEvaluation) Fields() []ent.Field {
	return []ent.Field{
		field.Int64("id").SchemaType(map[string]string{"postgres": "bigint"}),
		field.Int64("decision_id").SchemaType(map[string]string{"postgres": "bigint"}),
		field.Int64("bot_id").SchemaType(map[string]string{"postgres": "bigint"}),
		field.String("mode").SchemaType(map[string]string{"postgres": "text"}),
		field.String("name").SchemaType(map[string]string{"postgres": "text"}).Annotations(entsql.DefaultExpr("''::text")).Default(""),
		field.String("note").SchemaType(map[string]string{"postgres": "text"}).Annotations(entsql.DefaultExpr("''::text")).Default(""),
		field.Bool("changed").Annotations(entsql.DefaultExpr("false")).Default(false),
		field.JSON("before_run", json.RawMessage{}).Annotations(entsql.DefaultExpr("'{}'::jsonb")).Default(json.RawMessage("{}")),
		field.JSON("after_run", json.RawMessage{}).Annotations(entsql.DefaultExpr("'{}'::jsonb")).Default(json.RawMessage("{}")),
		field.JSON("parameters", json.RawMessage{}).Annotations(entsql.DefaultExpr("'{}'::jsonb")).Default(json.RawMessage("{}")),
		field.JSON("cost", json.RawMessage{}).Annotations(entsql.DefaultExpr("'{}'::jsonb")).Default(json.RawMessage("{}")),
		field.JSON("reproduction", json.RawMessage{}).Annotations(entsql.DefaultExpr("'{}'::jsonb")).Default(json.RawMessage("{}")),
		field.Time("created_at").Annotations(entsql.DefaultExpr("now()")).Default(time.Now),
	}
}

func (IntentReplayEvaluation) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "intent_replay_evaluations", Checks: map[string]string{
		"intent_replay_evaluations_mode_check": "((mode = ANY (ARRAY['recalculate'::text, 'model'::text])))",
	}}}
}

func (IntentReplayEvaluation) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("decision_id").StorageKey("intent_replay_evaluations_decision"),
		index.Fields("bot_id", "id").StorageKey("intent_replay_evaluations_bot_id_id"),
	}
}

func (IntentReplayEvaluation) Edges() []ent.Edge {
	return []ent.Edge{
		edge.From("decision_id_ref", IntentDecision.Type).Ref("intent_replay_evaluations_decision_id").Field("decision_id").Unique().Annotations(entsql.OnDelete("CASCADE")).Required(),
	}
}
