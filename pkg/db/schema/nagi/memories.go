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

type Memory struct{ ent.Schema }

func (Memory) Fields() []ent.Field {
	return []ent.Field{
		field.UUID("id", uuid.UUID{}).StorageKey("memory_id"),
		field.Int64("bot_id").SchemaType(map[string]string{"postgres": "bigint"}),
		field.String("visibility").SchemaType(map[string]string{"postgres": "text"}),
		field.String("owner_key").SchemaType(map[string]string{"postgres": "text"}).Annotations(entsql.DefaultExpr("''::text")).Default(""),
		field.String("content").SchemaType(map[string]string{"postgres": "text"}),
		field.Int64("source_message_id").SchemaType(map[string]string{"postgres": "bigint"}).Optional().Nillable(),
		field.String("source").SchemaType(map[string]string{"postgres": "text"}).Annotations(entsql.DefaultExpr("''::text")).Default(""),
		field.Float32("confidence").Annotations(entsql.DefaultExpr("0")).Default(0),
		field.String("status").SchemaType(map[string]string{"postgres": "text"}).Annotations(entsql.DefaultExpr("'candidate'::text")).Default("candidate"),
		field.String("approved_by").SchemaType(map[string]string{"postgres": "text"}).Annotations(entsql.DefaultExpr("''::text")).Default(""),
		field.Time("expires_at").Optional().Nillable(),
		field.Time("created_at").Annotations(entsql.DefaultExpr("now()")).Default(time.Now),
		field.Time("updated_at").Annotations(entsql.DefaultExpr("now()")).Default(time.Now),
	}
}
func (Memory) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "memories", Checks: map[string]string{"memories_visibility_check": "((visibility = ANY (ARRAY['bot_shared'::text, 'group'::text, 'private_user'::text, 'conversation'::text])))", "memories_status_check": "((status = ANY (ARRAY['candidate'::text, 'active'::text, 'superseded'::text, 'deleted'::text])))"}}}
}
func (Memory) Indexes() []ent.Index {
	return []ent.Index{index.Fields("bot_id", "visibility", "owner_key", "status").StorageKey("memories_visibility")}
}

func (Memory) Edges() []ent.Edge {
	return []ent.Edge{edge.From("bot_id_ref", BotAccount.Type).Ref("memories_bot_id").Field("bot_id").Unique().Annotations(entsql.OnDelete("NO ACTION")).Required(), edge.From("source_message_id_ref", Message.Type).Ref("memories_source_message_id").Field("source_message_id").Unique().Annotations(entsql.OnDelete("SET NULL"))}
}
