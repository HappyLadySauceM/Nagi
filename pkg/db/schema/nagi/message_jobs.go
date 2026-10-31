package nagi

import (
	"entgo.io/ent"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
	"github.com/google/uuid"
	"time"
)

type MessageJob struct{ ent.Schema }

func (MessageJob) Fields() []ent.Field {
	return []ent.Field{
		field.Int64("id").SchemaType(map[string]string{"postgres": "bigint"}),
		field.Int64("incoming_message_id").SchemaType(map[string]string{"postgres": "bigint"}),
		field.String("status").SchemaType(map[string]string{"postgres": "text"}).Annotations(entsql.DefaultExpr("'pending'::text")).Default("pending"),
		field.Int("attempts").SchemaType(map[string]string{"postgres": "integer"}).Annotations(entsql.DefaultExpr("0")).Default(0),
		field.Time("next_run_at").Annotations(entsql.DefaultExpr("now()")).Default(time.Now),
		field.Time("updated_at").Annotations(entsql.DefaultExpr("now()")).Default(time.Now),
		field.String("last_error_code").SchemaType(map[string]string{"postgres": "text"}).Optional().Nillable(),
		field.String("traceparent").SchemaType(map[string]string{"postgres": "text"}).Annotations(entsql.DefaultExpr("''::text")).Default(""),
		field.String("tracestate").SchemaType(map[string]string{"postgres": "text"}).Annotations(entsql.DefaultExpr("''::text")).Default(""),
		field.String("delivery_outcome").SchemaType(map[string]string{"postgres": "text"}).Annotations(entsql.DefaultExpr("''::text")).Default(""),
		field.String("delivery_reason").SchemaType(map[string]string{"postgres": "text"}).Annotations(entsql.DefaultExpr("''::text")).Default(""),
		field.Int("denied_count").SchemaType(map[string]string{"postgres": "integer"}).Annotations(entsql.DefaultExpr("0")).Default(0),
		field.UUID("claim_token", uuid.UUID{}).Optional().Nillable(),
		field.Time("lease_expires_at").Optional().Nillable(),
	}
}
func (MessageJob) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "message_jobs", Checks: map[string]string{"message_jobs_status_check": "((status = ANY (ARRAY['pending'::text, 'processing'::text, 'done'::text, 'failed'::text, 'cancelled'::text])))", "message_jobs_denied_count_check": "((denied_count >= 0))", "message_jobs_lease_check": "((status = 'processing' AND claim_token IS NOT NULL AND lease_expires_at IS NOT NULL) OR (status <> 'processing' AND claim_token IS NULL AND lease_expires_at IS NULL))"}}}
}
func (MessageJob) Indexes() []ent.Index {
	return []ent.Index{index.Fields("incoming_message_id").Unique().StorageKey("message_jobs_incoming_message_id_key"), index.Fields("status", "next_run_at", "id").StorageKey("message_jobs_ready"), index.Fields("lease_expires_at").StorageKey("message_jobs_expired").Annotations(entsql.IndexWhere("status = 'processing'"))}
}

func (MessageJob) Edges() []ent.Edge {
	return []ent.Edge{edge.From("incoming_message_id_ref", Message.Type).Ref("message_jobs_incoming_message_id").Field("incoming_message_id").Unique().Annotations(entsql.OnDelete("NO ACTION")).Required(), edge.To("intent_decisions_job_id", IntentDecision.Type).Annotations(entsql.OnDelete("CASCADE")), edge.To("agent_run_budgets_job_id", AgentRunBudget.Type).Annotations(entsql.OnDelete("CASCADE")), edge.To("agent_outbound_messages_job_id", AgentOutboundMessage.Type).Annotations(entsql.OnDelete("CASCADE"))}
}
