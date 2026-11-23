package nagi

import (
	"encoding/json"
	"entgo.io/ent"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
	"github.com/google/uuid"
)

type IntentDecisionRun struct{ ent.Schema }

func (IntentDecisionRun) Fields() []ent.Field {
	return []ent.Field{field.UUID("id", uuid.UUID{}), field.Int64("bot_id"),
		field.UUID("conversation_id", uuid.UUID{}), field.Int64("job_id"), field.UUID("claim_token", uuid.UUID{}),
		field.String("state").SchemaType(map[string]string{"postgres": "text"}), field.Int64("decision_id").Optional().Nillable(),
		field.Time("started_at"), field.Time("finished_at").Optional().Nillable(),
		field.JSON("recovery_decision", json.RawMessage{}).Optional(), field.Bool("reply_started").Default(false)}
}
func (IntentDecisionRun) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "intent_decision_runs"}}
}
func (IntentDecisionRun) Indexes() []ent.Index {
	return []ent.Index{index.Fields("job_id", "claim_token").Unique().StorageKey("intent_decision_runs_claim"), index.Fields("bot_id", "started_at", "id").StorageKey("intent_decision_runs_account"), index.Fields("state", "started_at").StorageKey("intent_decision_runs_state")}
}
func (IntentDecisionRun) Edges() []ent.Edge {
	return []ent.Edge{
		edge.From("job", MessageJob.Type).Ref("intent_runs").Field("job_id").Unique().Required().Annotations(entsql.OnDelete("CASCADE")),
		edge.From("decision", IntentDecision.Type).Ref("intent_run").Field("decision_id").Unique().Annotations(entsql.OnDelete("CASCADE")),
		edge.To("events", IntentRunEvent.Type).Annotations(entsql.OnDelete("CASCADE")),
	}
}

type IntentRunEvent struct{ ent.Schema }

func (IntentRunEvent) Fields() []ent.Field {
	return []ent.Field{
		field.Int64("id"), field.UUID("run_id", uuid.UUID{}), field.Int64("bot_id"), field.Int64("position"), field.Int("sequence"),
		field.String("kind").SchemaType(map[string]string{"postgres": "text"}), field.JSON("data", json.RawMessage{}), field.Time("created_at"),
	}
}
func (IntentRunEvent) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "intent_run_events"}}
}
func (IntentRunEvent) Indexes() []ent.Index {
	return []ent.Index{index.Fields("run_id", "sequence").Unique().StorageKey("intent_run_events_sequence"), index.Fields("bot_id", "position").Unique().StorageKey("intent_run_events_position"), index.Fields("bot_id", "created_at", "position").StorageKey("intent_run_events_time")}
}
func (IntentRunEvent) Edges() []ent.Edge {
	return []ent.Edge{edge.From("run", IntentDecisionRun.Type).Ref("events").Field("run_id").Unique().Required().Annotations(entsql.OnDelete("CASCADE"))}
}

type IntentStreamHead struct{ ent.Schema }

func (IntentStreamHead) Fields() []ent.Field {
	return []ent.Field{field.Int64("id").StorageKey("bot_id"), field.Int64("position"), field.Int64("retained_after").Default(0), field.Int64("retention_revision").Default(0)}
}
func (IntentStreamHead) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "intent_stream_heads"}}
}
