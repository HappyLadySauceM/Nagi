package nagi

import (
	"entgo.io/ent"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

type QQGroup struct{ ent.Schema }

func (QQGroup) Fields() []ent.Field {
	return []ent.Field{
		field.Int64("id"),
		field.Int64("bot_id").SchemaType(map[string]string{"postgres": "bigint"}),
		field.Int64("group_id").SchemaType(map[string]string{"postgres": "bigint"}),
		field.String("name").SchemaType(map[string]string{"postgres": "text"}).Annotations(entsql.DefaultExpr("''::text")).Default(""),
		field.Int("member_count").SchemaType(map[string]string{"postgres": "integer"}).Annotations(entsql.DefaultExpr("0")).Default(0),
		field.Time("last_seen_at").Optional().Nillable(),
	}
}
func (QQGroup) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "qq_groups", Checks: map[string]string{}}}
}
func (QQGroup) Indexes() []ent.Index {
	return []ent.Index{index.Fields("bot_id", "group_id").Unique().StorageKey("qq_groups_business_key")}
}

func (QQGroup) Edges() []ent.Edge {
	return []ent.Edge{edge.From("bot_id_ref", BotAccount.Type).Ref("qq_groups_bot_id").Field("bot_id").Unique().Annotations(entsql.OnDelete("NO ACTION")).Required()}
}
