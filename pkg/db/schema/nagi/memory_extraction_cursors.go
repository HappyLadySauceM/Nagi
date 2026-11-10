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

type MemoryExtractionCursor struct{ ent.Schema }

func (MemoryExtractionCursor) Fields() []ent.Field {
	return []ent.Field{
		field.Int64("id"),
		field.Int64("bot_id").SchemaType(map[string]string{"postgres": "bigint"}),
		field.UUID("conversation_id", uuid.UUID{}),
		field.Int64("last_message_id").SchemaType(map[string]string{"postgres": "bigint"}).Annotations(entsql.DefaultExpr("0")).Default(0),
		field.Int64("last_authorization_event_id").SchemaType(map[string]string{"postgres": "bigint"}).Optional().Nillable(),
		field.Time("last_scanned_at").Optional().Nillable(),
		field.Time("created_at").Annotations(entsql.DefaultExpr("now()")).Default(time.Now),
		field.Time("updated_at").Annotations(entsql.DefaultExpr("now()")).Default(time.Now),
	}
}
func (MemoryExtractionCursor) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "memory_extraction_cursors"}}
}
func (MemoryExtractionCursor) Indexes() []ent.Index {
	return []ent.Index{index.Fields("bot_id", "conversation_id").Unique().StorageKey("memory_extraction_cursors_business_key")}
}

func (MemoryExtractionCursor) Edges() []ent.Edge {
	return []ent.Edge{
		edge.From("bot_id_ref", BotAccount.Type).Ref("memory_extraction_cursors_bot_id").Field("bot_id").Unique().Annotations(entsql.OnDelete("NO ACTION")).Required(),
		edge.From("conversation_id_ref", Conversation.Type).Ref("memory_extraction_cursors_conversation_id").Field("conversation_id").Unique().Annotations(entsql.OnDelete("CASCADE")).Required(),
	}
}
