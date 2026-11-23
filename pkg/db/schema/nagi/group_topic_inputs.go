package nagi

import (
	"entgo.io/ent"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
	"github.com/google/uuid"
)

type GroupTopicInput struct{ ent.Schema }

func (GroupTopicInput) Fields() []ent.Field {
	return []ent.Field{
		field.UUID("id", uuid.UUID{}).Default(uuid.New),
		field.UUID("conversation_id", uuid.UUID{}),
		field.Int64("topic_id"),
		field.Int64("version"),
		field.Int64("route_id"),
		field.String("action").SchemaType(map[string]string{"postgres": "text"}),
	}
}

func (GroupTopicInput) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "group_topic_inputs", Checks: map[string]string{
		"group_topic_inputs_version_check": "version > 0",
		"group_topic_inputs_action_check":  "action IN ('opened', 'waiting', 'follow_up', 'contribution', 'corrected', 'replaced', 'merged', 'close')",
	}}}
}

func (GroupTopicInput) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("topic_id", "version").Unique().StorageKey("group_topic_inputs_topic_version_key"),
		index.Fields("route_id").Unique().StorageKey("group_topic_inputs_route_key"),
	}
}
