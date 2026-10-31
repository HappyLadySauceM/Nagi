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

type AgentOutboundMessage struct{ ent.Schema }

func (AgentOutboundMessage) Fields() []ent.Field {
	return []ent.Field{
		field.UUID("id", uuid.UUID{}),
		field.Int64("job_id").SchemaType(map[string]string{"postgres": "bigint"}),
		field.UUID("conversation_id", uuid.UUID{}),
		field.Int64("bot_id").SchemaType(map[string]string{"postgres": "bigint"}),
		field.String("run_id").SchemaType(map[string]string{"postgres": "text"}).Annotations(entsql.DefaultExpr("''::text")).Default(""),
		field.Int("sequence").SchemaType(map[string]string{"postgres": "integer"}),
		field.String("tool_call_id").SchemaType(map[string]string{"postgres": "text"}).Optional().Nillable(),
		field.String("text_content").SchemaType(map[string]string{"postgres": "text"}),
		field.String("status").SchemaType(map[string]string{"postgres": "text"}),
		field.Int64("platform_message_id").SchemaType(map[string]string{"postgres": "bigint"}).Optional().Nillable(),
		field.String("error_code").SchemaType(map[string]string{"postgres": "text"}).Optional().Nillable(),
		field.Time("created_at").Annotations(entsql.DefaultExpr("now()")).Default(time.Now),
		field.Time("updated_at").Annotations(entsql.DefaultExpr("now()")).Default(time.Now),
		field.UUID("claim_token", uuid.UUID{}).Default(uuid.New).Annotations(entsql.DefaultExpr("gen_random_uuid()")),
	}
}
func (AgentOutboundMessage) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "agent_outbound_messages", Checks: map[string]string{"agent_outbound_messages_sequence_check": "((sequence > 0))", "agent_outbound_messages_status_check": "((status = ANY (ARRAY['prepared'::text, 'sending'::text, 'sent'::text, 'failed'::text, 'unknown'::text, 'denied'::text, 'cancelled'::text])))"}}}
}
func (AgentOutboundMessage) Indexes() []ent.Index {
	return []ent.Index{index.Fields("job_id", "sequence").Unique().StorageKey("agent_outbound_messages_job_id_sequence_key"), index.Fields("job_id", "tool_call_id").Unique().StorageKey("agent_outbound_job_tool_call_uidx").Annotations(entsql.IndexWhere("(tool_call_id IS NOT NULL)")), index.Fields("conversation_id", "created_at", "sequence").StorageKey("agent_outbound_conversation_timeline")}
}

func (AgentOutboundMessage) Edges() []ent.Edge {
	return []ent.Edge{edge.To("messages_outbound_id", Message.Type).Annotations(entsql.OnDelete("SET NULL")), edge.From("job_id_ref", MessageJob.Type).Ref("agent_outbound_messages_job_id").Field("job_id").Unique().Annotations(entsql.OnDelete("CASCADE")).Required(), edge.From("conversation_id_ref", Conversation.Type).Ref("agent_outbound_messages_conversation_id").Field("conversation_id").Unique().Annotations(entsql.OnDelete("NO ACTION")).Required(), edge.From("bot_id_ref", BotAccount.Type).Ref("agent_outbound_messages_bot_id").Field("bot_id").Unique().Annotations(entsql.OnDelete("NO ACTION")).Required()}
}
