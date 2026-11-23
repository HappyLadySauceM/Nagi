package nagi

import (
	"entgo.io/ent"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
	"github.com/google/uuid"
)

var topicNoIncrement = false

type GroupTopic struct{ ent.Schema }

func (GroupTopic) Fields() []ent.Field {
	return []ent.Field{
		field.Int64("id").Annotations(entsql.Annotation{Incremental: &topicNoIncrement}),
		field.UUID("conversation_id", uuid.UUID{}),
		field.Int64("version"),
		field.Time("expires_at"),
		field.Bool("closed").Default(false),
	}
}

func (GroupTopic) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "group_topics", Checks: map[string]string{
		"group_topics_version_check": "version > 0",
	}}}
}

func (GroupTopic) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("id", "conversation_id").Unique().StorageKey("group_topics_scope_key"),
		index.Fields("conversation_id", "expires_at", "id").StorageKey("group_topics_open_scope").Annotations(entsql.IndexWhere("NOT closed")),
	}
}
