package nagi

import (
	"entgo.io/ent"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

type GroupMember struct{ ent.Schema }

func (GroupMember) Fields() []ent.Field {
	return []ent.Field{
		field.Int64("id"),
		field.Int64("bot_id").SchemaType(map[string]string{"postgres": "bigint"}),
		field.Int64("group_id").SchemaType(map[string]string{"postgres": "bigint"}),
		field.Int64("user_id").SchemaType(map[string]string{"postgres": "bigint"}),
		field.String("nickname").SchemaType(map[string]string{"postgres": "text"}).Annotations(entsql.DefaultExpr("''::text")).Default(""),
		field.String("card").SchemaType(map[string]string{"postgres": "text"}).Annotations(entsql.DefaultExpr("''::text")).Default(""),
		field.String("role").SchemaType(map[string]string{"postgres": "text"}).Annotations(entsql.DefaultExpr("'member'::text")).Default("member"),
		field.Time("last_seen_at").Optional().Nillable(),
	}
}
func (GroupMember) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "group_members", Checks: map[string]string{}}}
}
func (GroupMember) Indexes() []ent.Index {
	return []ent.Index{index.Fields("bot_id", "group_id", "user_id").Unique().StorageKey("group_members_business_key")}
}

func (GroupMember) Edges() []ent.Edge {
	return []ent.Edge{edge.From("bot_id_ref", BotAccount.Type).Ref("group_members_bot_id").Field("bot_id").Unique().Annotations(entsql.OnDelete("NO ACTION")).Required()}
}
