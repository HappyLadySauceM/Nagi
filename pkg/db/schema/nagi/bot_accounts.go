package nagi

import (
	"entgo.io/ent"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
)

type BotAccount struct{ ent.Schema }

func (BotAccount) Fields() []ent.Field {
	return []ent.Field{
		field.Int64("id").SchemaType(map[string]string{"postgres": "bigint"}).StorageKey("bot_id"),
		field.String("nickname").SchemaType(map[string]string{"postgres": "text"}).Annotations(entsql.DefaultExpr("''::text")).Default(""),
		field.Time("last_seen_at").Optional().Nillable(),
	}
}
func (BotAccount) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "bot_accounts", Checks: map[string]string{}}}
}

func (BotAccount) Edges() []ent.Edge {
	return []ent.Edge{edge.To("contacts_bot_id", Contact.Type).Annotations(entsql.OnDelete("NO ACTION")), edge.To("qq_groups_bot_id", QQGroup.Type).Annotations(entsql.OnDelete("NO ACTION")), edge.To("group_members_bot_id", GroupMember.Type).Annotations(entsql.OnDelete("NO ACTION")), edge.To("scope_policies_bot_id", ScopePolicy.Type).Annotations(entsql.OnDelete("NO ACTION")), edge.To("conversations_bot_id", Conversation.Type).Annotations(entsql.OnDelete("NO ACTION")), edge.To("messages_bot_id", Message.Type).Annotations(entsql.OnDelete("NO ACTION")), edge.To("memories_bot_id", Memory.Type).Annotations(entsql.OnDelete("NO ACTION")), edge.To("agent_outbound_messages_bot_id", AgentOutboundMessage.Type).Annotations(entsql.OnDelete("NO ACTION"))}
}
