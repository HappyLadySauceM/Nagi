package nagi

import (
	"entgo.io/ent"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
	"github.com/google/uuid"
)

type GroupTopicRoute struct{ ent.Schema }

func (GroupTopicRoute) Fields() []ent.Field {
	return []ent.Field{
		field.Int64("id").Annotations(entsql.Annotation{Incremental: &topicNoIncrement}),
		field.UUID("conversation_id", uuid.UUID{}),
		field.String("message_direction").SchemaType(map[string]string{"postgres": "text"}).Default("incoming"),
		field.Time("deadline"),
		field.UUID("claim_token", uuid.UUID{}).Optional().Nillable(),
		field.Time("lease_expires_at").Optional().Nillable(),
		field.String("status").SchemaType(map[string]string{"postgres": "text"}).Default("pending"),
	}
}

func (GroupTopicRoute) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "group_topic_routes", Checks: map[string]string{
		"group_topic_routes_direction_check": "message_direction = 'incoming'::text",
		"group_topic_routes_status_check":    "status IN ('pending', 'routing', 'resolved', 'unrelated', 'expired', 'unavailable', 'stale')",
		"group_topic_routes_lease_check":     "((status = 'routing' AND claim_token IS NOT NULL AND lease_expires_at IS NOT NULL AND lease_expires_at <= deadline) OR (status <> 'routing' AND claim_token IS NULL AND lease_expires_at IS NULL))",
	}}}
}

func (GroupTopicRoute) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("id", "conversation_id").Unique().StorageKey("group_topic_routes_scope_key"),
		index.Fields("conversation_id", "id").StorageKey("group_topic_routes_live_scope").Annotations(entsql.IndexWhere("status IN ('pending', 'routing')")),
		index.Fields("deadline", "id").StorageKey("group_topic_routes_due").Annotations(entsql.IndexWhere("status IN ('pending', 'routing')")),
	}
}
