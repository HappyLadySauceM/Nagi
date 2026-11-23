package nagi

import (
	"entgo.io/ent"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
	"github.com/google/uuid"
)

type GroupTopicRouteCandidate struct{ ent.Schema }

func (GroupTopicRouteCandidate) Fields() []ent.Field {
	return []ent.Field{
		field.UUID("id", uuid.UUID{}).Default(uuid.New),
		field.UUID("conversation_id", uuid.UUID{}),
		field.Int64("route_id"),
		field.Int64("topic_id"),
		field.Int64("version"),
	}
}

func (GroupTopicRouteCandidate) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "group_topic_route_candidates", Checks: map[string]string{
		"group_topic_route_candidates_version_check": "version > 0",
	}}}
}

func (GroupTopicRouteCandidate) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("route_id", "topic_id").Unique().StorageKey("group_topic_route_candidates_route_topic_key"),
		index.Fields("topic_id", "route_id").StorageKey("group_topic_route_candidates_topic_routes"),
	}
}
