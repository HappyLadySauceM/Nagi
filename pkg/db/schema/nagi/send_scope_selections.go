package nagi

import (
	"entgo.io/ent"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
	"github.com/google/uuid"
)

type SendScopeSelection struct{ ent.Schema }

func (SendScopeSelection) Fields() []ent.Field {
	return []ent.Field{
		field.UUID("id", uuid.UUID{}).Default(uuid.New).Annotations(entsql.DefaultExpr("gen_random_uuid()")),
		field.Int64("bot_id"),
		field.String("scope_type").SchemaType(map[string]string{"postgres": "text"}),
		field.Int64("scope_id"),
	}
}

func (SendScopeSelection) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "send_scope_selections", Checks: map[string]string{"send_scope_selections_exact_scope_check": "bot_id > 0 AND scope_id > 0 AND scope_type IN ('group', 'private')"}}}
}

func (SendScopeSelection) Indexes() []ent.Index {
	return []ent.Index{index.Fields("bot_id", "scope_type", "scope_id").Unique().StorageKey("send_scope_selections_scope_key")}
}
