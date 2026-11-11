package nagi

import (
	"entgo.io/ent"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
	"time"
)

type ScopeMemoryAuthorizationEvent struct{ ent.Schema }

func (ScopeMemoryAuthorizationEvent) Fields() []ent.Field {
	return []ent.Field{
		field.Int64("id"),
		field.Int64("bot_id").SchemaType(map[string]string{"postgres": "bigint"}),
		field.String("scope_type").SchemaType(map[string]string{"postgres": "text"}),
		field.Int64("scope_id").SchemaType(map[string]string{"postgres": "bigint"}),
		field.String("event_kind").SchemaType(map[string]string{"postgres": "text"}),
		field.Bool("authorized").Annotations(entsql.DefaultExpr("false")).Default(false),
		field.Int64("policy_version").SchemaType(map[string]string{"postgres": "bigint"}).Annotations(entsql.DefaultExpr("0")).Default(0),
		field.Time("created_at").Annotations(entsql.DefaultExpr("now()")).Default(time.Now),
	}
}
func (ScopeMemoryAuthorizationEvent) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "scope_memory_authorization_events", Checks: map[string]string{
		"scope_memory_authorization_events_event_kind_check": "((event_kind = ANY (ARRAY['created'::text, 'updated'::text, 'removed'::text, 'bootstrap'::text])))",
		"scope_memory_authorization_events_scope_type_check": "((scope_type = ANY (ARRAY['group'::text, 'private'::text])))",
	}}}
}
func (ScopeMemoryAuthorizationEvent) Indexes() []ent.Index {
	return []ent.Index{index.Fields("bot_id", "scope_type", "scope_id", "id").StorageKey("scope_memory_auth_events_scope")}
}

func (ScopeMemoryAuthorizationEvent) Edges() []ent.Edge {
	return []ent.Edge{edge.From("bot_id_ref", BotAccount.Type).Ref("scope_memory_authorization_events_bot_id").Field("bot_id").Unique().Annotations(entsql.OnDelete("NO ACTION")).Required()}
}
