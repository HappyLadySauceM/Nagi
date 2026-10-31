package nagi

import (
	"entgo.io/ent"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
	"github.com/google/uuid"
)

type Conversation struct{ ent.Schema }

func (Conversation) Fields() []ent.Field {
	return []ent.Field{
		field.UUID("id", uuid.UUID{}),
		field.Int64("bot_id").SchemaType(map[string]string{"postgres": "bigint"}),
		field.String("kind").SchemaType(map[string]string{"postgres": "text"}),
		field.Int64("target_id").SchemaType(map[string]string{"postgres": "bigint"}),
		field.Time("last_message_at").Optional().Nillable(),
	}
}
func (Conversation) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "conversations", Checks: map[string]string{"conversations_kind_check": "((kind = ANY (ARRAY['group'::text, 'private'::text])))"}}}
}
func (Conversation) Indexes() []ent.Index {
	return []ent.Index{index.Fields("bot_id", "kind", "target_id").Unique().StorageKey("conversations_bot_id_kind_target_id_key")}
}

func (Conversation) Edges() []ent.Edge {
	return []ent.Edge{edge.From("bot_id_ref", BotAccount.Type).Ref("conversations_bot_id").Field("bot_id").Unique().Annotations(entsql.OnDelete("NO ACTION")).Required(), edge.To("messages_conversation_id", Message.Type).Annotations(entsql.OnDelete("NO ACTION")), edge.To("agent_outbound_messages_conversation_id", AgentOutboundMessage.Type).Annotations(entsql.OnDelete("NO ACTION"))}
}
