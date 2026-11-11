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
	"time"
)

type MemoryExtractionJob struct{ ent.Schema }

func (MemoryExtractionJob) Fields() []ent.Field {
	return []ent.Field{
		field.UUID("id", uuid.UUID{}).Default(uuid.New),
		field.Int64("bot_id").SchemaType(map[string]string{"postgres": "bigint"}),
		field.UUID("conversation_id", uuid.UUID{}),
		field.String("scope_type").SchemaType(map[string]string{"postgres": "text"}),
		field.Int64("scope_id").SchemaType(map[string]string{"postgres": "bigint"}),
		field.Int64("message_id").SchemaType(map[string]string{"postgres": "bigint"}).Optional().Nillable(),
		field.Int64("authorization_event_id").SchemaType(map[string]string{"postgres": "bigint"}),
		field.String("status").SchemaType(map[string]string{"postgres": "text"}).Annotations(entsql.DefaultExpr("'pending'::text")).Default("pending"),
		field.Int("attempts").SchemaType(map[string]string{"postgres": "integer"}).Annotations(entsql.DefaultExpr("0")).Default(0),
		field.Int("round_attempts").SchemaType(map[string]string{"postgres": "integer"}).Annotations(entsql.DefaultExpr("0")).Default(0),
		field.Time("next_run_at").Annotations(entsql.DefaultExpr("now()")).Default(time.Now),
		field.UUID("claim_token", uuid.UUID{}).Optional().Nillable(),
		field.Time("lease_expires_at").Optional().Nillable(),
		field.String("last_error_code").SchemaType(map[string]string{"postgres": "text"}).Optional().Nillable(),
		field.JSON("extraction_payload", json.RawMessage{}).Optional(),
		field.Int("manual_retries").SchemaType(map[string]string{"postgres": "integer"}).Annotations(entsql.DefaultExpr("0")).Default(0),
		field.Time("created_at").Annotations(entsql.DefaultExpr("now()")).Default(time.Now),
		field.Time("updated_at").Annotations(entsql.DefaultExpr("now()")).Default(time.Now),
	}
}
func (MemoryExtractionJob) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "memory_extraction_jobs", Checks: map[string]string{
		"memory_extraction_jobs_status_check":         "((status = ANY (ARRAY['pending'::text, 'processing'::text, 'done'::text, 'failed'::text, 'cancelled'::text])))",
		"memory_extraction_jobs_scope_type_check":     "((scope_type = ANY (ARRAY['group'::text, 'private'::text])))",
		"memory_extraction_jobs_attempts_check":       "(attempts >= 0)",
		"memory_extraction_jobs_round_attempts_check": "(round_attempts >= 0)",
		"memory_extraction_jobs_manual_retries_check": "(manual_retries >= 0)",
		"memory_extraction_jobs_lease_check":          "(((status = 'processing'::text) AND (claim_token IS NOT NULL) AND (lease_expires_at IS NOT NULL)) OR ((status <> 'processing'::text) AND (claim_token IS NULL) AND (lease_expires_at IS NULL)))",
	}}}
}
func (MemoryExtractionJob) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("status", "next_run_at", "id").StorageKey("memory_extraction_jobs_due"),
		index.Fields("bot_id", "conversation_id").StorageKey("memory_extraction_jobs_conversation"),
		index.Fields("message_id").Unique().StorageKey("memory_extraction_jobs_message_key").Annotations(entsql.IndexWhere("(message_id IS NOT NULL)")),
	}
}

func (MemoryExtractionJob) Edges() []ent.Edge {
	return []ent.Edge{
		edge.From("bot_id_ref", BotAccount.Type).Ref("memory_extraction_jobs_bot_id").Field("bot_id").Unique().Annotations(entsql.OnDelete("NO ACTION")).Required(),
		edge.From("conversation_id_ref", Conversation.Type).Ref("memory_extraction_jobs_conversation_id").Field("conversation_id").Unique().Annotations(entsql.OnDelete("CASCADE")).Required(),
		edge.From("message_id_ref", Message.Type).Ref("memory_extraction_jobs_message_id").Field("message_id").Unique().Annotations(entsql.OnDelete("SET NULL")),
	}
}
