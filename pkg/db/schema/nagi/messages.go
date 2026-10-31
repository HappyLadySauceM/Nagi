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

type Message struct{ ent.Schema }

func (Message) Fields() []ent.Field {
	return []ent.Field{
		field.Int64("id").SchemaType(map[string]string{"postgres": "bigint"}),
		field.UUID("conversation_id", uuid.UUID{}),
		field.Int64("bot_id").SchemaType(map[string]string{"postgres": "bigint"}),
		field.String("direction").SchemaType(map[string]string{"postgres": "text"}),
		field.Int64("platform_message_id").SchemaType(map[string]string{"postgres": "bigint"}).Optional().Nillable(),
		field.Int64("sender_id").SchemaType(map[string]string{"postgres": "bigint"}),
		field.String("text_content").SchemaType(map[string]string{"postgres": "text"}).Annotations(entsql.DefaultExpr("''::text")).Default(""),
		field.JSON("segments", json.RawMessage{}).Annotations(entsql.DefaultExpr("'[]'::jsonb")).Default(json.RawMessage("[]")),
		field.String("sent_status").SchemaType(map[string]string{"postgres": "text"}).Optional().Nillable(),
		field.Time("created_at").Annotations(entsql.DefaultExpr("now()")).Default(time.Now),
		field.UUID("outbound_id", uuid.UUID{}).Optional().Nillable(),
	}
}
func (Message) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "messages", Checks: map[string]string{"messages_direction_check": "((direction = ANY (ARRAY['incoming'::text, 'outgoing'::text])))", "messages_sent_status_check": "(((sent_status IS NULL) OR (sent_status = ANY (ARRAY['sent'::text, 'failed'::text, 'unknown'::text]))))"}}}
}
func (Message) Indexes() []ent.Index {
	return []ent.Index{index.Fields("bot_id", "conversation_id", "platform_message_id").Unique().StorageKey("messages_incoming_event_uniq").Annotations(entsql.IndexWhere("((direction = 'incoming'::text) AND (platform_message_id IS NOT NULL))")), index.Fields("conversation_id", "created_at", "id").StorageKey("messages_conversation_timeline"), index.Fields("outbound_id").Unique().StorageKey("messages_outbound_unique").Annotations(entsql.IndexWhere("(outbound_id IS NOT NULL)")), index.Fields("created_at").StorageKey("messages_created_at")}
}

func (Message) Edges() []ent.Edge {
	return []ent.Edge{edge.From("conversation_id_ref", Conversation.Type).Ref("messages_conversation_id").Field("conversation_id").Unique().Annotations(entsql.OnDelete("NO ACTION")).Required(), edge.From("bot_id_ref", BotAccount.Type).Ref("messages_bot_id").Field("bot_id").Unique().Annotations(entsql.OnDelete("NO ACTION")).Required(), edge.To("message_jobs_incoming_message_id", MessageJob.Type).Annotations(entsql.OnDelete("NO ACTION")), edge.From("outbound_id_ref", AgentOutboundMessage.Type).Ref("messages_outbound_id").Field("outbound_id").Unique().Annotations(entsql.OnDelete("SET NULL")), edge.To("memories_source_message_id", Memory.Type).Annotations(entsql.OnDelete("SET NULL"))}
}
