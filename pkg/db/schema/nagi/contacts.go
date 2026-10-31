package nagi

import (
	"entgo.io/ent"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

type Contact struct{ ent.Schema }

func (Contact) Fields() []ent.Field {
	return []ent.Field{
		field.Int64("id"),
		field.Int64("bot_id").SchemaType(map[string]string{"postgres": "bigint"}),
		field.Int64("user_id").SchemaType(map[string]string{"postgres": "bigint"}),
		field.String("nickname").SchemaType(map[string]string{"postgres": "text"}).Annotations(entsql.DefaultExpr("''::text")).Default(""),
		field.String("remark").SchemaType(map[string]string{"postgres": "text"}).Annotations(entsql.DefaultExpr("''::text")).Default(""),
		field.Time("last_seen_at").Optional().Nillable(),
	}
}
func (Contact) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "contacts", Checks: map[string]string{}}}
}
func (Contact) Indexes() []ent.Index {
	return []ent.Index{index.Fields("bot_id", "user_id").Unique().StorageKey("contacts_business_key")}
}

func (Contact) Edges() []ent.Edge {
	return []ent.Edge{edge.From("bot_id_ref", BotAccount.Type).Ref("contacts_bot_id").Field("bot_id").Unique().Annotations(entsql.OnDelete("NO ACTION")).Required()}
}
